// Package policywrite is the one place that decides and performs a model-registry
// or router-policy write. The CLI (`yakos models enable|disable|alias|pin|pricing`,
// `yakos router policy set`) and the console's browser writes (K-175) both call
// it, so there is no second write path: the same checks run, the same trusted
// writers (modelreg's overlay functions, routerpolicy.SetPin/SetRules; both over
// statepath.EditYAML) replace the files, and the caller records every change
// through the Recorder it passes in.
//
// A Recorder is called right after each file replacement (changed or not), before
// the next write, so a multi-step edit never leaves a written file unrecorded. A
// Recorder error stops the edit and is returned as is.
package policywrite

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// Change is one file replacement, ready for the audit line.
type Change struct {
	// File is the base name of the file; Action is the fixed audit verb.
	File, Action string
	// What is the human description the CLI prints ("enabled gpt-5").
	What string
	// Note is an advisory the CLI prints on stderr; empty when there is none.
	Note string
	Res  statepath.EditResult
}

// Recorder receives each Change right after it is written.
type Recorder func(Change) error

// Code classifies a refusal with a fixed, input-free meaning.
type Code string

const (
	CodeUnknownModel Code = "unknown_model"
	CodeInvalid      Code = "invalid"
	CodeDisabled     Code = "model_disabled"
	CodeAmbiguous    Code = "ambiguous_runtime"
	CodeBilling      Code = "billing_mismatch"
)

// Error is a refusal the caller can show. Error() is the CLI text and may echo
// the request's own values; Code is the fixed meaning a network surface reports
// instead.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func refuse(c Code, format string, args ...any) error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...)}
}

// AgentRe is the shape of an agent name taken from a request.
var AgentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// SetEnabled switches a model on or off.
func SetEnabled(stateDir string, reg *modelreg.Registry, id string, enable bool, rec Recorder) error {
	es := reg.Find(id)
	if len(es) == 0 {
		return refuse(CodeUnknownModel, "unknown model id (try: yakos models list)")
	}
	res, err := modelreg.SetEnabled(stateDir, id, enable)
	if err != nil {
		return err
	}
	verb := "disable"
	if enable {
		verb = "enable"
	}
	c := Change{File: modelreg.OverlayFileName, Action: "models." + verb, What: verb + "d " + id, Res: res}
	if enable {
		for _, e := range es {
			if e.EnabledBy == "project" {
				c.Note = fmt.Sprintf("a project's .yakos.yml disables %s on %s; a project can only switch models off", e.ID, e.Harness)
				break
			}
		}
	}
	return rec(c)
}

// SetAlias maps a tier alias to a model on a harness; id "" is the harness default.
func SetAlias(stateDir string, reg *modelreg.Registry, alias, harness, id string, rec Recorder) error {
	if id != "" {
		if _, ok := reg.Lookup(harness, id); !ok {
			return refuse(CodeUnknownModel, "%s has no model %q (try: yakos models list --harness %s)", harness, id, harness)
		}
	}
	res, err := modelreg.SetAlias(stateDir, alias, harness, id)
	if err != nil {
		return err
	}
	return rec(Change{File: modelreg.OverlayFileName, Action: "models.alias", Res: res,
		What: fmt.Sprintf("%s on %s is now %s", alias, harness, orDefault(id))})
}

func orDefault(id string) string {
	if id == "" {
		return "the harness default"
	}
	return id
}

// PricingArgs are the values of `models pricing`. The numbers are strings so the
// CLI's parse errors are unchanged; a network caller formats its numbers.
type PricingArgs struct {
	Input, Output, CacheRead, CacheWrite string
	Billing                              string
	Clear                                bool
}

// ParsePrice parses one price flag.
func ParsePrice(name, s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, refuse(CodeInvalid, "%s %q: want a number of dollars per million tokens", name, s)
	}
	return f, nil
}

// SetPricing sets or clears a model's price and billing mode.
func SetPricing(stateDir string, reg *modelreg.Registry, id string, a PricingArgs, rec Recorder) error {
	es := reg.Find(id)
	if len(es) == 0 {
		return refuse(CodeUnknownModel, "unknown model id (try: yakos models list)")
	}
	const overlay = modelreg.OverlayFileName
	if !a.Clear && a.Billing == "" && (a.Input != "" || a.Output != "") {
		// A price on a model that is not billed per call is ignored by the registry
		// (and warned about on every load), so refuse it here.
		for _, e := range es {
			if e.Billing != modelreg.BillingAPI {
				return refuse(CodeBilling, "%s on %s is billed %s; a price counts only for api billing (add --billing api)", id, e.Harness, e.Billing)
			}
		}
	}
	if a.Billing != "" && !modelreg.Billing(a.Billing).Valid() {
		return refuse(CodeInvalid, "--billing %q: want subscription, api or local", a.Billing)
	}
	// Everything is parsed and validated before the first write, so a bad price
	// leaves the overlay (billing included) as it was.
	var price *modelreg.Pricing
	if !a.Clear && (a.Input != "" || a.Output != "" || a.Billing == "") {
		if a.Input == "" || a.Output == "" {
			return refuse(CodeInvalid, "--input and --output are required (dollars per million tokens)")
		}
		var p modelreg.Pricing
		for _, f := range []struct {
			name, val string
			dst       *float64
		}{{"--input", a.Input, &p.Input}, {"--output", a.Output, &p.Output}, {"--cache-read", a.CacheRead, &p.CacheRead}, {"--cache-write", a.CacheWrite, &p.CacheWrite}} {
			if f.val == "" {
				continue
			}
			v, err := ParsePrice(f.name, f.val)
			if err != nil {
				return err
			}
			*f.dst = v
		}
		if err := p.Validate(); err != nil {
			return refuse(CodeInvalid, "%v", err)
		}
		price = &p
	}
	// One atomic write for billing and price together: a failure cannot leave
	// the billing mode changed and the price not, and one audit line covers it.
	if price == nil && !a.Clear {
		if a.Billing == "" {
			return nil
		}
		res, err := modelreg.SetBillingAndPricing(stateDir, id, modelreg.Billing(a.Billing), false, nil)
		if err != nil {
			return err
		}
		return rec(Change{File: overlay, Action: "models.billing", What: id + " is billed " + a.Billing, Res: res})
	}
	res, err := modelreg.SetBillingAndPricing(stateDir, id, modelreg.Billing(a.Billing), true, price)
	if err != nil {
		return err
	}
	what := "priced " + id
	if a.Clear {
		what = "cleared the price of " + id
	}
	if a.Billing != "" {
		what += ", billed " + a.Billing
	}
	return rec(Change{File: overlay, Action: "models.pricing", What: what, Res: res})
}

// SetPin pins an agent to a model (runtime narrows an id several runtimes offer),
// or clears the pin.
func SetPin(stateDir string, reg *modelreg.Registry, agent, id, runtime string, clear bool, rec Recorder) error {
	const file = routerpolicy.FileName
	if !AgentRe.MatchString(agent) {
		return refuse(CodeInvalid, "invalid agent name")
	}
	if clear {
		res, err := routerpolicy.ClearPin(stateDir, agent, nil)
		if err != nil {
			return err
		}
		return rec(Change{File: file, Action: "models.unpin", What: "unpinned " + agent, Res: res})
	}
	var matches []modelreg.Entry
	for _, e := range reg.Find(id) {
		if runtime == "" || e.Harness == runtime {
			matches = append(matches, e)
		}
	}
	switch {
	case len(matches) == 0:
		return refuse(CodeUnknownModel, "unknown model id for that runtime (try: yakos models list)")
	case len(matches) > 1:
		var hs []string
		for _, e := range matches {
			hs = append(hs, e.Harness)
		}
		return refuse(CodeAmbiguous, "%s is offered by more than one runtime (%s); name one with --runtime", id, strings.Join(hs, ", "))
	case !matches[0].Enabled:
		return refuse(CodeDisabled, "%s is disabled (yakos models enable %s first)", id, id)
	}
	res, err := routerpolicy.SetPin(stateDir, routerpolicy.Pin{Agent: agent, Runtime: matches[0].Harness, Model: id}, router.CheckPolicy)
	if err != nil {
		return err
	}
	return rec(Change{File: file, Action: "models.pin", Res: res,
		What: fmt.Sprintf("pinned %s to %s/%s", agent, matches[0].Harness, id)})
}

// SetRules replaces the router policy's rules with the YAML list in rulesYAML.
// A non-nil base makes it a compare-and-swap: the policy file's sha, read under
// the edit lock, must equal *base or the error is a *statepath.StaleError.
func SetRules(stateDir string, rulesYAML []byte, base *string, rec Recorder) error {
	res, err := routerpolicy.SetRulesIf(stateDir, rulesYAML, base, router.CheckPolicy)
	if err != nil {
		return err
	}
	return rec(Change{File: routerpolicy.FileName, Action: "router.policy.set", What: "router rules", Res: res})
}
