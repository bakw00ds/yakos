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

// Follow-ups from round 2.
func TestSanitize_NeverPathsCaseInsensitiveAndCredentialFiles(t *testing.T) {
	for _, p := range []string{"a/b/.ENV", "A/Secrets/x.txt", "Keys/ID.KEY", "C:\\Users\\me\\.AWS\\Credentials",
		"~/.aws/credentials", "/home/u/.netrc", ".NETRC", "home/u/.git-credentials", "/root/.pgpass", "proj/.npmrc", "/home/u/.docker/config.json", ".docker/config.json"} {
		out, s, err := Sanitize(map[string]any{"file_path": p, "body": "TOPSECRETBODY"}, allowAll("file_path", "body"))
		if err != nil {
			t.Fatal(err)
		}
		if out.(map[string]any)["body"] != withheldMarker || !s.Withheld {
			t.Errorf("%q: sibling content not withheld: %v", p, out)
		}
	}
	// Ordinary look-alikes are not withheld.
	for _, p := range []string{"src/env.go", "docs/aws-credentials-guide.md", "cmd/netrc_test.go"} {
		out, _, _ := Sanitize(map[string]any{"file_path": p, "body": "fine"}, allowAll("file_path", "body"))
		if out.(map[string]any)["body"] != "fine" {
			t.Errorf("%q wrongly withheld", p)
		}
	}
}

func TestSanitize_LongMapKeyCannotSplitAToken(t *testing.T) {
	for back := 1; back <= len(fakeGH); back++ {
		key := strings.Repeat("k", 256-back) + fakeGH
		out, _, _ := Sanitize(map[string]any{"m": map[string]any{key: "v"}}, allowAll("m"))
		b, _ := json.Marshal(out)
		for i := 0; i+8 <= len(fakeGH); i++ {
			if strings.Contains(string(b), fakeGH[i:i+8]) {
				t.Fatalf("back=%d: fragment %q leaked through a truncated key", back, fakeGH[i:i+8])
			}
		}
	}
}

func TestSanitize_OperatorPatternCaseInsensitive(t *testing.T) {
	out, _, _ := Sanitize(map[string]any{"file_path": "infra/prod/vars.tf", "body": "x"},
		SanitizeOptions{Level: EgressStrict, AllowedFields: []string{"file_path", "body"}, NeverPaths: []string{"Infra/PROD/*"}})
	if out.(map[string]any)["body"] != withheldMarker {
		t.Error("a mixed-case operator never_paths entry must match case-insensitively")
	}
}

// K-111 P2b: credentials that ride on a command line as flags must not leave.
func TestRedact_CommandLineFlagCredentials(t *testing.T) {
	cases := []struct{ in, secret string }{
		{"curl -u admin:Hunter2Secret! https://x.example", "Hunter2Secret"},
		{"curl --user admin:Hunter2Secret! https://x.example", "Hunter2Secret"},
		{"curl -uadmin:Hunter2Secret! https://x.example", "Hunter2Secret"},
		{"curl -u bob:TAILSECRET99 https://x", "TAILSECRET99"},
		{"tool --password Passw0rdSpace run", "Passw0rdSpace"},
		{"tool --password=Passw0rdEq run", "Passw0rdEq"},
		{"tool --api-key K3yValueABC999 run", "K3yValueABC999"},
		{"tool --client-secret Cl1entSecretVal run", "Cl1entSecretVal"},
		{"tool --access-token=Acc3ssTok3nVal run", "Acc3ssTok3nVal"},
		{"docker login -u me -p DockerPw123 reg", "DockerPw123"},
		{"podman login reg -p PodmanPw123", "PodmanPw123"},
		{"mysql -u root -pS3cretPW db", "S3cretPW"},
		{"mysqldump -uroot -pDumpPw123 db", "DumpPw123"},
		{"redis-cli -a RedisPw123 ping", "RedisPw123"},
		{"sshpass -p 'Sshpass999' ssh host", "Sshpass999"},
		{"sshpass -pSshpassAttached1 ssh host", "SshpassAttached1"},
		{"htpasswd -b /etc/htpasswd alice HtPw12345", "HtPw12345"},
		{"curl -H 'X-Api-Key: XkeyValue12345' https://x", "XkeyValue12345"},
		{"curl -H 'Authorization: Token AuthTok12345' https://x", "AuthTok12345"},
		{"echo apikey_abcdef0123456789abcdef", "abcdef0123456789abcdef"},
		// Re-review: tab separators and quoted values containing a space.
		{"tool\t--password\tSEC15pw run", "SEC15pw"},
		{"curl -u\tu:SEC16pw https://x", "SEC16pw"},
		{"curl --user\tu:SEC16bpw https://x", "SEC16bpw"},
		{`curl -u "u:SEC17a SEC17b" https://x`, "SEC17b"},
		{`curl -u 'u:SEC18a SEC18b' https://x`, "SEC18b"},
		{`curl --user "u:SEC19a SEC19b" https://x`, "SEC19b"},
		{"tool --password \"SEC20a SEC20b\" run", "SEC20b"},
		{"tool --api-key\t'SEC21a SEC21b' run", "SEC21b"},
		{"sshpass -p\tSEC22pw ssh h", "SEC22pw"},
		{"redis-cli -a\tSEC23pw ping", "SEC23pw"},
		{"docker login -u me -p\tSEC24pw reg", "SEC24pw"},
		// Round 3: the class, not the variants. Any whitespace run, continuations, CRLF, NBSP.
		{"tool --password  SEC30pw run", "SEC30pw"},
		{"tool --password \t \t SEC31pw run", "SEC31pw"},
		{"curl -u   u:SEC32pw https://x", "SEC32pw"},
		{"tool --password \\\n  SEC33pw run", "SEC33pw"},
		{"tool --password \\\r\n SEC34pw run", "SEC34pw"},
		{"tool --password\u00a0SEC35pw run", "SEC35pw"},
		{"tool --password\u2003\u00a0SEC36pw run", "SEC36pw"},
		{"curl -u\u00a0u:SEC37pw https://x", "SEC37pw"},
		{"curl -u \\\n \"u:SEC38a SEC38b\" https://x", "SEC38b"},
		{"sshpass -p  SEC39pw ssh h", "SEC39pw"},
		{"redis-cli -a \\\n SEC40pw ping", "SEC40pw"},
		{"docker login -u me -p \u00a0 SEC41pw reg", "SEC41pw"},
		{"mysql -u root \\\n -pSEC42pw db", "SEC42pw"},
		{"echo apikey_" + strings.Repeat("Ab1", 15), "Ab1Ab1Ab1"},
	}
	for _, c := range cases {
		n := 0
		out := RedactText(c.in, &n)
		if strings.Contains(out, c.secret) || n == 0 {
			t.Errorf("credential survived (n=%d): %q -> %q", n, c.in, out)
		}
		// Redaction is idempotent: the second pass over a truncated preview
		// must not re-count or corrupt an already redacted value.
		if again := RedactText(out, nil); again != out {
			t.Errorf("not idempotent: %q -> %q -> %q", c.in, out, again)
		}
	}
	// Ordinary commands with lookalike flags are left alone.
	for _, in := range []string{
		"git commit --author=Bob -m fix",
		"docker run -p 8080:80 img",
		"mysql -P 3306 -h db",
		"ls -p /tmp",
		"curl -u",
		"tail -a file",
		"npm run build --pass-thru-x",
	} {
		n := 0
		out := RedactText(in, &n)
		if strings.Contains(in, "pass-thru") {
			continue // over-redaction of a --pass* flag is acceptable
		}
		if out != in {
			t.Errorf("false positive: %q -> %q", in, out)
		}
	}
}

// The flag credentials are also gone from the Sanitize output the provider gets.
func TestSanitize_CommandFlagCredentialsNeverLeave(t *testing.T) {
	out, _, err := Sanitize(map[string]any{"tool": "Bash", "command_or_diff_preview": "curl -u deploy:ProdPassw0rd https://api && mysql -pS3cretPW db"},
		SanitizeOptions{Level: EgressStrict, AllowedFields: []string{"tool", "command_or_diff_preview"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	for _, leak := range []string{"ProdPassw0rd", "S3cretPW"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%s leaked: %s", leak, b)
		}
	}
}

// The redacted text is whitespace-normalised: that is what is sent.
func TestRedactText_NormalisesWhitespace(t *testing.T) {
	out := RedactText("a  b\t\tc\\\nd\r\ne\u00a0f", nil)
	if out != "a b c d e f" {
		t.Errorf("got %q", out)
	}
	// Env-file lines are still redacted to the end of the line before the join.
	out = RedactText("DB_PASS=two words here\nPORT=8080\n", nil)
	if strings.Contains(out, "words") || !strings.Contains(out, "PORT=8080") {
		t.Errorf("env line handling: %q", out)
	}
}

// Both PRs' rules compose: whitespace normalisation runs first, then the
// keep-context basic-auth table, then the flag rules. The command and flag stay.
func TestRedact_NormalisationComposesWithKeepContext(t *testing.T) {
	cases := []struct{ in, want string }{
		{"curl -s -u admin:Hunter2pw https://x", "curl -s -u [REDACTED:basic-auth] https://x"},
		{"curl  -s\t-u   admin:Hunter2pw   https://x", "curl -s -u [REDACTED:basic-auth] https://x"},
		{"curl -s -u \\\n admin:Hunter2pw https://x", "curl -s -u [REDACTED:basic-auth] https://x"},
	}
	for _, c := range cases {
		if got := RedactText(c.in, nil); got != c.want {
			t.Errorf("%q -> %q, want %q", c.in, got, c.want)
		}
	}
	chain := "curl -u a:CHAIN1pw https://x && curl --user c:CHAIN2pw https://y && mysql -pCHAIN3pw db && tool --password  CHAIN4pw && sshpass -p CHAIN5pw ssh h"
	out := RedactText(chain, nil)
	for _, leak := range []string{"CHAIN1pw", "CHAIN2pw", "CHAIN3pw", "CHAIN4pw", "CHAIN5pw"} {
		if strings.Contains(out, leak) {
			t.Errorf("%s leaked: %q", leak, out)
		}
	}
	if !strings.Contains(out, "curl -u [REDACTED:basic-auth] https://x") || !strings.Contains(out, "curl --user [REDACTED:basic-auth] https://y") {
		t.Errorf("keep-context output lost the command or flag: %q", out)
	}
}
