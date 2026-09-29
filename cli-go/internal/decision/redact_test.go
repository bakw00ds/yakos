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
		out, st, err := Sanitize(map[string]any{"file_path": p, "preview": "harmless text", "cmd": "cat"}, allowAll("file_path", "preview", "cmd"))
		if err != nil {
			t.Fatal(err)
		}
		m := out.(map[string]any)
		if m["file_path"] != p {
			t.Errorf("%s: path itself should be sent, got %v", p, m["file_path"])
		}
		if m["preview"] != withheldMarker || m["cmd"] != withheldMarker || !st.Withheld {
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
