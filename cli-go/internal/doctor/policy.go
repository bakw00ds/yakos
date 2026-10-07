package doctor

// policy.go — `yakos doctor --policy` (K-137): a report of risky configurations.
//
// Each finding is one line with a severity and a fix hint. The report is
// advisory: it never changes the exit status (a gate would make the operator's
// own deliberate choices, such as allow_unsandboxed_runtimes, fail every run),
// and it names environment variables and booleans only. No environment value,
// file content or token material is ever printed.
//
// The checks live behind CheckPolicy so the console can call the same function
// later; Run's --policy mode only formats its result.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/codexhome"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	yakruntime "github.com/bakw00ds/yakos/internal/runtime"
)

// PolicySeverity ranks a finding. High means a protection is off or bypassable
// right now in a normal run, medium that a protection is bypassed by a setting
// or will refuse to work, low that a hardening or readiness step is missing.
type PolicySeverity string

const (
	PolicyHigh   PolicySeverity = "high"
	PolicyMedium PolicySeverity = "medium"
	PolicyLow    PolicySeverity = "low"
)

func (s PolicySeverity) rank() int {
	switch s {
	case PolicyHigh:
		return 3
	case PolicyMedium:
		return 2
	}
	return 1
}

// PolicyFinding is one risky configuration. Message and Fix are single lines.
type PolicyFinding struct {
	// ID is a stable machine key (kebab-case, plus ":<VARIABLE>" for a
	// per-variable finding), safe to key console state on.
	ID       string         `json:"id"`
	Severity PolicySeverity `json:"severity"`
	Message  string         `json:"message"`
	Fix      string         `json:"fix"`
}

// PolicyEnv is what CheckPolicy reads. Every field is optional: the zero value
// reads the real process ($HOME, os.Getenv, exec.LookPath) and reports the two
// installation facts below as false.
type PolicyEnv struct {
	// Home is the user's home directory; the state directory is Home/.yakos-state.
	// Empty means os.UserHomeDir(); if that fails too, the file checks are skipped.
	Home string
	// Getenv reads the process environment. Only variable NAMES are ever reported.
	Getenv func(string) string
	// LookPath finds a command on PATH.
	LookPath func(string) (string, error)

	// BashTreePresent reports that the bash CLI tree the YAKOS_IMPL gate would
	// route to is installed (passthrough.BashYakosExists on the executable's
	// root). The caller computes it because it depends on where the binary lives.
	BashTreePresent bool
	// SDKSidecarSelectable reports that the Agent-SDK engine is installed: node
	// and the sidecar bundle are present (the conditions under which
	// interactive.NewSDKEngineFactory succeeds). That is true on most developer
	// machines and is not a risk by itself, because the engine runs only when the
	// console is started with --console-structured-questions. The caller computes
	// it.
	SDKSidecarSelectable bool
	// SDKSidecarEnabled reports that the console was started with
	// --console-structured-questions and built its engine factory, so structured
	// questions go through the SDK sidecar. Only the daemon knows this: `yakos
	// doctor` cannot and leaves it false, which is why it reports the missing key
	// as a low heads-up there. It implies SDKSidecarSelectable.
	SDKSidecarEnabled bool

	// ProbeRuntime reports whether a runtime's CLI is on PATH and looks signed in.
	// The caller wraps auth.ProbeRuntime: its OS keyring read is bounded by the
	// context it is given, and where that read runs belongs to the caller, not to
	// this package. Nil skips the sign-in check.
	ProbeRuntime func(ctx context.Context, id string) RuntimeProbe
}

// RuntimeProbe is what a caller-supplied probe learned about one runtime CLI.
type RuntimeProbe struct {
	// CLIPresent is true when the runtime's CLI is on PATH.
	CLIPresent bool
	// Authed is true when credentials look configured. The check is best effort,
	// like `yakos auth status`: it only checks that a login exists (including a
	// look at yakOS's own keyring entry), prints no credential and makes no
	// network call.
	Authed bool
	// Note says something the probe could not settle, for the report (the OS
	// keyring did not answer in time). Empty when there is nothing to add.
	Note string
}

func (e PolicyEnv) withDefaults() PolicyEnv {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.LookPath == nil {
		e.LookPath = defaultLookPath
	}
	if e.Home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			e.Home = h
		}
	}
	return e
}

// CheckPolicy returns the risky configurations found, most severe first and by
// ID within a severity, so the same machine always yields the same report. An
// empty result means nothing was found.
func CheckPolicy(env PolicyEnv) []PolicyFinding {
	e := env.withDefaults()
	var out []PolicyFinding
	out = append(out, checkSDKSidecar(e)...)
	out = append(out, checkRouterPolicy(e)...)
	out = append(out, checkDefaultRuntime(e)...)
	out = append(out, checkBashDispatch(e)...)
	out = append(out, checkCodexProfile(e)...)
	out = append(out, checkAgySignIn(e)...)
	out = append(out, checkStatePathOverrides(e)...)
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := out[i].Severity.rank(), out[j].Severity.rank(); ri != rj {
			return ri > rj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ---- SDK sidecar ---------------------------------------------------------------

// checkSDKSidecar reports an Agent-SDK engine that would refuse to start. Since
// the K-137 gate it cannot run on a claude.ai login, so this is the
// operator-facing half: they would otherwise find out from a failed chat turn.
//
// The severity follows whether the engine is actually in use. node and the
// bundle exist on most developer machines, so "installed and no key" is only a
// low heads-up: nothing runs until the console is started with
// --console-structured-questions. When the daemon says it enabled the engine
// (SDKSidecarEnabled) structured questions will fail, which is medium.
func checkSDKSidecar(e PolicyEnv) []PolicyFinding {
	if !e.SDKSidecarSelectable && !e.SDKSidecarEnabled {
		return nil
	}
	err := yakruntime.CheckSDKAPIKey(e.Getenv)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, yakruntime.ErrSDKAPIKeyIsOAuthToken):
		return []PolicyFinding{{
			ID:       "sdk-sidecar-oauth-api-key",
			Severity: PolicyMedium,
			Message:  "the SDK sidecar (structured questions) can be selected, but ANTHROPIC_API_KEY holds a subscription OAuth token, not an API key: it will refuse to start",
			Fix:      "put an API key from the Anthropic Console in ANTHROPIC_API_KEY, or unset it and use the CLI engine (interactive chat without structured questions)",
		}}
	}
	if e.SDKSidecarEnabled {
		return []PolicyFinding{{
			ID:       "sdk-sidecar-no-api-key",
			Severity: PolicyMedium,
			Message:  "the console's SDK sidecar (structured questions) is enabled, but ANTHROPIC_API_KEY is not set in this environment: it will refuse to start rather than run on your claude.ai login",
			Fix:      "export ANTHROPIC_API_KEY for the daemon, or use the CLI engine (interactive chat without structured questions)",
		}}
	}
	return []PolicyFinding{{
		ID:       "sdk-sidecar-no-api-key",
		Severity: PolicyLow,
		Message:  "the SDK sidecar (structured questions) is installed, and ANTHROPIC_API_KEY is not set in this environment: if the console is started with --console-structured-questions it will refuse to start rather than run on your claude.ai login",
		Fix:      "export ANTHROPIC_API_KEY for the daemon before you enable it, or keep using the CLI engine (interactive chat without structured questions)",
	}}
}

// ---- router policy ---------------------------------------------------------------

const routerPolicyLabel = "~/.yakos-state/router-policy.yml"

// checkRouterPolicy reads the owner-only policy through the loader dispatch
// uses (routerpolicy.Load: same trust check, $HOME/.yakos-state only, never
// YAKOS_DISPATCH_LOG), so what is reported is what dispatch would do.
func checkRouterPolicy(e PolicyEnv) []PolicyFinding {
	// With no home there is no policy to read, and that is what dispatch does too:
	// statepath.TrustedDir() is empty, so it reads nothing and allows nothing. It
	// deliberately has no temp-directory fallback (another local user could plant a
	// policy there), unlike the default-runtime file below. The doctor must not read
	// one either, and must never resolve this path against the working directory.
	if e.Home == "" {
		return nil
	}
	stateDir := filepath.Join(e.Home, ".yakos-state")
	pol, err := routerpolicy.Load(stateDir)
	if err != nil {
		var pe *fs.PathError
		var reason string
		switch {
		case errors.Is(err, routerpolicy.ErrUntrusted):
			reason = "it " + trustReason(err, routerpolicy.Path(stateDir))
		case errors.As(err, &pe):
			reason = "it could not be read"
		default:
			// The parser's message can quote the offending line; print none of it.
			reason = "it could not be parsed as YAML"
		}
		return []PolicyFinding{{
			ID:       "router-policy-refused",
			Severity: PolicyMedium,
			Message:  fmt.Sprintf("%s was refused and is ignored: %s; codex keeps its sandbox and agy keeps --sandbox, and any rule in it does not apply", routerPolicyLabel, reason),
			Fix:      "make it a regular file you own with valid YAML and mode 600 (chmod 600 " + routerPolicyLabel + ")",
		}}
	}
	classes := gatewayClassFindings(e, pol)
	if len(pol.AllowUnsandboxedRuntimes) == 0 {
		return classes
	}
	var harnesses []string
	for _, name := range pol.AllowUnsandboxedRuntimes {
		n := strings.ToLower(strings.TrimSpace(name))
		if (n == "codex" || n == "agy") && !containsString(harnesses, n) {
			harnesses = append(harnesses, n)
		}
	}
	if len(harnesses) == 0 {
		return append(classes, PolicyFinding{
			ID:       "router-policy-no-effect",
			Severity: PolicyLow,
			Message:  "allow_unsandboxed_runtimes in " + routerPolicyLabel + " lists no runtime yakOS can run unsandboxed (only codex and agy), so it has no effect",
			Fix:      "correct the runtime names or remove the entries",
		})
	}
	return append(classes, PolicyFinding{
		ID:       "router-policy-unsandboxed",
		Severity: PolicyHigh,
		Message:  fmt.Sprintf("%s run WITHOUT their sandbox flags: allow_unsandboxed_runtimes in %s", strings.Join(harnesses, ", "), routerPolicyLabel),
		Fix:      "remove the runtime from allow_unsandboxed_runtimes (or delete the file) to restore codex --sandbox workspace-write and agy --sandbox",
	})
}

// gatewayClassFindings lists the active gateway_classes aliases (K-141): class,
// variable name and model id only. It says which entries the operator's own
// environment overrides, because the operator's variable always wins. A refused
// key is a finding of its own.
func gatewayClassFindings(e PolicyEnv, pol routerpolicy.File) []PolicyFinding {
	classes, warns := pol.Classes()
	var out []PolicyFinding
	for _, w := range warns {
		out = append(out, PolicyFinding{
			ID:       "router-policy-gateway-classes-refused",
			Severity: PolicyMedium,
			Message:  w,
			Fix:      "use only these classes with Claude model ids: " + strings.Join(routerpolicy.ClassNames(), ", "),
		})
	}
	if len(classes) == 0 {
		return out
	}
	var active, overridden []string
	for _, c := range classes {
		entry := fmt.Sprintf("%s (%s=%s)", c.Class, c.EnvName, c.Model)
		if e.Getenv != nil && e.Getenv(c.EnvName) != "" {
			overridden = append(overridden, c.Class+" ("+c.EnvName+")")
			continue
		}
		active = append(active, entry)
	}
	msg := "gateway_classes in " + routerPolicyLabel + " sets Claude Code model variables on claude runs: " + strings.Join(active, ", ")
	if len(active) == 0 {
		msg = "gateway_classes in " + routerPolicyLabel + " sets nothing: every class is overridden by your environment"
	}
	if len(overridden) > 0 {
		msg += "; your own environment wins for: " + strings.Join(overridden, ", ")
	}
	return append(out, PolicyFinding{
		ID:       "router-policy-gateway-classes",
		Severity: PolicyLow,
		Message:  msg,
		Fix:      "informational; remove the class from gateway_classes to stop aliasing it (a daemon needs a restart to re-read the policy)",
	})
}

// trustReason turns the loader's "router policy ignored: <path> is a symlink"
// into "is a symlink": the absolute path stays out of the report.
func trustReason(err error, path string) string {
	return strings.TrimPrefix(err.Error(), routerpolicy.ErrUntrusted.Error()+": "+path+" ")
}

// ---- default runtime ---------------------------------------------------------------

// checkDefaultRuntime reports a default-runtime file that dispatch refuses. The
// default steers every unpinned dispatch to a vendor, so the Go dispatcher reads
// it only when no one else could have written it and ignores it otherwise, with a
// one-line warning. This asks the same function dispatch asks
// (auth.ReadDefaultRuntime) about the same directory and reports its warning word
// for word, so the trust decision, the file name and the wording cannot drift
// apart. The warning names what was refused by role and carries no path.
func checkDefaultRuntime(e PolicyEnv) []PolicyFinding {
	_, warning := auth.ReadDefaultRuntime(e.dispatchStateDir())
	if warning == "" {
		return nil // absent, unreadable or trusted: nothing refused
	}
	return []PolicyFinding{{
		ID:       "default-runtime-refused",
		Severity: PolicyMedium,
		Message:  "dispatch is " + warning,
		Fix:      "make it a regular file you own with mode 600 in a directory only you can write, or run 'yakos auth set-default <runtime>' to write it again",
	}}
}

// dispatchStateDir is the directory dispatch reads the default runtime from,
// statepath.Dir() resolved from this environment: YAKOS_DISPATCH_LOG when set,
// else $HOME/.yakos-state, and with no home to resolve the same fallback
// statepath.Dir() has, a .yakos-state directory under the temp directory.
func (e PolicyEnv) dispatchStateDir() string {
	if v := e.Getenv("YAKOS_DISPATCH_LOG"); v != "" {
		return v
	}
	if e.Home == "" {
		return filepath.Join(os.TempDir(), ".yakos-state")
	}
	return filepath.Join(e.Home, ".yakos-state")
}

// ---- agy sign-in ---------------------------------------------------------------------

// agyProbeTimeout bounds the sign-in probe. auth.ProbeRuntime caps its own OS
// keyring read at two seconds; the report must stay quick even so. A variable so
// a test can shorten it.
var agyProbeTimeout = 3 * time.Second

// checkAgySignIn reports agy on PATH that does not look signed in, so the
// operator learns before a dispatch fails. It needs a caller-supplied probe and
// is silent without one.
func checkAgySignIn(e PolicyEnv) []PolicyFinding {
	if e.ProbeRuntime == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agyProbeTimeout)
	defer cancel()
	// The probe gets the deadline, but one that ignores its context must not hang
	// the report either: past the deadline the check says nothing rather than guess.
	done := make(chan RuntimeProbe, 1)
	go func() { done <- e.ProbeRuntime(ctx, "agy") }()
	var p RuntimeProbe
	select {
	case p = <-done:
	case <-ctx.Done():
		return nil
	}
	if !p.CLIPresent || p.Authed {
		return nil
	}
	msg := "agy is on PATH but does not look signed in, so a dispatch to it fails until it is"
	if p.Note != "" {
		msg += " (" + p.Note + ")"
	}
	return []PolicyFinding{{
		ID:       "agy-not-signed-in",
		Severity: PolicyLow,
		Message:  msg,
		Fix:      "run 'yakos auth login agy'; this check is best effort: it only checks that a login exists and prints no credential",
	}}
}

// ---- bash dispatch ----------------------------------------------------------------

// checkBashDispatch reports `yakos dispatch` reaching the bash CLI while codex or
// agy is installed. The bash adapters still start both with the bypass flags
// (cli/lib/runtimes/codex.sh, agy.sh) until the Go dispatcher becomes the
// default (K-143); the Go dispatcher passes the sandbox flags.
func checkBashDispatch(e PolicyEnv) []PolicyFinding {
	impl := strings.TrimSpace(e.Getenv("YAKOS_IMPL"))
	if impl == "go" || !e.BashTreePresent {
		return nil // Go-native, or nothing for bash to run (an explicit bash errors out)
	}
	via := "YAKOS_IMPL is not set to go and the bash CLI tree is installed"
	if impl == "bash" {
		via = "YAKOS_IMPL=bash"
	}
	var installed []string
	for _, rt := range []string{"codex", "agy"} {
		if _, err := e.LookPath(rt); err == nil {
			installed = append(installed, rt)
		}
	}
	if len(installed) == 0 {
		return nil
	}
	return []PolicyFinding{{
		ID:       "bash-dispatch-unsandboxed",
		Severity: PolicyHigh,
		Message: fmt.Sprintf("yakos dispatch runs through the bash CLI (%s), which starts %s WITHOUT their sandbox flags; the Go dispatcher passes codex --sandbox workspace-write and agy --sandbox",
			via, strings.Join(installed, ", ")),
		Fix: "export YAKOS_IMPL=go (the bash dispatch path keeps the bypass flags until K-143 makes Go the default)",
	}}
}

// ---- codex readiness ---------------------------------------------------------------

// checkCodexProfile reports codex on PATH with no yakOS-owned login profile and
// no API key: dispatch then shares the operator's own codex login, and
// concurrent token refreshes can sign one of them out (openai/codex#48465).
func checkCodexProfile(e PolicyEnv) []PolicyFinding {
	if _, err := e.LookPath("codex"); err != nil {
		return nil
	}
	if e.Getenv("OPENAI_API_KEY") != "" {
		return nil
	}
	if _, isolated := codexhome.Effective(e.Home, e.Getenv); isolated {
		return nil
	}
	return []PolicyFinding{{
		ID:       "codex-shared-login",
		Severity: PolicyLow,
		Message:  "codex is on PATH but yakOS has no codex login of its own: dispatch shares your interactive codex login (CODEX_HOME or ~/.codex), and concurrent token refreshes can sign one of them out (openai/codex#48465)",
		Fix:      "run 'yakos auth login codex' to give yakOS its own profile in ~/.yakos-state/codex-home",
	}}
}

// ---- state path overrides -------------------------------------------------------------

// statePathOverrides lists the variables that relocate yakOS state. A project
// can set environment variables for the processes it starts (a committed
// .claude/settings.json env block, K-129), so a set one is worth knowing about.
// The values are never printed.
var statePathOverrides = []struct{ name, what string }{
	{"YAKOS_DISPATCH_LOG", "the yakOS state directory (dispatch log, budget state, cost data)"},
	{"YAKOS_STATE_DIR", "the console and mTLS state directory"},
	{"YAKOS_MEMORY_DIR", "the memory store"},
	{"YAKOS_PLAN_QUALITY_LOG", "the plan-quality log"},
	{"YAKOS_MR_STATE_DIR", "the model-routing state directory"},
	{"YAKOS_MR_EVAL_LOG", "the model-routing evaluation log"},
	{"YAKOS_MR_CANDIDATES", "the model-routing candidates file"},
	{"YAKOS_MR_HISTORY", "the model-routing history file"},
	{"YAKOS_MR_GRAVEYARD", "the model-routing graveyard file"},
	{"YAKOS_MR_BACKUPS_DIR", "the model-routing backups directory"},
	{"YAKOS_COORD_ROOT", "the multi-developer coordination directory"},
	{"YAKOS_WORK_DIR", "the work directory (plan, decisions, kanban and reports)"},
}

func checkStatePathOverrides(e PolicyEnv) []PolicyFinding {
	var out []PolicyFinding
	for _, v := range statePathOverrides {
		if e.Getenv(v.name) == "" {
			continue
		}
		out = append(out, PolicyFinding{
			ID:       "state-path-override:" + v.name,
			Severity: PolicyMedium,
			Message:  fmt.Sprintf("%s is set in the environment: it moves %s away from its default location, and a project's .claude/settings.json env block can set it (K-129)", v.name, v.what),
			Fix:      "unset " + v.name + " unless you set it on purpose",
		})
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---- the doctor mode ----------------------------------------------------------------------

// runPolicy is Run's --policy mode: the report and its summary, nothing else.
// It records the findings on the Report and never counts them as warnings or
// errors, so the exit status stays 0.
func (r *runner) runPolicy() {
	// The home as the caller gave it, not the "/tmp" stand-in Run settles on so its other
	// sections have a path to print. Dispatch resolves its state directory from the real
	// environment and, with no home, falls back to a directory under the temp directory;
	// the policy checks must see "no home" to look where it looks.
	home := r.cfg.HomeDir
	if home == "" {
		home = r.env("HOME")
	}
	findings := CheckPolicy(PolicyEnv{
		Home:                 home,
		Getenv:               r.env,
		LookPath:             r.lookPath,
		BashTreePresent:      r.cfg.PolicyBashTreePresent,
		SDKSidecarSelectable: r.cfg.PolicySDKSidecarSelectable,
		ProbeRuntime:         r.cfg.PolicyProbeRuntime,
	})
	r.report.Policy = findings

	_, _ = fmt.Fprintln(r.w, "Risky configurations")
	if len(findings) == 0 {
		_, _ = fmt.Fprintln(r.w, "  [ok]   no risky configuration found")
	}
	var high, medium, low int
	for _, f := range findings {
		switch f.Severity {
		case PolicyHigh:
			high++
		case PolicyMedium:
			medium++
		default:
			low++
		}
		_, _ = fmt.Fprintf(r.w, "  %-8s %s. Fix: %s\n", "["+string(f.Severity)+"]", strings.TrimRight(f.Message, "."), f.Fix)
	}
	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintf(r.w, "Policy: %d finding(s): %d high, %d medium, %d low (a report: the exit status is 0 whatever it finds)\n",
		len(findings), high, medium, low)
}
