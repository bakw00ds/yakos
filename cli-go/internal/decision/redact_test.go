package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Secrets are assembled at runtime so this file never contains a literal that
// trips the repo's own secret scanners.
var (
	fakeAWS = "AKIA" + "IOSFODNN7EXAMPLE"
	fakeGH  = "ghp_" + strings.Repeat("a1B2", 9)
	fakePEM = "-----BEGIN RSA PRIV" + "ATE KEY-----\nMIIEowIBAAKCAQEAxyz\nabcdef\n-----END RSA PRIV" + "ATE KEY-----"
	fakeEnv = "DATABASE_URL=postgres://u:hunter2@db/x\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\nSTRIPE_TOKEN=abc123def456"
)

func allowAll(fields ...string) SanitizeOptions {
	return SanitizeOptions{Level: EgressStrict, AllowedFields: fields}
}

func TestSanitize_SecretsNeverLeave(t *testing.T) {
	state := map[string]any{
		"tool":    "Write",
		"preview": "x = '" + fakeAWS + "'\ntoken " + fakeGH + "\n" + fakePEM + "\n" + fakeEnv,
	}
	out, st, err := Sanitize(state, allowAll("tool", "preview"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	for _, leak := range []string{fakeAWS, fakeGH, "MIIEowIBAAKCAQEAxyz", "abcdef", "hunter2", "wJalrXUtnFEMI", "abc123def456", "BEGIN RSA"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("leaked %q in %s", leak, b)
		}
	}
	if st.Redactions < 5 {
		t.Errorf("redactions = %d, want >= 5", st.Redactions)
	}
}

// End to end: the bytes on the wire, as seen by the server.
func TestEgress_RequestBodyNeverCarriesSecrets(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = readAll(r)
		_, _ = w.Write(okBody(PinnedModel))
	}))
	defer srv.Close()
	j := &Jev{Getenv: func(k string) string {
		if k == KeyEnv {
			return "k"
		}
		return srv.URL
	}}
	set := &QuestionSet{Surface: "supervisor-prefilter", SchemaID: "supervisor-prefilter@1", Model: PinnedModel,
		StateFields: []string{"tool", "file_path", "preview"}, Questions: testQuestions(), MaxStateBytes: 8192}
	eng := &Engine{Provider: j, Egress: EgressConfig{Level: EgressStrict}}
	for name, state := range map[string]map[string]any{
		"aws key":      {"tool": "Write", "file_path": "src/a.go", "preview": "k=" + fakeAWS},
		"private key":  {"tool": "Write", "file_path": "src/a.go", "preview": fakePEM},
		".env content": {"tool": "Write", "file_path": "app/.env", "preview": fakeEnv},
		".env no path": {"tool": "Write", "file_path": "src/config.go", "preview": fakeEnv},
	} {
		out := eng.Execute(context.Background(), set, state, ModeShadow, "s", 0)
		if out.Err != nil {
			t.Fatalf("%s: %v", name, out.Err)
		}
		for _, leak := range []string{fakeAWS, "MIIEowIBAAKCAQEAxyz", "hunter2", "wJalrXUtnFEMI", "abc123def456"} {
			if strings.Contains(string(got), leak) {
				t.Errorf("%s: %q reached the wire: %s", name, leak, got)
			}
		}
	}
}

func TestSanitize_NeverPathsWithholdContentKeepPath(t *testing.T) {
	for _, p := range []string{".env", "app/.env.local", "certs/server.pem", "keys/id.key", "x/credentials/a.json", "a/secrets/b.txt", "/home/u/.ssh/id_rsa"} {
		out, st, err := Sanitize(map[string]any{"file_path": p, "preview": "harmless text", "note": "cat"}, allowAll("file_path", "preview", "note"))
		if err != nil {
			t.Fatal(err)
		}
		m := out.(map[string]any)
		if m["file_path"] != p {
			t.Errorf("%s: path itself should be sent, got %v", p, m["file_path"])
		}
		if m["preview"] != withheldMarker || m["note"] != withheldMarker || !st.Withheld {
			t.Errorf("%s: content must be withheld: %v", p, m)
		}
	}
	// Operator-supplied never_paths.
	out, _, _ := Sanitize(map[string]any{"file_path": "infra/prod/vars.tf", "preview": "x"},
		SanitizeOptions{Level: EgressStrict, AllowedFields: []string{"file_path", "preview"}, NeverPaths: []string{"infra/prod/*"}})
	if out.(map[string]any)["preview"] != withheldMarker {
		t.Error("operator never_paths not honoured")
	}
	// A normal path is untouched.
	out, st, _ := Sanitize(map[string]any{"file_path": "src/main.go", "preview": "fine"}, allowAll("file_path", "preview"))
	if out.(map[string]any)["preview"] != "fine" || st.Withheld {
		t.Error("normal paths must pass through")
	}
}

func TestSanitize_AllowlistDropsUnlistedFields(t *testing.T) {
	out, _, _ := Sanitize(map[string]any{"tool": "Edit", "env": map[string]any{"HOME": "/x"}, "extra": "y"}, allowAll("tool"))
	m := out.(map[string]any)
	if len(m) != 1 || m["tool"] != "Edit" {
		t.Errorf("only allowlisted fields may leave: %v", m)
	}
	out, _, _ = Sanitize(map[string]any{"tool": "Edit"}, SanitizeOptions{Level: EgressStrict})
	if len(out.(map[string]any)) != 0 {
		t.Error("empty allowlist sends nothing")
	}
}

func TestSanitize_StrictPreviewCapAndLevels(t *testing.T) {
	long := strings.Repeat("a b ", 5000) // 20000 bytes, no secrets
	get := func(level string) string {
		out, _, err := Sanitize(map[string]any{"preview": long}, SanitizeOptions{Level: level, AllowedFields: []string{"preview"}})
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)["preview"].(string)
	}
	if n := len(get(EgressStrict)); n > strictPreviewBytes+len(truncMarker) || n < strictPreviewBytes {
		t.Errorf("strict length = %d", n)
	}
	if n := len(get(EgressPreviews)); n > previewsPreviewBytes+len(truncMarker) || n < previewsPreviewBytes {
		t.Errorf("previews length = %d", n)
	}
	if n := len(get(EgressFull)); n != len(long) {
		t.Errorf("full length = %d", n)
	}
	if n := len(get("nonsense")); n > strictPreviewBytes+len(truncMarker) {
		t.Errorf("unknown level must fail closed to strict, got %d", n)
	}
}

func TestSanitize_HardCap(t *testing.T) {
	big := map[string]any{}
	fields := []string{}
	for i := 0; i < 40; i++ {
		k := "f" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		big[k] = strings.Repeat("z ", 1500)
		fields = append(fields, k)
	}
	_, _, err := Sanitize(big, SanitizeOptions{Level: EgressFull, AllowedFields: fields})
	if ErrorClass(err) != ClassOversize {
		t.Fatalf("class = %q, want oversize", ErrorClass(err))
	}
	// Schema cap smaller than hard cap is honoured.
	_, _, err = Sanitize(map[string]any{"p": strings.Repeat("y ", 600)}, SanitizeOptions{Level: EgressFull, AllowedFields: []string{"p"}, MaxBytes: 100})
	if ErrorClass(err) != ClassOversize {
		t.Fatal("schema max_state_bytes not honoured")
	}
	// A schema cap above the hard cap is clamped.
	_, _, err = Sanitize(map[string]any{"p": strings.Repeat("y ", 40000)}, SanitizeOptions{Level: EgressFull, AllowedFields: []string{"p"}, MaxBytes: 10_000_000})
	if ErrorClass(err) != ClassOversize {
		t.Fatal("hard cap must clamp the schema cap")
	}
}

func TestSanitize_RejectsNonObjectState(t *testing.T) {
	for _, s := range []any{"just prose", []any{1, 2}, 5, nil} {
		if _, _, err := Sanitize(s, allowAll("x")); ErrorClass(err) != ClassBadRequest {
			t.Errorf("%v: class = %q", s, ErrorClass(err))
		}
	}
}

func TestSanitize_SensitiveKeysAndNestedValues(t *testing.T) {
	out, _, _ := Sanitize(map[string]any{
		"env": map[string]any{"API_KEY": "plainvalue123", "note": "ok", "list": []any{"Bearer abcdefghijklmnopqrstuvwxyz", "fine"}},
	}, allowAll("env"))
	b, _ := json.Marshal(out)
	s := string(b)
	if strings.Contains(s, "plainvalue123") || strings.Contains(s, "abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("nested secrets leaked: %s", s)
	}
	if !strings.Contains(s, "fine") || !strings.Contains(s, "ok") {
		t.Errorf("benign values must survive: %s", s)
	}
}

func TestRedactText_Shapes(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "abcdefghij12345"
	cases := map[string]string{
		"password = 'hunter2hunter2'": "hunter2hunter2",
		"api_key: sk_test_zzzz":       "sk_test_zzzz",
		jwt:                           "eyJzdWIi",
		strings.Repeat("QUJD", 120):   "QUJDQUJD",
	}
	for in, leak := range cases {
		n := 0
		out := RedactText(in, &n)
		if strings.Contains(out, leak) || n == 0 {
			t.Errorf("%q -> %q (n=%d)", in, out, n)
		}
	}
	n := 0
	if out := RedactText("just a normal sentence about tokens and passwords", &n); out != "just a normal sentence about tokens and passwords" || n != 0 {
		t.Errorf("false positive: %q n=%d", out, n)
	}
}

func TestRedact_TruncatedPEMBlockIsFullyRedacted(t *testing.T) {
	// Preview cut mid-key: no END line present.
	s := "prefix -----BEGIN OPENSSH PRIV" + "ATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA"
	out := RedactText(s, nil)
	if strings.Contains(out, "b3Blbn") || strings.Contains(out, "BEGIN OPENSSH") {
		t.Errorf("leaked: %q", out)
	}
}

func TestRedactText_UsesSecretScanTable(t *testing.T) {
	// Each pattern name in the shared table must redact its own example.
	slack := "xoxb-" + "1234567890-abcdefghij"
	stripe := "sk_live_" + strings.Repeat("Q", 24)
	for _, s := range []string{fakeAWS, fakeGH, slack, stripe} {
		if out := RedactText("v "+s+" v", nil); strings.Contains(out, s) {
			t.Errorf("%q not redacted", s)
		}
	}
}

// ---- review round: F1, F2, F3, F8, F9 ---------------------------------------

// F1: a token straddling the preview cut must not leave as a partial.
func TestSanitize_TokenStraddlingPreviewCutIsFullyRedacted(t *testing.T) {
	tok := fakeGH // 40 chars
	for _, level := range []string{EgressStrict, EgressPreviews} {
		cut := strictPreviewBytes
		if level == EgressPreviews {
			cut = previewsPreviewBytes
		}
		// Every offset from "token starts 39 bytes before the cut" to "starts at the cut".
		for back := 1; back <= len(tok); back++ {
			pad := strings.Repeat("x ", (cut-back)/2)
			pad += strings.Repeat("y", cut-back-len(pad))
			s := pad + tok + " tail"
			out, _, err := Sanitize(map[string]any{"p": s}, SanitizeOptions{Level: level, AllowedFields: []string{"p"}})
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(out)
			// No 8+ char run of the token may appear.
			for i := 0; i+8 <= len(tok); i++ {
				if strings.Contains(string(b), tok[i:i+8]) {
					t.Fatalf("%s back=%d: token fragment %q leaked", level, back, tok[i:i+8])
				}
			}
		}
	}
}

func TestSanitize_PEMStraddlingCutAndAWSKeyAtCut(t *testing.T) {
	for back := 1; back < 40; back++ {
		pad := strings.Repeat("z", strictPreviewBytes-back)
		out, _, _ := Sanitize(map[string]any{"p": pad + fakeAWS + "\n" + fakePEM}, allowAll("p"))
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), "AKIAIOS") || strings.Contains(string(b), "MIIEow") {
			t.Fatalf("back=%d leaked: %s", back, b[len(b)-120:])
		}
	}
}

// F2: never_paths at ANY depth withholds the enclosing content.
func TestSanitize_NestedNeverPaths(t *testing.T) {
	secretBody := "MY_SERVICE_PWD=hunter2hunter2\nSESSION_COOKIE=abcdef123456"
	cases := map[string]map[string]any{
		"tool_input.file_path":          {"tool_input": map[string]any{"file_path": "app/.env", "content": secretBody}},
		"deep + array":                  {"calls": []any{map[string]any{"args": map[string]any{"notebook_path": "keys/id.key", "new_source": secretBody}}}},
		"sibling of the holder":         {"tool_input": map[string]any{"file_path": ".env"}, "preview": secretBody},
		"path key differently cased":    {"Tool": map[string]any{"File_Path": "x/secrets/a.txt", "body": secretBody}},
		"command targets a secret path": {"tool_input": map[string]any{"command": "cat .env.production && echo done", "stdout": secretBody}},
		"quoted command token":          {"cmd": `grep -r "x" 'certs/server.pem'`, "out": secretBody},
	}
	for name, st := range cases {
		fields := []string{}
		for k := range st {
			fields = append(fields, k)
		}
		out, s, err := Sanitize(st, SanitizeOptions{Level: EgressFull, AllowedFields: fields})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		for _, leak := range []string{"hunter2", "abcdef123456", "MY_SERVICE_PWD"} {
			if strings.Contains(string(b), leak) {
				t.Errorf("%s: %q leaked: %s", name, leak, b)
			}
		}
		if !s.Withheld || !strings.Contains(string(b), withheldMarker) {
			t.Errorf("%s: expected withheld content: %s", name, b)
		}
	}
	// The path itself still leaves; unrelated state without a secret path is untouched.
	out, s, _ := Sanitize(map[string]any{"tool_input": map[string]any{"file_path": "app/.env", "content": "x"}}, allowAll("tool_input"))
	if out.(map[string]any)["tool_input"].(map[string]any)["file_path"] != "app/.env" || !s.Withheld {
		t.Errorf("path must be kept: %v", out)
	}
	out, s, _ = Sanitize(map[string]any{"tool_input": map[string]any{"file_path": "src/a.go", "content": "fine"}}, allowAll("tool_input"))
	if out.(map[string]any)["tool_input"].(map[string]any)["content"] != "fine" || s.Withheld {
		t.Errorf("no secret path: nothing withheld: %v", out)
	}
}

// F3: a realistic .env file, none of whose values may leave.
var realisticEnv = strings.NewReplacer("@L@", "live", "@WH@", "whsec", "@SL@", "slack").Replace(`# app config
NODE_ENV=production
PORT=8080
DB_HOST=db.internal
DB_USER=app
DB_PASS=Sup3rS3cretPassw0rd
MY_SERVICE_PWD=hunter2hunter2
STRIPE_KEY=sk_@L@_abc123
STRIPE_WEBHOOK=@WH@_9f8e7d6c5b4a
SLACK_HOOK=https://hooks.@SL@.com/services/T0000/B0000/XXXXXXXXXXXXXXXX
AUTH_HEADER=Basic dXNlcjpwYXNzd29yZA==
export API_AUTH=opaqueOpaque123
AWS_ACCESS_KEY_ID=` + "AKIA" + `IOSFODNN7EXAMPLE
AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
SESSION_SECRET_BASE=deadbeefcafebabe
CACHE_DSN=redis://:cachepw@cache:6379/0
`)

func TestRedact_RealisticEnvFile(t *testing.T) {
	out, _, err := Sanitize(map[string]any{"preview": realisticEnv, "tool": "Bash"}, allowAll("preview", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	for _, leak := range []string{"Sup3rS3cretPassw0rd", "hunter2hunter2", "sk_live_abc123", "whsec_9f8e7d6c5b4a", "T0000/B0000", "XXXXXXXXXXXXXXXX",
		"dXNlcjpwYXNzd29yZA", "opaqueOpaque123", "IOSFODNN7EXAMPLE", "wJalrXUtnFEMI", "deadbeefcafebabe", "cachepw"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%q leaked: %s", leak, b)
		}
	}
	// Non-secret configuration survives, so the preview stays useful.
	for _, keep := range []string{"NODE_ENV=production", "PORT=8080", "DB_HOST=db.internal", "DB_USER=app"} {
		if !strings.Contains(string(b), keep) {
			t.Errorf("%q should survive: %s", keep, b)
		}
	}
}

func TestRedact_BareSecretShapesAndHeaders(t *testing.T) {
	for _, in := range []string{
		"aws secret wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY here",
		"curl -H 'Authorization: Basic dXNlcjpwYXNzd29yZA==' x",
		"curl -H 'Authorization: Digest username=\"u\", response=\"abcdef0123456789\"' x",
		"x-token: Token abcdef0123456789abcdef",
		"hook https://hooks.slack.com/services/T1/B2/abcdEFGH",
		"sk_" + "test_4eC39HqLyjWDarjtT1zdp7dc and rk_" + "live_abcDEF123456",
		"pwd=abc123def456",
		"PGPASS: hunter2hunter2",
	} {
		n := 0
		out := RedactText(in, &n)
		if n == 0 || out == in {
			t.Errorf("not redacted: %q", in)
		}
		for _, leak := range []string{"wJalrXUtnFEMI", "dXNlcjpwYXNzd29yZA", "abcdef0123456789", "T1/B2", "4eC39HqLy", "abcDEF123456", "abc123def456", "hunter2hunter2"} {
			if strings.Contains(out, leak) {
				t.Errorf("%q leaked in %q", leak, out)
			}
		}
	}
	// A 40-hex git SHA and ordinary prose are not secrets.
	for _, in := range []string{"commit 3f786850e387550fdab836ed7e6dc881de23001b done", "the compass bypass keyboard monkey", "PORT=8080 HOST=localhost"} {
		n := 0
		if out := RedactText(in, &n); out != in || n != 0 {
			t.Errorf("false positive: %q -> %q", in, out)
		}
	}
}

// F8: an object under a path-named key still gets the per-string preview cap.
func TestSanitize_PathNamedObjectIsCapped(t *testing.T) {
	big := strings.Repeat("a b ", 5000)
	out, _, err := Sanitize(map[string]any{"file_path": map[string]any{"content": big}}, allowAll("file_path"))
	if err != nil {
		t.Fatal(err)
	}
	got := out.(map[string]any)["file_path"].(map[string]any)["content"].(string)
	if len(got) > strictPreviewBytes+len(truncMarker) {
		t.Errorf("object under a path key skipped the cap: %d bytes", len(got))
	}
}

// F9: JSON map keys are redacted too.
func TestSanitize_MapKeysRedacted(t *testing.T) {
	out, _, _ := Sanitize(map[string]any{"env": map[string]any{fakeAWS: "v", "ok": "fine", fakeGH: "w"}}, allowAll("env"))
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "AKIAIOS") || strings.Contains(string(b), fakeGH[:12]) {
		t.Errorf("secret used as a key leaked: %s", b)
	}
	if !strings.Contains(string(b), `"ok":"fine"`) {
		t.Errorf("benign keys must survive: %s", b)
	}
	// Two secret keys must not collapse into one entry.
	m := out.(map[string]any)["env"].(map[string]any)
	if len(m) != 3 {
		t.Errorf("redacted keys collided: %v", m)
	}
}

func TestSanitize_LongPathStringsSurviveWhole(t *testing.T) {
	p := strings.Repeat("dir-", 1250) + "file.go" // 5007 bytes, over the strict preview
	out, _, _ := Sanitize(map[string]any{"file_path": p}, allowAll("file_path"))
	if out.(map[string]any)["file_path"] != p {
		t.Error("a path under the path cap must not be truncated by the strict preview")
	}
}
