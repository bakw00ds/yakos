package dispatch

// jev_shadow_test.go covers K-177, the routing shadow. The properties, each
// proven against a fake Jev server on a loopback port (the real service is never
// called):
//
//   - off by default; only the trusted user policy turns it on, a project file
//     and an untrusted policy file cannot;
//   - it never changes a decision (a matrix over the router paths, run with the
//     shadow off and with it answering a different tier each time);
//   - the payload is the agent name, the route class and at most 2 KiB of task,
//     and nothing else leaves or is logged (sentinel strings);
//   - a sensitive task is never sent;
//   - one attempt, bounded, failures are "unavailable" and never delay a dispatch.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/framework"
	"github.com/bakw00ds/yakos/internal/runtime"
)

const (
	shadowFakeKey    = "tsk-fake-key-for-tests-0000"
	taskSentinel     = "TASK-SENTINEL-k177"
	envSentinel      = "ENV-SENTINEL-k177-do-not-send"
	knowledgeMarker  = "KNOWLEDGE-SENTINEL-k177"
	projectDirMarker = "PROJECT-SENTINEL-k177"
)

// fakeJev is a loopback stand-in for the Jev API. It records every request.
type fakeJev struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
	auth   []string
	// respond writes the reply; the default answers tier.
	tier    string
	status  int           // 0 = 200
	block   chan struct{} // when set, the handler waits on it
	garbage bool
}

func newFakeJev(t *testing.T) *fakeJev {
	t.Helper()
	f := &fakeJev{tier: "opus"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(b))
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		tier, status, block, garbage := f.tier, f.status, f.block, f.garbage
		f.mu.Unlock()
		if block != nil {
			select {
			case <-block:
			case <-r.Context().Done():
			}
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if garbage {
			_, _ = w.Write([]byte("{not json"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "jev-1.13.0",
			"answers": map[string]any{"tier": map[string]any{
				"type": "choice", "choice": tier,
				"probabilities": map[string]float64{"haiku": 0.1, "sonnet": 0.2, "opus": 0.7},
				"confidence":    0.7,
			}},
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 1},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJev) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

// realRoutingTierSet loads lib/decisions/routing-tier.yaml from this checkout.
func realRoutingTierSet(t *testing.T) func(string) (*decision.QuestionSet, error) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "lib", "decisions"))
	if err != nil {
		t.Fatal(err)
	}
	return func(string) (*decision.QuestionSet, error) {
		return decision.LoadSet(dir, decision.RoutingShadowSurface)
	}
}

type shadowOpts struct {
	policy string // body of decision-policy.yml; "" writes none
	mode   os.FileMode
	noKey  bool
}

// shadowOn installs the seams: a temp state dir holding the policy, the fake
// server as the endpoint, a fake key, the real question set. The shadow's wait
// at ledger time is lengthened so a healthy call always lands in the row.
func shadowOn(t *testing.T, f *fakeJev, o shadowOpts) (stateDir string) {
	t.Helper()
	stateDir = t.TempDir()
	if o.policy != "" {
		p := filepath.Join(stateDir, decision.PolicyFileName)
		if err := os.WriteFile(p, []byte(o.policy), 0o600); err != nil {
			t.Fatal(err)
		}
		if o.mode != 0 {
			if err := os.Chmod(p, o.mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	oDir, oGet, oURL, oHTTP, oSet, oTO, oWait := jevShadowStateDir, jevShadowGetenv, jevShadowBaseURL, jevShadowHTTP, jevShadowSet, jevShadowTimeout, jevShadowFinishWait
	t.Cleanup(func() {
		jevShadowStateDir, jevShadowGetenv, jevShadowBaseURL, jevShadowHTTP, jevShadowSet, jevShadowTimeout, jevShadowFinishWait = oDir, oGet, oURL, oHTTP, oSet, oTO, oWait
	})
	jevShadowStateDir = func() string { return stateDir }
	jevShadowGetenv = func(k string) string {
		if k == decision.KeyEnv && !o.noKey {
			return shadowFakeKey
		}
		return ""
	}
	if f != nil {
		jevShadowBaseURL = f.srv.URL
	}
	jevShadowSet = realRoutingTierSet(t)
	jevShadowFinishWait = 3 * time.Second
	return stateDir
}

const policyOn = "routing_shadow: true\n"

// settleAbandonedCall runs (as a cleanup, before the state dir is removed) after
// a test left a call in flight: it releases the fake server and waits for the
// abandoned call to record its failure on the breaker, so nothing writes into
// the state dir while it is being deleted.
func settleAbandonedCall(t *testing.T, f *fakeJev, stateDir string) {
	t.Helper()
	t.Cleanup(func() {
		close(f.block)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(stateDir, decision.ShadowBreakerFileName)); err == nil {
				time.Sleep(50 * time.Millisecond)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// shadowRow runs one real Run (fake CLIs on PATH) and returns the finished row.
func shadowRow(t *testing.T, req Request) map[string]interface{} {
	t.Helper()
	return shadowRowIn(t, req, isolatedLogDir(t))
}

// shadowRowIn is shadowRow with the YAKOS_DISPATCH_LOG directory chosen by the
// caller (isolatedLogDir has already pointed the variable at logDir).
func shadowRowIn(t *testing.T, req Request, logDir string) map[string]interface{} {
	t.Helper()
	rec := fakeCLIs(t)
	_ = rec
	captureRouteLog(t)
	if req.YakosRoot == "" {
		req.YakosRoot = routingRoot(t)
	}
	if req.Project == "" {
		req.Project = t.TempDir()
	}
	if _, _, err := Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := readDispatchLog(t, logDir)
	return events[len(events)-1]
}

func TestJevShadow_OffByDefault(t *testing.T) {
	f := newFakeJev(t)
	shadowOn(t, f, shadowOpts{}) // no policy file
	row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
	if _, ok := row["tier_suggested_by_jev"]; ok {
		t.Errorf("row has a tier with no opt-in: %v", row)
	}
	if _, ok := row["jev_shadow"]; ok {
		t.Errorf("row has a shadow status with no opt-in: %v", row)
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests left the host with the shadow off", n)
	}
}

func TestJevShadow_OnlyTheTrustedUserPolicyEnablesIt(t *testing.T) {
	cases := []struct {
		name string
		o    shadowOpts
		yml  string
	}{
		{"policy says false", shadowOpts{policy: "routing_shadow: false\n"}, ""},
		{"project file asks for it", shadowOpts{}, "decisions:\n  provider: jev\n  routing_shadow: true\n  surfaces:\n    routing-tier:\n      mode: shadow\n"},
		{"project vetoes a user opt-in", shadowOpts{policy: policyOn}, "decisions:\n  provider: none\n"},
		{"project turns the surface off", shadowOpts{policy: policyOn}, "decisions:\n  surfaces:\n    routing-tier:\n      mode: \"off\"\n"},
	}
	if goruntime.GOOS != "windows" {
		cases = append(cases,
			struct {
				name string
				o    shadowOpts
				yml  string
			}{"policy file is group writable", shadowOpts{policy: policyOn, mode: 0o664}, ""})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeJev(t)
			shadowOn(t, f, c.o)
			row := shadowRow(t, Request{AgentName: "plain", Task: "hello", Project: projectWithYML(t, c.yml)})
			if _, ok := row["jev_shadow"]; ok {
				t.Errorf("shadow ran: %v", row)
			}
			if n := len(f.requests()); n != 0 {
				t.Errorf("%d requests left the host", n)
			}
		})
	}
}

func TestJevShadow_KillSwitchTurnsItOff(t *testing.T) {
	f := newFakeJev(t)
	shadowOn(t, f, shadowOpts{policy: policyOn})
	orig := jevShadowGetenv
	jevShadowGetenv = func(k string) string {
		if k == "YAKOS_DECISION_DISABLE" {
			return "1"
		}
		return orig(k)
	}
	row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
	if _, ok := row["jev_shadow"]; ok || len(f.requests()) != 0 {
		t.Errorf("kill switch ignored: %v", row)
	}
}

// The ledger gets the tier, and the request carries exactly three fields.
func TestJevShadow_RecordsTierAndSendsOnlyThePayload(t *testing.T) {
	t.Setenv("K177_ENV_SENTINEL", envSentinel)
	f := newFakeJev(t)
	stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
	project := filepath.Join(t.TempDir(), projectDirMarker)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	task := taskSentinel + " " + strings.Repeat("word ", 1200) // about 6 KiB
	row := shadowRow(t, Request{AgentName: "plain", Task: task, Project: project, ScanExtra: []string{knowledgeMarker}})

	if row["tier_suggested_by_jev"] != "opus" || row["jev_shadow"] != JevShadowOK {
		t.Fatalf("row = tier %v status %v", row["tier_suggested_by_jev"], row["jev_shadow"])
	}
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want exactly 1", len(reqs))
	}
	var wire struct {
		State     map[string]string `json:"state"`
		Model     string            `json:"model"`
		Questions map[string]any    `json:"questions"`
	}
	if err := json.Unmarshal([]byte(reqs[0]), &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.State) != 3 || wire.State["agent"] != "plain" || wire.State["route_class"] != "default" {
		t.Errorf("state = %v, want exactly agent, route_class, task_preview", wire.State)
	}
	prev := wire.State["task_preview"]
	if len(prev) == 0 || len(prev) > 2048 || !strings.HasPrefix(prev, taskSentinel) {
		t.Errorf("task_preview is %d bytes: %.40q", len(prev), prev)
	}
	for _, leak := range []string{envSentinel, knowledgeMarker, projectDirMarker, "lib/agents", shadowFakeKey, "K177_ENV_SENTINEL"} {
		if strings.Contains(reqs[0], leak) {
			t.Errorf("request body holds %q", leak)
		}
	}
	if len(wire.Questions) != 1 {
		t.Errorf("questions = %v", wire.Questions)
	}
	if a := f.auth[0]; a != "Bearer "+shadowFakeKey {
		t.Errorf("authorization header = %q", a)
	}

	// Log sinks: nothing but counts. Neither the task text, the key, nor the
	// environment may appear in the decision log, the breaker or budget files,
	// or the dispatch row.
	sinks := []string{}
	_ = filepath.WalkDir(stateDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			sinks = append(sinks, string(b))
		}
		return nil
	})
	rowJSON, _ := json.Marshal(row)
	sinks = append(sinks, string(rowJSON))
	for _, s := range sinks {
		for _, leak := range []string{taskSentinel, shadowFakeKey, envSentinel, knowledgeMarker, "word word"} {
			if strings.Contains(s, leak) {
				t.Errorf("a log sink holds %q: %.200s", leak, s)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(stateDir, decision.LogFileName)); err != nil {
		t.Errorf("the decision was not logged for compare: %v", err)
	}
}

func TestJevShadow_PayloadBoundIsExactAndRuneSafe(t *testing.T) {
	task := strings.Repeat("a", 2047) + "é" + "tail" // the 2-byte rune straddles byte 2048
	p := jevShadowPayload(Request{AgentName: "x", RouteClass: "default", Task: task})
	got := p["task_preview"].(string)
	if len(got) != 2047 {
		t.Errorf("preview = %d bytes, want 2047 (cut before the split rune)", len(got))
	}
	if g := jevShadowPayload(Request{Task: strings.Repeat("b", 5000)})["task_preview"].(string); len(g) != 2048 {
		t.Errorf("preview = %d bytes, want 2048", len(g))
	}
	if len(p) != 3 {
		t.Errorf("payload has %d fields", len(p))
	}
}

func TestJevShadow_SensitiveTaskIsNeverSent(t *testing.T) {
	secret := ghToken()
	cases := []struct {
		name string
		task string
	}{
		{"secret in the first 2 KiB", "use " + secret},
		{"secret past the cut", strings.Repeat("x ", 1500) + secret},
		{"credential file named", "please cat the .env file and tell me"},
		{"private key header", pemHead() + " abc"},
	}
	for _, c := range cases {
		t.Run(c.name+"/through Run", func(t *testing.T) {
			f := newFakeJev(t)
			shadowOn(t, f, shadowOpts{policy: policyOn})
			row := shadowRow(t, Request{AgentName: "plain", Task: c.task})
			if row["jev_shadow"] != JevShadowSkippedSensitive {
				t.Errorf("jev_shadow = %v", row["jev_shadow"])
			}
			if _, ok := row["tier_suggested_by_jev"]; ok {
				t.Errorf("a tier was recorded for a sensitive task")
			}
			if n := len(f.requests()); n != 0 {
				t.Errorf("%d requests left the host", n)
			}
		})
		// The payload gate on its own: even when the route class said default
		// (a classifier that missed it), the scan of the exact payload stops it.
		t.Run(c.name+"/payload gate alone", func(t *testing.T) {
			f := newFakeJev(t)
			stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
			out := runJevShadow(Request{AgentName: "plain", RouteClass: "default", Task: c.task}, stateDir, decision.DefaultConfig())
			if out.Status != JevShadowSkippedSensitive || len(f.requests()) != 0 {
				t.Errorf("outcome = %+v, requests = %d", out, len(f.requests()))
			}
		})
	}
}

func TestJevShadow_FailuresAreUnavailableAndRowStillWrites(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeJev)
		o     shadowOpts
	}{
		{"http 500", func(f *fakeJev) { f.status = 500 }, shadowOpts{policy: policyOn}},
		{"http 401", func(f *fakeJev) { f.status = 401 }, shadowOpts{policy: policyOn}},
		{"malformed body", func(f *fakeJev) { f.garbage = true }, shadowOpts{policy: policyOn}},
		{"a tier outside the set", func(f *fakeJev) { f.tier = "gpt-9" }, shadowOpts{policy: policyOn}},
		{"no key", func(*fakeJev) {}, shadowOpts{policy: policyOn, noKey: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeJev(t)
			c.setup(f)
			shadowOn(t, f, c.o)
			row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
			if row["jev_shadow"] != JevShadowUnavailable {
				t.Errorf("jev_shadow = %v", row["jev_shadow"])
			}
			if _, ok := row["tier_suggested_by_jev"]; ok {
				t.Errorf("tier recorded on a failure: %v", row["tier_suggested_by_jev"])
			}
			assertField(t, row, "type", "dispatch_finished")
			assertField(t, row, "runtime", "claude")
		})
	}
}

// One attempt: a retryable status is not retried.
func TestJevShadow_NoRetry(t *testing.T) {
	for _, status := range []int{429, 529, 503} {
		f := newFakeJev(t)
		f.status = status
		shadowOn(t, f, shadowOpts{policy: policyOn})
		row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
		if row["jev_shadow"] != JevShadowUnavailable {
			t.Errorf("status %d: jev_shadow = %v", status, row["jev_shadow"])
		}
		if n := len(f.requests()); n != 1 {
			t.Errorf("status %d: %d attempts, want 1", status, n)
		}
	}
}

// The client refuses a host that is not configured: an environment override to
// another host never receives the request or the key.
func TestJevShadow_RefusesAnUnconfiguredHost(t *testing.T) {
	f := newFakeJev(t) // would record anything that reached loopback
	shadowOn(t, f, shadowOpts{policy: policyOn})
	jevShadowBaseURL = ""
	hit := false
	jevShadowHTTP = &http.Client{Transport: roundTripFn(func(*http.Request) (*http.Response, error) {
		hit = true
		return nil, io.EOF
	})}
	orig := jevShadowGetenv
	jevShadowGetenv = func(k string) string {
		if k == decision.BaseURLEnv {
			return "https://collector.example.net"
		}
		return orig(k)
	}
	row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
	if row["jev_shadow"] != JevShadowUnavailable {
		t.Errorf("jev_shadow = %v", row["jev_shadow"])
	}
	if hit || len(f.requests()) != 0 {
		t.Errorf("a request was built for a host that is not *.typesafe.ai or loopback")
	}
}

type roundTripFn func(*http.Request) (*http.Response, error)

func (f roundTripFn) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A call that never answers cannot delay or fail the dispatch: the row is
// written within the finish wait, as unavailable.
func TestJevShadow_SlowServerNeverDelaysTheDispatch(t *testing.T) {
	f := newFakeJev(t)
	f.block = make(chan struct{})
	stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
	jevShadowTimeout = 10 * time.Second
	settleAbandonedCall(t, f, stateDir)
	jevShadowFinishWait = 100 * time.Millisecond
	start := time.Now()
	row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("the dispatch took %v with a hung shadow", d)
	}
	if row["jev_shadow"] != JevShadowUnavailable {
		t.Errorf("jev_shadow = %v", row["jev_shadow"])
	}
	assertField(t, row, "type", "dispatch_finished")
}

// A call that overruns its own timeout is cut at the timeout.
func TestJevShadow_TimeoutIsEnforced(t *testing.T) {
	f := newFakeJev(t)
	f.block = make(chan struct{})
	stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
	settleAbandonedCall(t, f, stateDir)
	jevShadowTimeout = 300 * time.Millisecond
	start := time.Now()
	out := runJevShadow(Request{AgentName: "plain", RouteClass: "default", Task: "hello"}, stateDir, decision.DefaultConfig())
	if out.Status != JevShadowUnavailable {
		t.Errorf("outcome = %+v", out)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v for a 300ms timeout", d)
	}
}

// Finish without Start (a turn whose start is only remembered) never begins the
// shadow: that dispatch has already run.
func TestJevShadow_FinishWithoutStartSendsNothing(t *testing.T) {
	f := newFakeJev(t)
	shadowOn(t, f, shadowOpts{policy: policyOn})
	logDir := isolatedLogDir(t)
	a := newAccountAt(Request{AgentName: "plain", Task: "hello"}, filepath.Join(logDir, "dispatch-log.ndjson"), fixedTime)
	a.Finish(Result{})
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
	events := readDispatchLog(t, logDir)
	if _, ok := events[len(events)-1]["jev_shadow"]; ok {
		t.Errorf("shadow field on a row that never started the shadow")
	}
}

// ---- the shadow never changes a decision --------------------------------------

type shadowScenario struct {
	name   string
	policy string
	agent  string
	task   string
	mut    func(*Request)
	probe  string // runtime whose sign-in probe fails
	repeat int    // run this many times in one conversation (sticky)
}

// decisionOf runs the scenario with the given shadow server (nil = shadow off)
// and returns every routing field of every row plus the CLIs that executed.
func decisionOf(t *testing.T, sc shadowScenario, f *fakeJev) string {
	t.Helper()
	root := routingRoot(t)
	rec := fakeCLIs(t)
	logDir := isolatedLogDir(t)
	setPolicy(t, sc.policy)
	captureRouteLog(t)
	withProbe(t, func(name string) probeResult {
		if name == sc.probe {
			return probeResult{Reason: "not signed in"}
		}
		return probeResult{OK: true}
	})
	if f != nil {
		shadowOn(t, f, shadowOpts{policy: policyOn})
	} else {
		shadowOn(t, nil, shadowOpts{})
	}
	project := projectWithYML(t, "")
	n := sc.repeat
	if n == 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		req := Request{AgentName: sc.agent, Task: sc.task, Project: project, YakosRoot: root, ConversationID: "conv-k177"}
		if sc.mut != nil {
			sc.mut(&req)
		}
		if _, _, err := Run(context.Background(), req); err != nil {
			t.Fatalf("%s: Run: %v", sc.name, err)
		}
	}
	var out []string
	for _, ev := range readDispatchLog(t, logDir) {
		if ev["type"] != "dispatch_finished" {
			continue
		}
		var parts []string
		for _, k := range []string{"runtime", "runtime_chosen_by", "fallback_from", "model", "model_chosen_by", "model_resolved", "route_rule", "route_reason", "route_class", "policy_sha", "provider", "billing"} {
			parts = append(parts, k+"="+asString(ev[k]))
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return strings.Join(out, "\n") + "\nexecuted=" + strings.Join(invoked(t, rec), ",")
}

func asString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestJevShadow_NeverChangesADecision(t *testing.T) {
	long := strings.Repeat("y", 1200)
	scenarios := []shadowScenario{
		{name: "R0 default", policy: "", agent: "plain", task: "hello"},
		{name: "R0 with a policy file and no match", policy: tablePolicy, agent: "plain", task: "hello"},
		{name: "R2 domain rule", policy: tablePolicy, agent: "reviewer", task: "hello"},
		{name: "R3 agent rule with an alias model", policy: tablePolicy, agent: "bare", task: "hello"},
		{name: "R4 size rule", policy: tablePolicy, agent: "gpt-claude", task: long},
		{name: "R6 fallbacks", policy: tablePolicy, agent: "gpt-claude", task: "hello", probe: "claude"},
		{name: "agent pin", policy: "", agent: "general-codex", task: "hello"},
		{name: "explicit runtime", policy: tablePolicy, agent: "reviewer", task: "hello", mut: func(r *Request) { r.Runtime = "agy" }},
		{name: "explicit model", policy: tablePolicy, agent: "plain", task: "hello", mut: func(r *Request) { r.Model = "opus" }},
		{name: "sticky conversation", policy: tablePolicy, agent: "reviewer", task: "hello", repeat: 2},
		{name: "sensitive task", policy: tablePolicy, agent: "general-codex", task: "see " + ghToken()},
		{name: "sensitive task, no policy", policy: "", agent: "plain", task: "see " + ghToken()},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			off := decisionOf(t, sc, nil)
			// The shadow answers a tier that differs from anything routing chose,
			// then another, then fails, then returns junk.
			for _, v := range []struct {
				name string
				set  func(*fakeJev)
			}{
				{"answers opus", func(f *fakeJev) { f.tier = "opus" }},
				{"answers haiku", func(f *fakeJev) { f.tier = "haiku" }},
				{"fails with 500", func(f *fakeJev) { f.status = 500 }},
				{"returns junk", func(f *fakeJev) { f.garbage = true }},
			} {
				f := newFakeJev(t)
				v.set(f)
				if on := decisionOf(t, sc, f); on != off {
					t.Errorf("shadow %s changed the decision:\n off: %s\n  on: %s", v.name, off, on)
				}
			}
		})
	}
}

// The streamed path takes the same decision with the shadow on and off.
func TestJevShadow_StreamPathDecisionUnchangedAndRecorded(t *testing.T) {
	run := func(f *fakeJev) map[string]interface{} {
		logDir := isolatedLogDir(t)
		if f != nil {
			shadowOn(t, f, shadowOpts{policy: policyOn})
		} else {
			shadowOn(t, nil, shadowOpts{})
		}
		yakosRoot := buildFakeRoster(t, "chat-agent", "p")
		svc := NewService(ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: logDir, OperatorID: "op"})
		fake := &fakeClaudeStreamAdapter{lines: streamLinesWithModelAndSession()}
		withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
			return execWithStreaming(ctx, req, fake, chatReq, onChunk)
		}, func() {
			if _, err := svc.RunStream(context.Background(), Params{Agent: "chat-agent", Task: "t", Project: logDir, Surface: SurfaceConsoleChat}, func(StreamChunk) {}); err != nil {
				t.Fatalf("RunStream: %v", err)
			}
		})
		events := readDispatchLog(t, logDir)
		return events[len(events)-1]
	}
	off := run(nil)
	f := newFakeJev(t)
	on := run(f)
	if on["tier_suggested_by_jev"] != "opus" || on["jev_shadow"] != JevShadowOK {
		t.Errorf("stream row: tier %v status %v", on["tier_suggested_by_jev"], on["jev_shadow"])
	}
	for _, k := range []string{"runtime", "runtime_chosen_by", "model", "model_chosen_by", "model_resolved", "route_rule", "route_reason", "route_class"} {
		if asString(off[k]) != asString(on[k]) {
			t.Errorf("%s changed: off %v on %v", k, off[k], on[k])
		}
	}
}

func TestJevShadow_ValidTierIsAClosedSet(t *testing.T) {
	for _, ok := range []string{"haiku", "sonnet", "opus"} {
		if !validTier(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "fable", "Opus", "opus\n", "gpt-5", `{"x":1}`} {
		if validTier(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A route class of sensitive that the caller declared (no secret in the text) is
// still never sent: the class alone is enough.
func TestJevShadow_DeclaredSensitiveClassIsNeverSent(t *testing.T) {
	f := newFakeJev(t)
	shadowOn(t, f, shadowOpts{policy: policyOn})
	s := startJevShadow(Request{AgentName: "plain", RouteClass: "sensitive", Task: "a perfectly clean task"})
	if s == nil {
		t.Fatal("the shadow did not start")
	}
	if got := s.collect(); got.Status != JevShadowSkippedSensitive || got.Tier != "" {
		t.Errorf("outcome = %+v", got)
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests left the host", n)
	}
}

// The egress redactor sees shapes the route scanner does not (a password on a
// command line). A redaction in the payload means the text is not sent at all.
func TestJevShadow_ARedactedPayloadIsNotSent(t *testing.T) {
	f := newFakeJev(t)
	stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
	for _, task := range []string{
		"run curl -u admin:" + "hunter2value https://internal.example/x",
		"export DB_PASSWORD=" + "correct-horse-battery-staple && ./migrate",
	} {
		out := runJevShadow(Request{AgentName: "plain", RouteClass: "default", Task: task}, stateDir, decision.DefaultConfig())
		if out.Status != JevShadowSkippedSensitive {
			t.Errorf("%.30q: outcome = %+v", task, out)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests left the host", n)
	}
}

// In a built binary the question set comes from the embedded lib, not from the
// project or the root, and it is the same file as the source.
func TestJevShadow_EmbeddedSetMatchesSource(t *testing.T) {
	if !framework.HasEmbeddedLib() {
		t.Skip("no embedded lib in this build (run make embed-lib)")
	}
	emb, err := loadRoutingTierSet("")
	if err != nil {
		t.Fatal(err)
	}
	src, err := realRoutingTierSet(t)("")
	if err != nil {
		t.Fatal(err)
	}
	if emb.Hash != src.Hash || emb.Model != decision.PinnedModel {
		t.Errorf("embedded %s (%s) vs source %s", emb.Hash, emb.Model, src.Hash)
	}
	if got := strings.Join(emb.StateFields, ","); got != "agent,route_class,task_preview" {
		t.Errorf("state_fields = %s", got)
	}
}

// sec-370 HIGH: YAKOS_DISPATCH_LOG is project-settable (a committed
// .claude/settings.json env block), so a policy planted behind it must not turn
// the shadow on. The opt-in is read only from $HOME/.yakos-state.
func TestJevShadow_PlantedPolicyBehindDispatchLogEnvCannotEnableIt(t *testing.T) {
	f := newFakeJev(t)
	shadowOn(t, f, shadowOpts{})
	// TestMain blanks the seam; restore the value the product ships with. A
	// separate test below pins that value to statepath.TrustedDir.
	jevShadowStateDir = jevShadowStateDirProduct
	logDir := isolatedLogDir(t) // resets HOME, so set the home after it
	home := t.TempDir()         // no ~/.yakos-state, no policy
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	planted := filepath.Join(logDir, decision.PolicyFileName)
	if err := os.WriteFile(planted, []byte(policyOn), 0o644); err != nil { // as a git checkout leaves it
		t.Fatal(err)
	}
	if got := jevShadowStateDirProduct(); got != filepath.Join(home, ".yakos-state") || got == logDir {
		t.Errorf("the product default state dir is %q", got)
	}
	row := shadowRowIn(t, Request{AgentName: "plain", Task: "hello"}, logDir)
	if _, ok := row["jev_shadow"]; ok {
		t.Errorf("a planted policy enabled the shadow: %v", row)
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests left the host from a planted policy", n)
	}
}

// sec-370 LOW: the shadow has its own breaker and budget files, so its failures
// and volume cannot affect the supervisor pre-filter's.
func TestJevShadow_UsesItsOwnBreakerAndBudgetFiles(t *testing.T) {
	f := newFakeJev(t)
	f.status = 500
	stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
	row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
	if row["jev_shadow"] != JevShadowUnavailable {
		t.Fatalf("row = %v", row)
	}
	for _, name := range []string{decision.BreakerFileName, decision.BudgetFileName} {
		if _, err := os.Stat(filepath.Join(stateDir, name)); err == nil {
			t.Errorf("the shadow touched the pre-filter's %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(stateDir, decision.ShadowBreakerFileName)); err != nil {
		t.Errorf("no shadow breaker file: %v", err)
	}
}

// rev-370: the ledger-write wait is at most 150 ms. A hung call returns within
// about that, and the product constant is pinned.
func TestJevShadow_FinishWaitIsAbout150ms(t *testing.T) {
	if jevShadowFinishWait != 150*time.Millisecond {
		t.Fatalf("jevShadowFinishWait = %v, want 150ms", jevShadowFinishWait)
	}
	hung := &jevShadow{done: make(chan struct{})} // never closed
	start := time.Now()
	out := hung.collect()
	d := time.Since(start)
	if out.Status != JevShadowUnavailable {
		t.Errorf("outcome = %+v", out)
	}
	if d < 100*time.Millisecond || d > 600*time.Millisecond {
		t.Errorf("collect on a hung call took %v, want about 150ms", d)
	}
}

// sec-370 LOW: a hostile response cannot write its model string into the log.
func TestJevShadow_HostileModelStringIsNotLogged(t *testing.T) {
	secret := "AKIA" + strings.Repeat("Q", 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": strings.Repeat("m", 900_000) + secret,
			"answers": map[string]any{"tier": map[string]any{
				"type": "choice", "choice": "opus",
				"probabilities": map[string]float64{"haiku": 0.1, "sonnet": 0.2, "opus": 0.7}, "confidence": 0.7,
			}},
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	t.Cleanup(srv.Close)
	f := &fakeJev{srv: srv}
	stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
	shadowRow(t, Request{AgentName: "plain", Task: "hello"})
	data, err := os.ReadFile(filepath.Join(stateDir, decision.LogFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4096 || strings.Contains(string(data), secret) {
		t.Errorf("hostile model reached the decision log (%d bytes)", len(data))
	}
}

// The same, for a successful call: the spend lands in the shadow's own budget.
func TestJevShadow_SuccessSpendsTheShadowsOwnBudget(t *testing.T) {
	f := newFakeJev(t)
	stateDir := shadowOn(t, f, shadowOpts{policy: policyOn})
	row := shadowRow(t, Request{AgentName: "plain", Task: "hello"})
	if row["jev_shadow"] != JevShadowOK {
		t.Fatalf("row = %v", row)
	}
	matches, _ := filepath.Glob(filepath.Join(stateDir, "decision-*budget*"))
	var shadow, prefilter int
	for _, m := range matches {
		if strings.Contains(filepath.Base(m), "shadow") {
			shadow++
		} else {
			prefilter++
		}
	}
	if shadow == 0 || prefilter != 0 {
		t.Errorf("budget files %v: shadow %d, pre-filter %d", matches, shadow, prefilter)
	}
}
