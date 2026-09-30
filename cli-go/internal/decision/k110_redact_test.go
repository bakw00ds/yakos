package decision

import (
	"strings"
	"testing"
)

// K-110: egress redaction covers curl basic auth, URL credentials and PEM
// bodies (decision egress shares the secretscan table).
func TestRedactText_K110Shapes(t *testing.T) {
	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmU\nQWxwaGFCZXRhR2FtbWE\n-----END OPENSSH PRIVATE KEY-----"
	for _, tc := range []struct{ in, secret string }{
		{"curl -u alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl --user alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl -ualice:s3cretPw https://x.example", "s3cretPw"},
		{`curl -u "alice:pw one sTail" https://x.example`, "sTail"},
		{"curl --proxy-user alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl -U alice:s3cretPw https://x.example", "s3cretPw"},
		{"redis-cli -u redis://:s3cretPw@cache:6379", "s3cretPw"},
		{"x -----BEGIN PGP PRIVATE KEY BLOCK-----\nlQPGBFk2pgpBodyLine\n-----END PGP PRIVATE KEY BLOCK----- y", "lQPGBFk2pgpBodyLine"},
		{"git clone https://bob:hunter2pass@github.com/o/r.git", "hunter2pass"},
		{"run " + pem + " end", "QWxwaGFCZXRhR2FtbWE"},
		{"run " + pem + " end", "b3BlbnNzaC1rZXktdjEAAAAABG5vbmU"},
	} {
		var n int
		out := RedactText(tc.in, &n)
		if strings.Contains(out, tc.secret) || n == 0 {
			t.Errorf("not redacted: %q -> %q (n=%d)", tc.in, out, n)
		}
	}
}

// The curl shape needs a curl-family command in front: container and sort
// flags that look like user:pass are left alone.
func TestRedactText_CurlShapeIgnoresOtherCommands(t *testing.T) {
	for _, in := range []string{"docker run -u 1000:1000 img", "docker run --user 1000:1000 img", "sort -u 12:30", "ls -lu a:b"} {
		var n int
		if out := RedactText(in, &n); out != in || n != 0 {
			t.Errorf("over-redacted %q -> %q (n=%d)", in, out, n)
		}
	}
	var n int
	if out := RedactText("curl -s -u 1admin:s3cretPw https://x.example", &n); strings.Contains(out, "s3cretPw") {
		t.Errorf("digit-leading user leaked: %q", out)
	}
}
