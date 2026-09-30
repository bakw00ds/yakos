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
