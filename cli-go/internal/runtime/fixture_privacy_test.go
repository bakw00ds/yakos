package runtime

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Recordings under tests/fixtures/runtime-streams are real harness output, and a
// real run names the directory it ran in, the home directory and sometimes the
// account. adapter-argv-recordings.md says the committed files were redacted to
// /work/project and /Users/user; this guard keeps that true for every
// recording, whoever recorded it: it knows no machine in particular.

// redactedUser is the stand-in account name the redaction writes
// (/Users/user, /home/user).
const redactedUser = "user"

var (
	// A home directory of any account on macOS, Linux or Windows, other than the
	// stand-in. The Windows form is matched with doubled backslashes too, as
	// they appear inside a JSON string.
	reUnixHome    = regexp.MustCompile(`/(?:Users|home)/([^/\s"'\\]+)`)
	reWindowsHome = regexp.MustCompile(`(?i)[a-z]:\\{1,2}users\\{1,2}([^\\/\s"']+)`)
	// Per-user temporary and scratch locations: macOS /var/folders and
	// /private/var, the harness scratch directories, and Claude Code's
	// per-session /tmp/claude-<uid> tree.
	reScratch = regexp.MustCompile(`/var/folders/|/private/var/|/private/tmp|/tmp/claude-|/claude-\d+(?:/|"|$)|scratchpad`)
)

// identity is what a recording must not reveal about the machine running the
// test: the account names and home directories to look for in addition to the
// patterns above.
type identity struct {
	userNames []string
	homeDirs  []string
}

// currentIdentity gathers the running account's names and home directory.
func currentIdentity() identity {
	var id identity
	seen := map[string]bool{}
	addName := func(n string) {
		if i := strings.LastIndexAny(n, `\/`); i >= 0 { // DOMAIN\name
			n = n[i+1:]
		}
		n = strings.TrimSpace(n)
		if len(n) < 2 || strings.EqualFold(n, redactedUser) || seen[strings.ToLower(n)] {
			return
		}
		seen[strings.ToLower(n)] = true
		id.userNames = append(id.userNames, n)
	}
	if u, err := user.Current(); err == nil {
		addName(u.Username)
	}
	for _, k := range []string{"USER", "LOGNAME", "USERNAME"} {
		addName(os.Getenv(k))
	}
	if h, err := os.UserHomeDir(); err == nil && len(h) > 3 {
		id.homeDirs = append(id.homeDirs, h)
	}
	return id
}

// findPersonalData lists what in text identifies a machine or a person. An empty
// result means the text is clean.
func (id identity) findPersonalData(text string) []string {
	var found []string
	add := func(kind, what string) { found = append(found, kind+" "+what) }

	for _, m := range reUnixHome.FindAllStringSubmatch(text, -1) {
		if m[1] != redactedUser {
			add("home directory", m[0])
		}
	}
	for _, m := range reWindowsHome.FindAllStringSubmatch(text, -1) {
		if !strings.EqualFold(m[1], redactedUser) {
			add("home directory", m[0])
		}
	}
	for _, m := range reScratch.FindAllString(text, -1) {
		add("temporary or scratch path", m)
	}
	// Any @ is an e-mail address or a handle; recordings carry neither.
	if strings.Contains(text, "@") {
		add("@ (e-mail address or handle)", "@")
	}
	for _, h := range id.homeDirs {
		if strings.Contains(text, h) || strings.Contains(text, strings.ReplaceAll(h, `\`, `\\`)) {
			add("this machine's home directory", h)
		}
	}
	for _, n := range id.userNames {
		// The name as a whole token: a path segment, a quoted value, a word.
		re := regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(n) + `(?:[^A-Za-z0-9_.-]|$)`)
		if re.MatchString(text) {
			add("the current user name", n)
		}
	}
	return found
}

// TestRecordingsContainNoPersonalData scans every recording in the fixture
// directory, so a new file cannot escape the check by being left off a list.
func TestRecordingsContainNoPersonalData(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(streamFixtureDir, "*.ndjson"))
	if len(files) == 0 {
		t.Skip("fixtures not reachable from the package dir")
	}
	id := currentIdentity()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range id.findPersonalData(string(b)) {
			t.Errorf("%s contains %s", filepath.Base(f), hit)
		}
	}
}

// TestPersonalDataDetector pins what the guard catches and what it lets by. Each
// class below was a gap or a machine-specific rule once; removing its rule from
// findPersonalData must fail this test.
func TestPersonalDataDetector(t *testing.T) {
	id := identity{userNames: []string{"dave"}, homeDirs: []string{"/opt/odd-home"}}
	for _, tc := range []struct {
		name, text string
		want       string // substring expected in a finding; "" means clean
	}{
		{"macOS home", `{"cwd":"/Users/alice/proj"}`, "/Users/alice"},
		{"macOS home, another user", `/Users/mallory/x`, "/Users/mallory"},
		{"Linux home", `{"cwd":"/home/bob/proj"}`, "/home/bob"},
		{"Windows home", `C:\\Users\\carol\\proj`, "carol"},
		{"Windows home, single backslashes", `C:\Users\carol\proj`, "carol"},
		{"macOS per-user temp", `/var/folders/ab/cdef/T/x`, "/var/folders/"},
		{"macOS real temp", `/private/var/folders/ab/T`, "/private/var/"},
		{"private tmp", `/private/tmp/x`, "/private/tmp"},
		{"claude session tmp", `/tmp/claude-1234/work`, "/tmp/claude-"},
		{"claude uid directory", `"/claude-1001/"`, "/claude-1001"},
		{"scratch dir", `the scratchpad dir`, "scratchpad"},
		{"e-mail", `a@b.example`, "@"},
		{"the current user name as a path segment", `/work/dave/proj`, "dave"},
		{"the current user name as a quoted value", `{"user":"dave"}`, "dave"},
		{"this machine's home directory", `{"home":"/opt/odd-home/x"}`, "/opt/odd-home"},

		{"redacted macOS home is fine", `{"cwd":"/work/project","err":"sh: /Users/user/p.txt: Operation not permitted"}`, ""},
		{"redacted Linux home is fine", `/home/user/x`, ""},
		{"redacted Windows home is fine", `C:\\Users\\user\\x`, ""},
		{"a model id with claude- is fine", `claude-opus-5-5-high claude-3-5-sonnet`, ""},
		{"a word containing the user name is fine", `davenport davey`, ""},
		{"an empty text is fine", ``, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := id.findPersonalData(tc.text)
			joined := strings.Join(got, "; ")
			switch {
			case tc.want == "" && len(got) != 0:
				t.Errorf("clean text flagged: %s", joined)
			case tc.want != "" && !strings.Contains(joined, tc.want):
				t.Errorf("want a finding containing %q, got %q", tc.want, joined)
			}
		})
	}
}

// TestCurrentIdentityNamesThisMachine: the guard must know the account it runs
// as, whichever it is.
func TestCurrentIdentityNamesThisMachine(t *testing.T) {
	t.Setenv("USER", "someone-else")
	t.Setenv("LOGNAME", "User") // the redaction's stand-in, in another case
	t.Setenv("USERNAME", `CORP\domain-name`)
	names := currentIdentity().userNames
	has := func(want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}
	if !has("someone-else") {
		t.Errorf("the USER environment variable must be among the names looked for, got %v", names)
	}
	if !has("domain-name") {
		t.Errorf("a DOMAIN\\name account must be reduced to its name, got %v", names)
	}
	for _, n := range names {
		if strings.EqualFold(n, redactedUser) {
			t.Errorf("the redaction's stand-in %q must never be looked for, got %v", redactedUser, names)
		}
	}
}
