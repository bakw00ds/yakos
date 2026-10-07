package runtime

// fixture_version.go names the harness versions the stream parsers were
// recorded against (K-144, plan risk 1: a harness can change its output format
// between releases). The recordings live in testdata/<harness>/<version>/ and a
// test keeps this table equal to the newest directory, so a re-recording moves
// both. `yakos doctor --policy` compares the installed version with it.

import "regexp"

// RecordedVersions is the harness version each LineParser's recordings were
// made with. Only harnesses with a native format of their own are listed.
var RecordedVersions = map[string]string{
	"codex": "0.154.0",
	"agy":   "1.3.1",
}

// reVersion finds a version with an optional leading "v" and an optional
// prerelease suffix ("v1.4.0", "0.154.0-beta.1"). The version itself is group 1
// (the "v" is dropped); the lead-in group stands in for a word boundary, which
// would not fall between "v" and the first digit.
var reVersion = regexp.MustCompile(`(?:^|[^0-9A-Za-z.])v?(\d+\.\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?)`)

// ParseVersion extracts the version number from a `--version` output
// ("codex-cli 0.154.0", "1.3.1", "v1.4.0"). A leading "v" is dropped and a
// prerelease suffix is kept, so VersionSkew reports "0.154.0-beta.1" as a
// difference from "0.154.0". It returns "" when none is present. Only the
// first line is read and it is bounded, since the output is untrusted.
func ParseVersion(out string) string {
	if len(out) > 512 {
		out = out[:512]
	}
	for i := 0; i < len(out); i++ {
		if out[i] == '\n' {
			out = out[:i]
			break
		}
	}
	if m := reVersion.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// VersionSkew reports the version a harness was recorded with and whether the
// installed one differs. Unknown harnesses and unreadable versions report no
// skew: the hint is advisory.
func VersionSkew(harness, installed string) (recorded string, skew bool) {
	recorded = RecordedVersions[harness]
	if recorded == "" || installed == "" {
		return recorded, false
	}
	return recorded, installed != recorded
}
