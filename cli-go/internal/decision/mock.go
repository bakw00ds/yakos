package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// MockFixture is the JSON a mock fixture file holds. It is deterministic
// input for CI and bash/Go parity tests; no network and no key are involved.
//
//	{"model":"jev-1.13.0","answers":{"risk_class":{"type":"choice",...}},
//	 "usage":{"input_tokens":100,"output_tokens":0}}
//
// "error" (a class from this package, e.g. "timeout") makes the mock fail
// with that class instead, to exercise every fail-closed path.
type MockFixture struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
	Error   string            `json:"error"`
}

// Mock is the deterministic test provider.
type Mock struct {
	// Fixture is a file, or a directory holding <surface>.json. Empty falls
	// back to $YAKOS_DECISION_MOCK, then to the neutral default answers.
	Fixture string
	Getenv  func(string) string
}

// Name implements Provider.
func (m *Mock) Name() string { return ProviderMock }

// Available implements Provider; the mock is always available unless killed.
func (m *Mock) Available(context.Context) error {
	if KillSwitch(m.Getenv) {
		return newErr(ClassDisabled, "%s=1", killSwitchEnvVar)
	}
	return nil
}

func (m *Mock) fixturePath(surface string) (string, error) {
	p := m.Fixture
	if p == "" {
		g := m.Getenv
		if g == nil {
			g = os.Getenv
		}
		p = g(EnvMock)
	}
	if p == "" {
		return "", nil
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", newErr(ClassBadRequest, "mock fixture: %v", err)
	}
	if !fi.IsDir() {
		return p, nil
	}
	if !ValidSurface(surface) {
		return "", newErr(ClassBadRequest, "mock fixture: invalid surface")
	}
	p = filepath.Join(p, surface+".json")
	if _, err := os.Stat(p); err != nil {
		return "", newErr(ClassBadRequest, "mock fixture: %v", err)
	}
	return p, nil
}

// Decide implements Provider. It never sleeps and never touches the network,
// so identical inputs always give identical outputs.
func (m *Mock) Decide(_ context.Context, req Request) (*Result, error) {
	if KillSwitch(m.Getenv) {
		return nil, newErr(ClassDisabled, "%s=1", killSwitchEnvVar)
	}
	path, err := m.fixturePath(req.Surface)
	if err != nil {
		return nil, err
	}
	var fx MockFixture
	if path == "" {
		fx = neutralFixture(req)
	} else {
		data, rerr := os.ReadFile(path) //nolint:gosec // operator/CI-supplied fixture
		if rerr != nil {
			return nil, newErr(ClassBadRequest, "mock fixture: %v", rerr)
		}
		if jerr := json.Unmarshal(data, &fx); jerr != nil {
			return nil, newErr(ClassMalformed, "mock fixture is not valid JSON")
		}
	}
	if fx.Error != "" {
		return nil, newErr(fx.Error, "mock fixture requested failure")
	}
	if fx.Model == "" {
		fx.Model = req.Model
	}
	wr := wireResponse{Model: fx.Model, Answers: fx.Answers, Usage: fx.Usage}
	if verr := validateResponse(&wr, req.Questions); verr != nil {
		return nil, verr
	}
	answers := make(map[string]Answer, len(req.Questions))
	for id := range req.Questions {
		answers[id] = fx.Answers[id]
	}
	return &Result{Provider: ProviderMock, Model: fx.Model, Answers: answers, Usage: fx.Usage, CostUSD: CostUSD(fx.Usage)}, nil
}

// neutralFixture is the "no opinion" answer: the first option in sorted
// order with a uniform distribution (confidence 0), Noul 0.5, lowest score.
// It can never satisfy a confidence threshold, so an unconfigured mock never
// escalates or blocks anything.
func neutralFixture(req Request) MockFixture {
	fx := MockFixture{Model: req.Model, Answers: map[string]Answer{}}
	zero, half := 0.0, 0.5
	for _, id := range sortedKeys(req.Questions) {
		q := req.Questions[id]
		switch q.Type {
		case "noul":
			h := half
			fx.Answers[id] = Answer{Type: "noul", Noul: &h}
		case "choice":
			opts, _ := asStringMap(q.Criteria)
			keys := make([]string, 0, len(opts))
			for k := range opts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			probs := map[string]float64{}
			for _, k := range keys {
				probs[k] = 1 / float64(len(keys))
			}
			c := zero
			fx.Answers[id] = Answer{Type: "choice", Choice: keys[0], Probabilities: probs, Confidence: &c}
		case "score":
			levels, _ := asStringList(q.Criteria)
			probs := map[string]float64{}
			legend := map[string]string{}
			for i, l := range levels {
				k := fmt.Sprint(i)
				probs[k] = 1 / float64(len(levels))
				legend[k] = l
			}
			s, c := zero, zero
			fx.Answers[id] = Answer{Type: "score", Score: &s, Legend: legend, Probabilities: probs, Confidence: &c}
		}
	}
	return fx
}

// compile-time interface checks
var (
	_ Provider = (*Jev)(nil)
	_ Provider = (*Mock)(nil)
	_ Provider = none{}
)
