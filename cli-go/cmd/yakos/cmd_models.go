package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/modelreg"
)

// Exit-code contract of `yakos models` (docs/routing.md, "Model registry"):
//
//	0  ok (a skipped or unsupported probe is an answer, not a failure)
//	1  usage error, an unknown model id, or a probe that ran and failed
//
// It never exits 2: exit 2 is the Claude Code hook "block" code.
func printModelsHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos models <list|show|probe> — the provider-aware model registry (K-138)

Subcommands:
    list [--harness <name>] [--project <path>] [--json]
                          One row per model per harness: id, harness, billing,
                          availability and the tier aliases that resolve to it.
                          Availability is yes or no once discovery has listed the
                          harness (agy), unknown otherwise, and disabled for a model
                          switched off. list never runs a harness CLI.
    show <id> [--harness <name>] [--project <path>] [--json]
                          Everything the registry knows about one model: provider,
                          billing and where that was decided, price (api models only),
                          effort levels, limits, aliases and availability.
    probe [--harness <name>] [--timeout <duration>] [--json]
                          List the models a harness reports for the signed-in account
                          (agy only: claude and codex answer "unsupported"), cache the
                          answer in ~/.yakos-state and say what changed. Bounded by
                          --timeout (default 15s); a harness that is not installed or
                          not signed in is skipped, not an error.

Flags:
    --json                Machine-readable output.
    --harness <name>      Only this harness: claude, codex or agy.
    --project <path>      Project whose .yakos.yml models: may disable models
                          (default: the working directory).
    --timeout <duration>  probe: how long one harness may take (default 15s, max 2m).

Sources, in order: the catalog embedded in the binary (lib/settings/model-catalog.json),
the owner-only overlay ~/.yakos-state/model-registry.yml (enable or disable a model, say
how it is billed, price it, map a tier alias, admit discovered ids), and the project's
.yakos.yml models: disable: [id, ...], which can only switch models off. Discovery adds an
availability flag and never adds a model by itself. See docs/routing.md.
`)
}

// modelsEnv is what `yakos models` reads from its surroundings, so the command can
// be tested without a home directory, a project or an installed agy.
type modelsEnv struct {
	// stateDir is where the overlay and the discovery cache live: the trusted
	// state directory, never the one YAKOS_DISPATCH_LOG can move. "" means none.
	stateDir string
	// cwd is the default project.
	cwd func() (string, error)
	// discoverer builds the Discoverer for stateDir with the given per-probe
	// timeout (0 means the default).
	discoverer func(stateDir string, timeout time.Duration) *modelreg.Discoverer
	// ctx is what a probe is bound to, besides the process's own interrupt. Nil
	// means context.Background().
	ctx context.Context
}

func defaultModelsEnv() modelsEnv {
	return modelsEnv{
		stateDir: modelreg.DefaultStateDir(),
		cwd:      os.Getwd,
		discoverer: func(stateDir string, timeout time.Duration) *modelreg.Discoverer {
			return modelreg.NewDiscoverer(modelreg.DiscovererConfig{
				StateDir: stateDir,
				Probe:    modelsSignInProbe,
				Env:      modelsDiscoveryEnv(os.Environ()),
				Timeout:  timeout,
			})
		},
	}
}

// modelsEnvNames and modelsEnvPrefixes are what a read-only model listing may see
// of the environment: how to find the program and its login (PATH, HOME, the
// platform basics), how to reach the network (proxy and certificate variables),
// and agy's own credential families (the ones dispatch lets agy see). It is a
// strict subset of runtime.FilterEnvFor("agy", ...), a test keeps it one: the
// dispatch allowlist also carries GH_TOKEN, GITHUB_TOKEN, SSH_AUTH_SOCK, GIT_*,
// NODE_OPTIONS and YAKOS_* because an agent doing a task needs git, and a listing
// needs none of them, which matters once discovery runs unattended.
var modelsEnvNames = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TERM": true, "LANG": true, "TZ": true, "TMPDIR": true,
	"SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "USERPROFILE": true, "APPDATA": true,
	"LOCALAPPDATA": true, "PROGRAMDATA": true, "PATHEXT": true, "COMSPEC": true, "SYSTEMDRIVE": true,
	"HOMEDRIVE": true, "HOMEPATH": true, "USERNAME": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true,
}

var modelsEnvPrefixes = []string{"LC_", "XDG_", "GEMINI_", "GOOGLE_", "GCLOUD_", "ANTIGRAVITY_"}

// modelsDiscoveryEnv filters environ to the variables a model listing may see, in
// the order given. Names match case-insensitively (Windows spells them Path,
// SystemRoot).
func modelsDiscoveryEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			continue
		}
		k := strings.ToUpper(key)
		keep := modelsEnvNames[k]
		for _, p := range modelsEnvPrefixes {
			keep = keep || strings.HasPrefix(k, p)
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// modelsSignInProbe is the P0a auth probe (auth.ProbeRuntime: CLI on PATH, looks
// signed in) worded the way the dispatcher words it.
func modelsSignInProbe(ctx context.Context, harness string) (bool, string) {
	p := auth.ProbeRuntime(ctx, harness)
	switch {
	case !p.CLIPresent:
		reason := "CLI not found on PATH"
		if p.CLIHint != "" {
			reason += "; " + p.CLIHint
		}
		return false, reason
	case !p.Authed:
		reason := "not signed in"
		if p.AuthHint != "" {
			reason += "; " + p.AuthHint
		}
		if p.Note != "" {
			reason += " (" + p.Note + ")"
		}
		return false, reason
	}
	return true, ""
}

func runModels(args []string) {
	os.Exit(modelsMain(args, os.Stdout, os.Stderr, defaultModelsEnv()))
}

// modelsMain is runModels with its streams and surroundings injected; it returns
// the exit code.
func modelsMain(args []string, stdout, stderr io.Writer, env modelsEnv) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printModelsHelp(stdout)
		if len(args) == 0 {
			return 1
		}
		return 0
	}
	sub, rest := args[0], args[1:]
	var (
		help    bool
		asJSON  bool
		harness string
		project string
		timeout string
	)
	specs := []cliflag.Spec{{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help}}
	switch sub {
	case "list", "show":
		specs = append(specs,
			cliflag.Spec{Name: "--json", Kind: cliflag.Bool, Bool: &asJSON},
			cliflag.Spec{Name: "--harness", Kind: cliflag.String, Str: &harness, ValueDesc: "a harness name"},
			cliflag.Spec{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"})
	case "probe":
		specs = append(specs,
			cliflag.Spec{Name: "--json", Kind: cliflag.Bool, Bool: &asJSON},
			cliflag.Spec{Name: "--harness", Kind: cliflag.String, Str: &harness, ValueDesc: "a harness name"},
			cliflag.Spec{Name: "--timeout", Kind: cliflag.String, Str: &timeout, ValueDesc: "a duration"})
	default:
		_, _ = fmt.Fprintf(stderr, "models: unknown subcommand %q (list | show | probe)\n", sub)
		return 1
	}
	fs := &cliflag.Set{Cmd: "models " + sub, Specs: specs}
	pos, err := fs.Parse(rest)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if help {
		printModelsHelp(stdout)
		return 0
	}
	for _, p := range pos {
		if strings.HasPrefix(p, "-") {
			_, _ = fmt.Fprintf(stderr, "models %s: unknown flag %q\n", sub, p)
			return 1
		}
	}
	if harness != "" && !modelreg.IsHarness(harness) {
		_, _ = fmt.Fprintf(stderr, "models %s: unknown harness %q (%s)\n", sub, harness, strings.Join(modelreg.Harnesses, " | "))
		return 1
	}
	switch sub {
	case "list":
		if len(pos) != 0 {
			_, _ = fmt.Fprintln(stderr, "models list: unexpected argument")
			return 1
		}
		return modelsList(stdout, stderr, env, harness, project, asJSON)
	case "show":
		if len(pos) != 1 {
			_, _ = fmt.Fprintln(stderr, "usage: yakos models show <id> [--harness <name>] [--project <path>] [--json]")
			return 1
		}
		return modelsShow(stdout, stderr, env, pos[0], harness, project, asJSON)
	default: // probe
		if len(pos) != 0 {
			_, _ = fmt.Fprintln(stderr, "models probe: unexpected argument")
			return 1
		}
		d, err := parseProbeTimeout(timeout)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "models probe: %v\n", err)
			return 1
		}
		return modelsProbe(stdout, stderr, env, harness, d, asJSON)
	}
}

const (
	defaultProbeTimeout = 15 * time.Second
	maxProbeTimeout     = 2 * time.Minute
	// modelsIdleWait is how long probe waits, before it returns, for a run it
	// stopped to finish dying.
	modelsIdleWait = 5 * time.Second
)

func parseProbeTimeout(s string) (time.Duration, error) {
	if s == "" {
		return defaultProbeTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > maxProbeTimeout {
		return 0, fmt.Errorf("--timeout %q: want a duration between 1ms and %s, such as 20s", s, maxProbeTimeout)
	}
	return d, nil
}

// loadModelRegistry builds the registry for the command: the embedded catalog, the
// trusted overlay, the project's disables and whatever discovery has cached. The
// warnings go to stderr, once each, in the registry's fixed order.
func loadModelRegistry(stderr io.Writer, env modelsEnv, project string, d *modelreg.Discoverer) (*modelreg.Registry, error) {
	if project == "" {
		if wd, err := env.cwd(); err == nil {
			project = wd
		}
	}
	reg, err := modelreg.Load(modelreg.Options{StateDir: env.stateDir, Project: project, Snapshots: d})
	if err != nil {
		return nil, err
	}
	for _, w := range reg.Warnings() {
		_, _ = fmt.Fprintf(stderr, "yakos models: %s\n", w)
	}
	return reg, nil
}

func modelsList(stdout, stderr io.Writer, env modelsEnv, harness, project string, asJSON bool) int {
	reg, err := loadModelRegistry(stderr, env, project, env.discoverer(env.stateDir, 0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "yakos models: %v\n", err)
		return 1
	}
	var entries []modelreg.Entry
	for _, e := range reg.Entries() {
		if harness == "" || e.Harness == harness {
			entries = append(entries, e)
		}
	}
	if asJSON {
		return writeJSON(stdout, stderr, struct {
			Models   []modelreg.Entry `json:"models"`
			Warnings []string         `json:"warnings,omitempty"`
		}{entries, reg.Warnings()})
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tHARNESS\tBILLING\tAVAILABLE\tALIASES")
	agyUnknown := false
	for _, e := range entries {
		aliases := "-"
		if len(e.Aliases) > 0 {
			aliases = strings.Join(e.Aliases, ",")
		}
		if e.Harness == "agy" && e.Availability.State == modelreg.AvailUnknown {
			agyUnknown = true
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.Harness, e.Billing, availabilityCell(e), aliases)
	}
	_ = tw.Flush()
	if agyUnknown {
		_, _ = fmt.Fprintln(stderr, "yakos models: no discovery result for agy yet; run `yakos models probe` to see which agy models this account can use")
	}
	return 0
}

// availabilityCell is the AVAILABLE column: a disabled model says so, whatever
// discovery saw; otherwise the discovery state, with a stale listing marked.
func availabilityCell(e modelreg.Entry) string {
	if !e.Enabled {
		return "disabled"
	}
	if e.Availability.Stale && e.Availability.State != modelreg.AvailUnknown {
		return string(e.Availability.State) + " (stale)"
	}
	return string(e.Availability.State)
}

func modelsShow(stdout, stderr io.Writer, env modelsEnv, id, harness, project string, asJSON bool) int {
	reg, err := loadModelRegistry(stderr, env, project, env.discoverer(env.stateDir, 0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "yakos models: %v\n", err)
		return 1
	}
	var entries []modelreg.Entry
	for _, e := range reg.Find(id) {
		if harness == "" || e.Harness == harness {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintf(stderr, "models show: no model %q in the registry (see `yakos models list`)\n", id)
		return 1
	}
	if asJSON {
		return writeJSON(stdout, stderr, struct {
			Models   []modelreg.Entry `json:"models"`
			Warnings []string         `json:"warnings,omitempty"`
		}{entries, reg.Warnings()})
	}
	for i, e := range entries {
		if i > 0 {
			_, _ = fmt.Fprintln(stdout)
		}
		printEntry(stdout, e, reg)
	}
	return 0
}

func printEntry(w io.Writer, e modelreg.Entry, reg *modelreg.Registry) {
	row := func(k, v string) { _, _ = fmt.Fprintf(w, "  %-13s %s\n", k+":", v) }
	_, _ = fmt.Fprintf(w, "%s (%s)\n", e.ID, e.Harness)
	row("name", e.Name)
	row("provider", e.Provider)
	if e.Family != "" {
		row("family", e.Family)
	}
	if e.Description != "" {
		row("description", e.Description)
	}
	row("billing", fmt.Sprintf("%s (%s)", e.Billing, e.BillingBy))
	row("price", priceText(e))
	row("enabled", enabledText(e))
	row("available", availabilityText(e))
	row("aliases", aliasesText(e, reg))
	if len(e.EffortLevels) > 0 {
		v := strings.Join(e.EffortLevels, ", ")
		switch {
		case e.EffortInID:
			v += " (carried by the id; do not pass --effort with it)"
		case e.DefaultEffort != "":
			v += " (default " + e.DefaultEffort + ")"
		}
		row("effort", v)
	}
	if e.Limit != nil {
		var parts []string
		if e.Limit.Context > 0 {
			parts = append(parts, fmt.Sprintf("context %d tokens", e.Limit.Context))
		}
		if e.Limit.ContextMax > 0 && e.Limit.ContextMax != e.Limit.Context {
			parts = append(parts, fmt.Sprintf("extended %d", e.Limit.ContextMax))
		}
		if e.Limit.Output > 0 {
			parts = append(parts, fmt.Sprintf("output %d", e.Limit.Output))
		}
		if len(parts) > 0 {
			row("limits", strings.Join(parts, ", "))
		}
	}
	if len(e.Modalities) > 0 {
		row("input", strings.Join(e.Modalities, ", "))
	}
	if e.OpenWeights {
		row("open weights", "yes")
	}
	if e.Visibility != "" {
		row("visibility", e.Visibility+" (the harness's own picker)")
	}
	row("source", e.Source)
}

func priceText(e modelreg.Entry) string {
	if e.Cost == nil {
		if e.Billing == modelreg.BillingAPI {
			return "none recorded (billed per call: set one in model-registry.yml; tokens are counted meanwhile)"
		}
		return "none (billing is " + string(e.Billing) + ": tokens are the unit)"
	}
	s := fmt.Sprintf("$%s in / $%s out per million tokens", num(e.Cost.Input), num(e.Cost.Output))
	if e.Cost.CacheRead > 0 || e.Cost.CacheWrite > 0 {
		s += fmt.Sprintf(" (cache read $%s, write $%s)", num(e.Cost.CacheRead), num(e.Cost.CacheWrite))
	}
	return s + " (" + e.CostBy + ")"
}

// num renders a price without trailing zeros.
func num(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func enabledText(e modelreg.Entry) string {
	v := "yes"
	if !e.Enabled {
		v = "no"
	}
	switch e.EnabledBy {
	case modelreg.FromCatalog:
		return v + " (catalog default)"
	case modelreg.FromOverlay:
		return v + " (model-registry.yml)"
	case modelreg.FromProject:
		return v + " (this project's .yakos.yml disables it)"
	}
	return v
}

func availabilityText(e modelreg.Entry) string {
	a := e.Availability
	switch a.State {
	case modelreg.AvailYes, modelreg.AvailNo:
		verb := "listed by"
		if a.State == modelreg.AvailNo {
			verb = "not in the listing of"
		}
		s := fmt.Sprintf("%s (%s %s at %s", a.State, verb, a.Source, a.At.UTC().Format(time.RFC3339))
		if a.Stale {
			s += ", stale: run `yakos models probe`"
		}
		return s + ")"
	}
	if e.Harness == "agy" {
		return "unknown (no listing taken yet: run `yakos models probe`)"
	}
	return "unknown (no model listing is wired in for " + e.Harness + ")"
}

func aliasesText(e modelreg.Entry, reg *modelreg.Registry) string {
	if len(e.Aliases) > 0 {
		parts := make([]string, len(e.Aliases))
		for i, a := range e.Aliases {
			parts[i] = a
			if reg.AliasSource(e.Harness, a) == modelreg.FromOverlay {
				parts[i] += " (model-registry.yml)"
			}
		}
		return strings.Join(parts, ", ")
	}
	if e.Harness == "codex" {
		return "none (tier aliases are empty for codex: the catalog gives no tiers; map them in model-registry.yml)"
	}
	return "none"
}

// ---- probe -------------------------------------------------------------------------

type probeOutput struct {
	Probes []modelreg.ProbeReport `json:"probes"`
	// NotInCatalog lists, per harness, the listed ids the catalog does not have.
	NotInCatalog map[string][]string `json:"not_in_catalog,omitempty"`
	Warnings     []string            `json:"warnings,omitempty"`
}

func modelsProbe(stdout, stderr io.Writer, env modelsEnv, harness string, timeout time.Duration, asJSON bool) int {
	// agy runs in a session of its own (no controlling terminal, so it can never
	// prompt), which means a Ctrl-C at the terminal no longer reaches it. The probe
	// is bound to the interrupt instead: the context ends, the run is killed with its
	// whole process group, and the command waits for that before it returns.
	base := env.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := env.discoverer(env.stateDir, timeout)
	defer func() {
		idle, cancel := context.WithTimeout(context.Background(), modelsIdleWait)
		defer cancel()
		_ = d.WaitIdle(idle)
	}()
	targets := modelreg.Harnesses
	if harness != "" {
		targets = []string{harness}
	}
	// The catalog alone, to say which listed ids it lacks: the overlay's admit
	// switch must not hide them.
	catalog, err := modelreg.Load(modelreg.Options{})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "yakos models: %v\n", err)
		return 1
	}
	// Which harnesses the overlay admits discovered ids for: what is said about an id
	// the catalog lacks depends on it. Warnings about the overlay are not repeated
	// here (list prints them).
	ov, _ := modelreg.LoadOverlay(env.stateDir)
	admitted := map[string]bool{}
	for _, h := range ov.Admit {
		admitted[h] = true
	}
	out := probeOutput{NotInCatalog: map[string][]string{}}
	failed := false
	for _, h := range targets {
		rep, err := d.Probe(ctx, h)
		out.Probes = append(out.Probes, rep)
		out.Warnings = append(out.Warnings, rep.Warnings...)
		if err != nil {
			failed = true
		}
		if rep.Status == modelreg.ProbeUpdated {
			if missing := notInCatalog(catalog, h, rep.Snapshot); len(missing) > 0 {
				out.NotInCatalog[h] = missing
			}
		}
	}
	if len(out.NotInCatalog) == 0 {
		out.NotInCatalog = nil
	}
	if asJSON {
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		printProbes(stdout, out, catalog, admitted)
	}
	if failed {
		for _, rep := range out.Probes {
			if rep.Status == modelreg.ProbeFailed {
				_, _ = fmt.Fprintf(stderr, "yakos models probe: %s failed: %s\n", rep.Harness, rep.Reason)
			}
		}
		return 1
	}
	return 0
}

// notInCatalog returns the ids the snapshot lists for harness that the catalog
// has no entry for, sorted.
func notInCatalog(cat *modelreg.Registry, harness string, snap modelreg.Snapshot) []string {
	var missing []string
	for _, id := range snap.IDs() {
		if _, ok := cat.Lookup(harness, id); !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

func printProbes(w io.Writer, out probeOutput, cat *modelreg.Registry, admitted map[string]bool) {
	for _, rep := range out.Probes {
		switch rep.Status {
		case modelreg.ProbeUpdated:
			_, _ = fmt.Fprintf(w, "%s: updated, %d models listed by `%s`\n", rep.Harness, len(rep.Snapshot.Models), rep.Snapshot.Source)
			switch {
			case rep.Previous == nil:
				_, _ = fmt.Fprintln(w, "  first listing: nothing to compare with")
			case len(rep.Added) == 0 && len(rep.Removed) == 0:
				_, _ = fmt.Fprintln(w, "  no change since the last listing")
			}
			if len(rep.Added) > 0 {
				_, _ = fmt.Fprintf(w, "  new:     %s\n", strings.Join(rep.Added, ", "))
			}
			if len(rep.Removed) > 0 {
				_, _ = fmt.Fprintf(w, "  gone:    %s\n", strings.Join(rep.Removed, ", "))
			}
			if missing := out.NotInCatalog[rep.Harness]; len(missing) > 0 {
				_, _ = fmt.Fprintf(w, "  not in the catalog: %s\n", strings.Join(missing, ", "))
				if admitted[rep.Harness] {
					_, _ = fmt.Fprintf(w, "    model-registry.yml admits them: they are registered as discovered entries (a name that is a tier alias or a Claude tier is skipped)\n")
				} else {
					_, _ = fmt.Fprintf(w, "    they stay unregistered unless model-registry.yml says `discovery: {admit: [%s]}`\n", rep.Harness)
				}
			}
			if unlisted := catalogUnlisted(cat, rep.Harness, rep.Snapshot); len(unlisted) > 0 {
				_, _ = fmt.Fprintf(w, "  catalog entries this account does not list: %s\n", strings.Join(unlisted, ", "))
			}
			if rep.Dropped > 0 {
				_, _ = fmt.Fprintf(w, "  %d line(s) of the listing were not model ids and were dropped\n", rep.Dropped)
			}
		case modelreg.ProbeSkipped:
			_, _ = fmt.Fprintf(w, "%s: skipped, %s\n", rep.Harness, rep.Reason)
		case modelreg.ProbeUnsupported:
			_, _ = fmt.Fprintf(w, "%s: %s\n", rep.Harness, rep.Reason)
		case modelreg.ProbeFailed:
			_, _ = fmt.Fprintf(w, "%s: failed, %s\n", rep.Harness, rep.Reason)
		}
		for _, warn := range rep.Warnings {
			_, _ = fmt.Fprintf(w, "  note: %s\n", warn)
		}
	}
}

// catalogUnlisted returns the catalog's entries on harness that the snapshot does
// not list, in catalog order.
func catalogUnlisted(cat *modelreg.Registry, harness string, snap modelreg.Snapshot) []string {
	var out []string
	for _, e := range cat.Entries() {
		if e.Harness == harness && e.Source == modelreg.FromCatalog && !snap.Has(e.ID) {
			out = append(out, e.ID)
		}
	}
	return out
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "yakos models: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s\n", b)
	return 0
}
