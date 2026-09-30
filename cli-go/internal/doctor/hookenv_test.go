package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func hookEnvFindings(t *testing.T, val string) (Severity, string) {
	t.Helper()
	var buf bytes.Buffer
	cfg := Config{
		HomeDir:  makeTmpHome(t),
		LookPath: noLookPath,
		Environ: func(k string) string {
			if k == "YAKOS_HOOK_JQ_TIMEOUT" {
				return val
			}
			return ""
		},
		Writer: &buf,
	}
	rep, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if val == "" {
		if strings.Contains(buf.String(), "Hook environment") {
			t.Fatalf("unset value must not print a section (default doctor output is unchanged):\n%s", buf.String())
		}
		for _, f := range rep.Findings {
			if f.Section == SectionHookEnv {
				t.Fatalf("unset value produced a finding: %v", f)
			}
		}
		return SeverityInfo, "unset"
	}
	if !strings.Contains(buf.String(), "Hook environment") {
		t.Fatalf("Hook environment section missing:\n%s", buf.String())
	}
	for _, f := range rep.Findings {
		if f.Section == SectionHookEnv {
			return f.Severity, f.Message
		}
	}
	t.Fatalf("no SectionHookEnv finding:\n%s", buf.String())
	return 0, ""
}

// K-110: doctor reports the effective YAKOS_HOOK_JQ_TIMEOUT, mirroring the
// parsing and clamping in lib/hooks/lib/hook-input.sh.
func TestHookEnv_JQTimeout(t *testing.T) {
	for _, tc := range []struct {
		val  string
		sev  Severity
		want string
	}{
		{"", SeverityInfo, "unset"},
		{"8", SeverityOK, "YAKOS_HOOK_JQ_TIMEOUT=8 s"},
		{"08", SeverityOK, "YAKOS_HOOK_JQ_TIMEOUT=8 s"},
		{"0", SeverityInfo, "clamped to 1 s"},
		{"99", SeverityInfo, "clamped to 25 s"},
		{"99999999999999999999999", SeverityInfo, "clamped to 25 s"},
		{"abc", SeverityWarn, "not a whole number"},
		{"-3", SeverityWarn, "not a whole number"},
	} {
		sev, msg := hookEnvFindings(t, tc.val)
		if sev != tc.sev || !strings.Contains(msg, tc.want) {
			t.Errorf("value %q: got (%v, %q), want (%v, ...%q...)", tc.val, sev, msg, tc.sev, tc.want)
		}
	}
}
