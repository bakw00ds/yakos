package main

// cmd_models_write.go: the writers behind `yakos models enable|disable|alias|
// pin|pricing` and `yakos router policy get|set` (K-153).
//
// Every write goes through the one trusted writer of its file (modelreg's overlay
// functions, routerpolicy.Edit; both over statepath.EditYAML: trust-checked read,
// owner-only 0600 atomic rename) and appends a config_changed line to the
// dispatch log through dispatch.Account (operator, file, sha before and after).
// None of them prints a path.

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// maxPolicyInput bounds a rules file read for `router policy set`.
const maxPolicyInput = 256 << 10

// cliOperatorID names the actor of a CLI write in the audit line: the OS user.
func cliOperatorID() string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return "cli"
	}
	name := u.Username
	if i := strings.LastIndexByte(name, '\\'); i >= 0 { // DOMAIN\user on Windows
		name = name[i+1:]
	}
	return name
}

// auditWrite records a policy write. The write has already happened; an audit
// failure is reported (exit 1) so the operator knows it went unrecorded.
func auditWrite(stderr io.Writer, file, action string, res statepath.EditResult) bool {
	err := dispatch.NewAccount(dispatch.Request{OperatorID: cliOperatorID(), Surface: dispatch.SurfaceCLI}).ConfigChanged(dispatch.ConfigChange{
		File: file, Action: action, SHABefore: res.SHABefore, SHAAfter: res.SHAAfter, Surface: dispatch.SurfaceCLI,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "yakos: the change was written but could not be recorded in the dispatch log")
		return false
	}
	return true
}

// reportWrite prints the one-line result of a write and audits it.
func reportWrite(stdout, stderr io.Writer, file, action, what string, res statepath.EditResult) int {
	if !res.Changed {
		_, _ = fmt.Fprintf(stdout, "unchanged: %s (%s)\n", what, file)
		return 0
	}
	if !auditWrite(stderr, file, action, res) {
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "ok: %s (%s %s)\n", what, file, res)
	return 0
}

// modelsWriteArgs are the flags of the write subcommands.
type modelsWriteArgs struct {
	input, output, cacheRead, cacheWrite, billing, runtime string
	clear                                                  bool
}

func parsePrice(name, s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%s %q: want a number of dollars per million tokens", name, s)
	}
	return f, nil
}

func modelsWrite(stdout, stderr io.Writer, env modelsEnv, sub string, pos []string, a modelsWriteArgs, project string) int {
	fail := func(format string, args ...any) int {
		_, _ = fmt.Fprintf(stderr, "models %s: %s\n", sub, fmt.Sprintf(format, args...))
		return 1
	}
	if env.stateDir == "" {
		return fail("no home directory, so no overlay to write")
	}
	reg, err := loadModelRegistry(stderr, env, project, env.discoverer(env.stateDir, 0))
	if err != nil {
		return fail("the model registry could not be loaded")
	}
	known := func(id string) ([]modelreg.Entry, bool) {
		es := reg.Find(id)
		return es, len(es) > 0
	}
	const overlay = modelreg.OverlayFileName
	switch sub {
	case "enable", "disable":
		if len(pos) != 1 {
			return fail("usage: yakos models %s <id>", sub)
		}
		es, ok := known(pos[0])
		if !ok {
			return fail("unknown model id (try: yakos models list)")
		}
		res, err := modelreg.SetEnabled(env.stateDir, pos[0], sub == "enable")
		if err != nil {
			return fail("%v", err)
		}
		if sub == "enable" {
			for _, e := range es {
				if e.EnabledBy == "project" {
					_, _ = fmt.Fprintf(stderr, "models enable: note: a project's .yakos.yml disables %s on %s; a project can only switch models off\n", e.ID, e.Harness)
					break
				}
			}
		}
		return reportWrite(stdout, stderr, overlay, "models."+sub, sub+"d "+pos[0], res)
	case "alias":
		if len(pos) != 3 {
			return fail("usage: yakos models alias <%s> <codex|agy> <id|default>", strings.Join(modelreg.AliasNames, "|"))
		}
		alias, harness, id := pos[0], pos[1], pos[2]
		if id == "default" {
			id = ""
		}
		if id != "" {
			if _, ok := reg.Lookup(harness, id); !ok {
				return fail("%s has no model %q (try: yakos models list --harness %s)", harness, id, harness)
			}
		}
		res, err := modelreg.SetAlias(env.stateDir, alias, harness, id)
		if err != nil {
			return fail("%v", err)
		}
		return reportWrite(stdout, stderr, overlay, "models.alias", fmt.Sprintf("%s on %s is now %s", alias, harness, orDefault(id)), res)
	case "pricing":
		return modelsPricing(stdout, stderr, env, reg, pos, a, fail)
	default: // pin
		return modelsPin(stdout, stderr, env, reg, pos, a, fail)
	}
}

func orDefault(id string) string {
	if id == "" {
		return "the harness default"
	}
	return id
}

func modelsPricing(stdout, stderr io.Writer, env modelsEnv, reg *modelreg.Registry, pos []string, a modelsWriteArgs, fail func(string, ...any) int) int {
	if len(pos) != 1 {
		return fail("usage: yakos models pricing <id> --input <usd> --output <usd> [--cache-read <usd>] [--cache-write <usd>] [--billing api|subscription|local] | --clear")
	}
	id := pos[0]
	es := reg.Find(id)
	if len(es) == 0 {
		return fail("unknown model id (try: yakos models list)")
	}
	const overlay = modelreg.OverlayFileName
	if !a.clear && a.billing == "" && (a.input != "" || a.output != "") {
		// A price on a model that is not billed per call is ignored by the registry
		// (and warned about on every load), so refuse it here.
		for _, e := range es {
			if e.Billing != modelreg.BillingAPI {
				return fail("%s on %s is billed %s; a price counts only for api billing (add --billing api)", id, e.Harness, e.Billing)
			}
		}
	}
	if a.billing != "" {
		if !modelreg.Billing(a.billing).Valid() {
			return fail("--billing %q: want subscription, api or local", a.billing)
		}
		res, err := modelreg.SetBilling(env.stateDir, id, modelreg.Billing(a.billing))
		if err != nil {
			return fail("%v", err)
		}
		if rc := reportWrite(stdout, stderr, overlay, "models.billing", id+" is billed "+a.billing, res); rc != 0 {
			return rc
		}
	}
	if a.clear {
		res, err := modelreg.SetPricing(env.stateDir, id, nil)
		if err != nil {
			return fail("%v", err)
		}
		return reportWrite(stdout, stderr, overlay, "models.pricing", "cleared the price of "+id, res)
	}
	if a.input == "" && a.output == "" && a.billing != "" {
		return 0 // only the billing mode was asked for
	}
	if a.input == "" || a.output == "" {
		return fail("--input and --output are required (dollars per million tokens)")
	}
	var p modelreg.Pricing
	for _, f := range []struct {
		name, val string
		dst       *float64
		need      bool
	}{{"--input", a.input, &p.Input, true}, {"--output", a.output, &p.Output, true}, {"--cache-read", a.cacheRead, &p.CacheRead, false}, {"--cache-write", a.cacheWrite, &p.CacheWrite, false}} {
		if f.val == "" {
			continue
		}
		v, err := parsePrice(f.name, f.val)
		if err != nil {
			return fail("%v", err)
		}
		*f.dst = v
	}
	if err := p.Validate(); err != nil {
		return fail("%v", err)
	}
	res, err := modelreg.SetPricing(env.stateDir, id, &p)
	if err != nil {
		return fail("%v", err)
	}
	return reportWrite(stdout, stderr, overlay, "models.pricing", "priced "+id, res)
}

func modelsPin(stdout, stderr io.Writer, env modelsEnv, reg *modelreg.Registry, pos []string, a modelsWriteArgs, fail func(string, ...any) int) int {
	const file = routerpolicy.FileName
	if len(pos) < 1 || len(pos) > 2 || (a.clear && len(pos) != 1) || (!a.clear && len(pos) != 2) {
		return fail("usage: yakos models pin <agent> <id> [--runtime <name>] | yakos models pin <agent> --clear")
	}
	agent := pos[0]
	if !explainAgentRe.MatchString(agent) {
		return fail("invalid agent name")
	}
	if a.clear {
		res, err := routerpolicy.ClearPin(env.stateDir, agent, nil)
		if err != nil {
			return fail("%v", err)
		}
		return reportWrite(stdout, stderr, file, "models.unpin", "unpinned "+agent, res)
	}
	var matches []modelreg.Entry
	for _, e := range reg.Find(pos[1]) {
		if a.runtime == "" || e.Harness == a.runtime {
			matches = append(matches, e)
		}
	}
	switch {
	case len(matches) == 0:
		return fail("unknown model id for that runtime (try: yakos models list)")
	case len(matches) > 1:
		var hs []string
		for _, e := range matches {
			hs = append(hs, e.Harness)
		}
		return fail("%s is offered by more than one runtime (%s); name one with --runtime", pos[1], strings.Join(hs, ", "))
	case !matches[0].Enabled:
		return fail("%s is disabled (yakos models enable %s first)", pos[1], pos[1])
	}
	res, err := routerpolicy.SetPin(env.stateDir, routerpolicy.Pin{Agent: agent, Runtime: matches[0].Harness, Model: pos[1]}, router.CheckPolicy)
	if err != nil {
		return fail("%v", err)
	}
	return reportWrite(stdout, stderr, file, "models.pin", fmt.Sprintf("pinned %s to %s/%s", agent, matches[0].Harness, pos[1]), res)
}

// routerPolicy runs `yakos router policy get|set`.
func routerPolicy(stdout, stderr io.Writer, env explainEnv, args []string) int {
	var (
		help, asJSON bool
		rulesFile    string
	)
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	fs := policyFlagSet(&help, &asJSON, &rulesFile)
	pos, err := fs.Parse(args[min(1, len(args)):])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return explainExitUsage
	}
	if help || sub == "" || sub == "--help" || sub == "-h" {
		printRouterHelp(stdout)
		return 0
	}
	state := env.stateDir()
	switch {
	case sub == "get" && len(pos) == 0:
		return routerPolicyGet(stdout, stderr, state, asJSON)
	case sub == "set" && len(pos) == 0 && rulesFile != "":
		return routerPolicySet(stdout, stderr, state, rulesFile)
	}
	_, _ = fmt.Fprintln(stderr, "usage: yakos router policy get [--json] | yakos router policy set --rules-file <file|->")
	return explainExitUsage
}

func routerPolicyGet(stdout, stderr io.Writer, state string, asJSON bool) int {
	v := router.ViewOf(state)
	if asJSON {
		return writeJSON(stdout, stderr, v)
	}
	sha := v.SHA
	if sha == "" {
		sha = "(no trusted policy file)"
	}
	_, _ = fmt.Fprintf(stdout, "policy_sha: %s\n", sha)
	_, _ = fmt.Fprintf(stdout, "allow_unsandboxed_runtimes: [%s]\n", strings.Join(v.AllowUnsandboxedRuntimes, " "))
	_, _ = fmt.Fprintf(stdout, "hooks_endpoint: %t\nopenai_endpoint: %t\n", v.HooksEndpoint, v.OpenAIEndpoint)
	for _, r := range v.Rules {
		_, _ = fmt.Fprintf(stdout, "%s: %s\n", r.ID, sanitizeForTerminal(ruleLine(r)))
	}
	for _, p := range v.Pins {
		_, _ = fmt.Fprintf(stdout, "pin: %s -> %s/%s\n", sanitizeForTerminal(p.Agent), sanitizeForTerminal(p.Runtime), sanitizeForTerminal(p.Model))
	}
	for _, w := range v.Warnings {
		_, _ = fmt.Fprintf(stderr, "router policy: %s\n", sanitizeForTerminal(w))
	}
	return 0
}

func routerPolicySet(stdout, stderr io.Writer, state, rulesFile string) int {
	if state == "" {
		_, _ = fmt.Fprintln(stderr, "router policy set: no home directory, so no policy to write")
		return explainExitFail
	}
	data, err := readBounded(rulesFile, maxPolicyInput)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "router policy set: --rules-file: %v\n", err)
		return explainExitUsage
	}
	res, err := routerpolicy.SetRules(state, data, router.CheckPolicy)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "router policy set: %s\n", sanitizeForTerminal(err.Error()))
		return explainExitFail
	}
	if rc := reportWrite(stdout, stderr, routerpolicy.FileName, "router.policy.set", "router rules", res); rc != 0 {
		return explainExitFail
	}
	return 0
}

// readBounded reads at most max bytes of a regular file, or of stdin for "-". A
// FIFO or device named by mistake is refused, not read.
func readBounded(path string, max int64) ([]byte, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("cannot stat the file")
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("not a regular file")
		}
		f, err := os.Open(path) //nolint:gosec // operator-named file, regular, bounded below
		if err != nil {
			return nil, fmt.Errorf("cannot open the file")
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read the file")
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("larger than %d bytes", max)
	}
	return data, nil
}

func policyFlagSet(help, asJSON *bool, rulesFile *string) *cliflag.Set {
	return &cliflag.Set{Cmd: "router policy", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: help},
		{Name: "--json", Kind: cliflag.Bool, Bool: asJSON},
		{Name: "--rules-file", Kind: cliflag.String, Str: rulesFile, ValueDesc: "a path or -"},
	}}
}

// ruleLine renders a rule on one line: what it matches, then what it does.
func ruleLine(r router.Rule) string {
	var m, a []string
	add := func(dst *[]string, k, v string) {
		if v != "" {
			*dst = append(*dst, k+"="+v)
		}
	}
	add(&m, "class", r.Match.Class)
	add(&m, "agent", r.Match.Agent)
	add(&m, "domain", r.Match.Domain)
	if r.Match.TaskBytesGT > 0 {
		add(&m, "task_bytes_gt", strconv.FormatInt(r.Match.TaskBytesGT, 10))
	}
	add(&m, "tags", strings.Join(r.Match.Tags, ","))
	add(&a, "runtime", r.Action.Runtime)
	add(&a, "model", r.Action.Model)
	add(&a, "fallbacks", strings.Join(r.Action.Fallbacks, ","))
	line := "when " + orAny(strings.Join(m, " ")) + " -> " + strings.Join(a, " ")
	if r.OverridePins {
		line += " (overrides pins)"
	}
	return line
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}
