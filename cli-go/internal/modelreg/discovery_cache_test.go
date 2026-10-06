package modelreg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- helpers ------------------------------------------------------------------

var discT0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func discCacheOf(probedAt time.Time, ids ...string) cacheFile {
	models := make([]DiscoveredModel, len(ids))
	for i, id := range ids {
		models[i] = DiscoveredModel{ID: id, Name: "Name of " + id}
	}
	return cacheFile{Schema: cacheSchema, Harnesses: map[string]cacheHarness{
		agyHarness: {Source: agySource, ProbedAt: probedAt, Models: models},
	}}
}

// discWriteCache writes f as the cache file with exactly mode (umask included).
func discWriteCache(t *testing.T, dir string, f cacheFile, mode os.FileMode) string {
	t.Helper()
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return discWriteCacheBytes(t, dir, data, mode)
}

func discWriteCacheBytes(t *testing.T, dir string, data []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, DiscoveryFileName)
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// discPrivateDir returns a fresh state directory the trust check accepts.
func discPrivateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// discSnapshotFrom is what a dispatch sees: a new Discoverer over dir asked for
// its snapshot, with the clock at discT0.
func discSnapshotFrom(dir string) (Snapshot, bool) {
	d := NewDiscoverer(DiscovererConfig{StateDir: dir, Now: func() time.Time { return discT0 }})
	return d.Snapshot("agy")
}

func discSkipPosixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mode bits and symlinks are not the trust boundary on Windows")
	}
}

// ---- reading ----------------------------------------------------------------------

func TestDiscoveryCache_RoundTripAcrossInstances(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(rig.stateDir, DiscoveryFileName))
	if err != nil {
		t.Fatalf("no cache written: %v", err)
	}
	var f cacheFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("the cache is not JSON: %v\n%s", err, raw)
	}
	h := f.Harnesses[agyHarness]
	if f.Schema != 1 || h.Source != "agy models" || len(h.Models) != 3 || !h.ProbedAt.Equal(rig.clock.Now()) {
		t.Errorf("cache content: %+v", f)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("the cache should end with a newline")
	}
	got, ok := rig.another().Snapshot("agy")
	if !ok || !reflect.DeepEqual(discIDs(got), []string{"model-a", "model-b", "model-c"}) || got.Models[1].Name != "Name of model-b" {
		t.Errorf("round trip: %+v %v", got, ok)
	}
}

func TestDiscoveryCache_TrustedModesAreAccepted(t *testing.T) {
	discSkipPosixOnly(t)
	// The same rule as the router policy and the default-runtime file: only a file
	// someone ELSE could write is refused. Readable by others is fine.
	for _, mode := range []os.FileMode{0o600, 0o644, 0o640, 0o400} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, discCacheOf(discT0.Add(-time.Hour), "model-a"), mode)
			if _, ok := discSnapshotFrom(dir); !ok {
				t.Errorf("a %o cache owned by this user was refused", mode)
			}
		})
	}
}

func TestDiscoveryCache_UntrustedFilesAreIgnored(t *testing.T) {
	discSkipPosixOnly(t)
	valid := discCacheOf(discT0.Add(-time.Hour), "model-a")
	cases := []struct {
		name  string
		setup func(t *testing.T) string // returns the state dir
	}{
		{"symlink to a valid cache", func(t *testing.T) string {
			dir := discPrivateDir(t)
			elsewhere := t.TempDir()
			target := discWriteCache(t, elsewhere, valid, 0o600)
			if err := os.Symlink(target, filepath.Join(dir, DiscoveryFileName)); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{"world writable file", func(t *testing.T) string {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, valid, 0o666)
			return dir
		}},
		{"group writable file", func(t *testing.T) string {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, valid, 0o660)
			return dir
		}},
		{"group write bit only", func(t *testing.T) string {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, valid, 0o620)
			return dir
		}},
		{"world write bit only", func(t *testing.T) string {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, valid, 0o602)
			return dir
		}},
		{"group writable directory", func(t *testing.T) string {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, valid, 0o600)
			if err := os.Chmod(dir, 0o770); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{"world writable directory", func(t *testing.T) string {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, valid, 0o600)
			if err := os.Chmod(dir, 0o707); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{"directory is a symlink", func(t *testing.T) string {
			real := discPrivateDir(t)
			discWriteCache(t, real, valid, 0o600)
			link := filepath.Join(t.TempDir(), "state")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			return link
		}},
		{"a directory where the file should be", func(t *testing.T) string {
			dir := discPrivateDir(t)
			if err := os.Mkdir(filepath.Join(dir, DiscoveryFileName), 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			if s, ok := discSnapshotFrom(dir); ok {
				t.Errorf("an untrusted cache was used: %+v", s)
			}
		})
	}
}

func TestDiscoveryCache_MissingIsNotAnError(t *testing.T) {
	if s, ok := discSnapshotFrom(discPrivateDir(t)); ok {
		t.Errorf("snapshot from an empty directory: %+v", s)
	}
	if s, ok := discSnapshotFrom(filepath.Join(t.TempDir(), "no", "such", "dir")); ok {
		t.Errorf("snapshot from a missing directory: %+v", s)
	}
	if s, ok := discSnapshotFrom(""); ok {
		t.Errorf("snapshot with no state directory: %+v", s)
	}
}

func TestDiscoveryCache_OversizedIsIgnored(t *testing.T) {
	valid, err := json.Marshal(discCacheOf(discT0.Add(-time.Hour), "model-a"))
	if err != nil {
		t.Fatal(err)
	}
	pad := func(n int) []byte { // valid JSON, trailing whitespace is allowed
		return append(append([]byte{}, valid...), []byte(strings.Repeat(" ", n-len(valid)))...)
	}
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"exactly the limit", pad(maxCacheBytes), true},
		{"one byte over", pad(maxCacheBytes + 1), false},
		{"far over", pad(3 * maxCacheBytes), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := discPrivateDir(t)
			discWriteCacheBytes(t, dir, tc.data, 0o600)
			if _, ok := discSnapshotFrom(dir); ok != tc.want {
				t.Errorf("usable = %v, want %v for a %d byte file", ok, tc.want, len(tc.data))
			}
		})
	}
}

func TestDiscoveryCache_CorruptAndWrongSchemaAreIgnored(t *testing.T) {
	ok := discCacheOf(discT0.Add(-time.Hour), "model-a")
	schema2 := ok
	schema2.Schema = 2
	schema0 := ok
	schema0.Schema = 0
	bodies := map[string][]byte{
		"empty file":      {},
		"not json":        []byte("this is not json"),
		"truncated json":  []byte(`{"schema":1,"harnesses":{"agy":{"models":[{"id":"a"`),
		"a json array":    []byte(`[]`),
		"null":            []byte(`null`),
		"harnesses null":  []byte(`{"schema":1,"harnesses":null}`),
		"models mistyped": []byte(`{"schema":1,"harnesses":{"agy":{"probed_at":"2026-10-06T11:00:00Z","models":"nope"}}}`),
		"time mistyped":   []byte(`{"schema":1,"harnesses":{"agy":{"probed_at":12,"models":[{"id":"a"}]}}}`),
	}
	for name, mk := range map[string]cacheFile{"schema 2": schema2, "schema missing": schema0} {
		data, _ := json.Marshal(mk)
		bodies[name] = data
	}
	for name, data := range bodies {
		t.Run(name, func(t *testing.T) {
			dir := discPrivateDir(t)
			discWriteCacheBytes(t, dir, data, 0o600)
			if s, got := discSnapshotFrom(dir); got {
				t.Errorf("a cache that cannot be read was used: %+v", s)
			}
		})
	}
	// Control: the same shape with schema 1 is accepted.
	dir := discPrivateDir(t)
	discWriteCache(t, dir, ok, 0o600)
	if _, got := discSnapshotFrom(dir); !got {
		t.Error("control: a good cache was refused")
	}
}

// The file is read back as hard as a fresh listing: it is outside the process's
// control (another process, a hand edit, a bug).
func TestDiscoveryCache_IDsAreRevalidatedOnRead(t *testing.T) {
	f := discCacheOf(discT0.Add(-time.Hour))
	f.Harnesses[agyHarness] = cacheHarness{Source: "agy models", ProbedAt: discT0.Add(-time.Hour), Models: []DiscoveredModel{
		{ID: "../x", Name: "traversal"},
		{ID: "UPPER", Name: "upper"},
		{ID: "-flag", Name: "dash"},
		{ID: "a b", Name: "space"},
		{ID: "semi;colon", Name: "semicolon"},
		{ID: strings.Repeat("a", 65), Name: "long"},
		{ID: "", Name: "empty"},
		{ID: "ok-1", Name: "\x1b[31mRed\x1b[0m\u0000 name   with    gaps"},
		{ID: "ok-1", Name: "duplicate keeps the first"},
		{ID: "ok-2", Name: strings.Repeat("n", 500)},
	}}
	dir := discPrivateDir(t)
	discWriteCache(t, dir, f, 0o600)
	s, ok := discSnapshotFrom(dir)
	if !ok {
		t.Fatal("the valid ids should have made a snapshot")
	}
	if got := discIDs(s); !reflect.DeepEqual(got, []string{"ok-1", "ok-2"}) {
		t.Errorf("ids = %q, want [ok-1 ok-2]", got)
	}
	if s.Models[0].Name != "Red name with gaps" {
		t.Errorf("name = %q, want it sanitized on read", s.Models[0].Name)
	}
	if n := len([]rune(s.Models[1].Name)); n != maxNameRunes {
		t.Errorf("name has %d runes, want it cut to %d", n, maxNameRunes)
	}
}

func TestDiscoveryCache_NumberOfModelsIsCappedOnRead(t *testing.T) {
	ids := make([]string, 600)
	for i := range ids {
		ids[i] = fmt.Sprintf("model-%03d", i)
	}
	dir := discPrivateDir(t)
	discWriteCache(t, dir, discCacheOf(discT0.Add(-time.Hour), ids...), 0o600)
	s, ok := discSnapshotFrom(dir)
	if !ok || len(s.Models) != maxListedModels {
		t.Errorf("got %d models (ok=%v), want %d", len(s.Models), ok, maxListedModels)
	}
}

func TestDiscoveryCache_SnapshotWithNoUsableIDIsDropped(t *testing.T) {
	dir := discPrivateDir(t)
	discWriteCache(t, dir, discCacheOf(discT0.Add(-time.Hour), "../x", "UP PER"), 0o600)
	if s, ok := discSnapshotFrom(dir); ok {
		t.Errorf("a snapshot with no valid id was used: %+v", s)
	}
}

func TestDiscoveryCache_TimestampsAreChecked(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"an hour ago", discT0.Add(-time.Hour), true},
		{"a year ago is stale, not invalid", discT0.Add(-365 * 24 * time.Hour), true},
		{"a minute in the future is clock skew", discT0.Add(time.Minute), true},
		{"the year 2099 would stay fresh forever", time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"ten minutes ahead", discT0.Add(10 * time.Minute), false},
		{"the zero time", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := discPrivateDir(t)
			discWriteCache(t, dir, discCacheOf(tc.at, "model-a"), 0o600)
			if _, ok := discSnapshotFrom(dir); ok != tc.want {
				t.Errorf("usable = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestDiscoveryCache_OnlyAgyIsRead(t *testing.T) {
	f := discCacheOf(discT0.Add(-time.Hour), "model-a")
	f.Harnesses["claude"] = cacheHarness{Source: "claude", ProbedAt: discT0.Add(-time.Hour), Models: []DiscoveredModel{{ID: "opus"}}}
	f.Harnesses["../../etc"] = cacheHarness{Source: "x", ProbedAt: discT0.Add(-time.Hour), Models: []DiscoveredModel{{ID: "evil"}}}
	dir := discPrivateDir(t)
	discWriteCache(t, dir, f, 0o600)
	d := NewDiscoverer(DiscovererConfig{StateDir: dir, Now: func() time.Time { return discT0 }})
	if s, ok := d.Snapshot("agy"); !ok || !reflect.DeepEqual(discIDs(s), []string{"model-a"}) {
		t.Errorf("agy: %+v %v", s, ok)
	}
	for _, h := range []string{"claude", "codex", "../../etc"} {
		if s, ok := d.Snapshot(h); ok {
			t.Errorf("Snapshot(%q) = %+v, want none", h, s)
		}
	}
	// And the reader itself keeps only the harness that has a listing wired in,
	// whatever the file names.
	if got := readCacheFile(dir, discT0); len(got) != 1 || len(got[agyHarness].Models) != 1 {
		t.Errorf("readCacheFile returned %d harnesses, want only agy", len(got))
	}
}

func TestDiscoveryCache_SourceComesFromTheProgramNotTheFile(t *testing.T) {
	f := discCacheOf(discT0.Add(-time.Hour), "model-a")
	h := f.Harnesses[agyHarness]
	h.Source = "<script>alert(1)</script>\x1b[31m"
	f.Harnesses[agyHarness] = h
	dir := discPrivateDir(t)
	discWriteCache(t, dir, f, 0o600)
	s, ok := discSnapshotFrom(dir)
	if !ok || s.Source != "agy models" || s.Harness != "agy" {
		t.Errorf("Source = %q, Harness = %q (ok=%v), want the package's own constants", s.Source, s.Harness, ok)
	}
}

// ---- writing ----------------------------------------------------------------------

func TestDiscoveryCache_WrittenPrivately(t *testing.T) {
	discSkipPosixOnly(t)
	rig := newDiscRig(t)
	if err := os.Chmod(rig.stateDir, 0o755); err != nil { // as a bash-created state directory is
		t.Fatal(err)
	}
	// A permissive file already there must not decide the new file's mode.
	discWriteCache(t, rig.stateDir, discCacheOf(discT0, "old-1"), 0o644)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(rig.stateDir, DiscoveryFileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("cache mode = %o, want 600", fi.Mode().Perm())
	}
	di, _ := os.Stat(rig.stateDir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("state directory mode = %o, want 700 (statepath.SecureDir tightens it)", di.Mode().Perm())
	}
}

// A symlink planted at the cache path is replaced by the rename, never written
// through: the file it points at is untouched.
func TestDiscoveryCache_WriteReplacesAPlantedSymlink(t *testing.T) {
	discSkipPosixOnly(t)
	rig := newDiscRig(t)
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("SENTINEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(rig.stateDir, DiscoveryFileName)
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated || len(rep.Warnings) != 0 {
		t.Fatalf("probe: %q %v %v", rep.Status, rep.Warnings, err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "SENTINEL" {
		t.Errorf("the write went through the symlink: victim now holds %q", got)
	}
	fi, err := os.Lstat(link)
	if err != nil || !fi.Mode().IsRegular() {
		t.Errorf("the planted symlink must have been replaced by a regular file: %v %v", fi, err)
	}
	if _, ok := rig.another().Snapshot("agy"); !ok {
		t.Error("the replaced file should be a readable cache")
	}
}

// A state directory that is a symlink is refused for writing too, not followed.
func TestDiscoveryCache_WriteRefusesASymlinkedStateDir(t *testing.T) {
	discSkipPosixOnly(t)
	real := discPrivateDir(t)
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = link })
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("probe: %q %v", rep.Status, err)
	}
	if len(rep.Warnings) != 1 || !strings.HasPrefix(rep.Warnings[0], "cache not written: ") {
		t.Errorf("Warnings = %q, want a cache-not-written warning", rep.Warnings)
	}
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Errorf("the cache was written through the symlink into %s: %v", real, entries)
	}
}

func TestDiscoveryCache_WriteFailureKeepsTheMemorySnapshot(t *testing.T) {
	discSkipPosixOnly(t)
	notADir := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = notADir })
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated {
		t.Fatalf("a cache that cannot be written must not fail the probe: %q %v", rep.Status, err)
	}
	if len(rep.Warnings) != 1 || !strings.HasPrefix(rep.Warnings[0], "cache not written: ") {
		t.Errorf("Warnings = %q", rep.Warnings)
	}
	if s, ok := rig.d.Snapshot("agy"); !ok || len(s.Models) != 3 {
		t.Errorf("the in-memory snapshot must stay in effect: %+v %v", s, ok)
	}
}

func TestDiscoveryCache_NoStateDirMeansNoFiles(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = "" })
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeUpdated || len(rep.Warnings) != 0 {
		t.Fatalf("probe: %q %v %v", rep.Status, rep.Warnings, err)
	}
	if _, ok := rig.d.Snapshot("agy"); !ok {
		t.Error("memory-only mode must still keep the snapshot")
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("a Discoverer with no state directory wrote %d file(s) into the working directory", len(entries))
	}
	if _, ok := rig.another().Snapshot("agy"); ok {
		t.Error("memory-only mode must not share through a directory")
	}
}

func TestDiscoveryCache_NoTempFileIsLeftBehind(t *testing.T) {
	discSkipPosixOnly(t)
	rig := newDiscRig(t)
	// A directory at the target path makes the final rename fail.
	if err := os.Mkdir(filepath.Join(rig.stateDir, DiscoveryFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rig.stateDir, DiscoveryFileName, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || len(rep.Warnings) != 1 {
		t.Fatalf("want a warning and no error: %q %v", rep.Warnings, err)
	}
	entries, _ := os.ReadDir(rig.stateDir)
	for _, e := range entries {
		if e.Name() != DiscoveryFileName {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}
