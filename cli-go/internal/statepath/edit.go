package statepath

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// EditResult is what EditYAML did. The shas are the hex SHA-256 of the file's
// bytes (the definition routerpolicy.File.SHA uses), "" for a file that did not
// exist.
type EditResult struct {
	SHABefore string
	SHAAfter  string
	// Changed is false when the edit left the bytes as they were; nothing was
	// written then.
	Changed bool
}

// ErrBusy is returned when another process holds the edit lock too long.
var ErrBusy = errors.New("statepath: the file is being edited by another process")

// editLockWait and editLockStale are variables so a test can shorten them.
var (
	editLockWait  = 2 * time.Second
	editLockStale = 30 * time.Second
)

// EditYAML is the one writer of the owner-only policy files that loosen a
// default (the router policy, the model registry overlay). It reads the file
// through ReadTrusted, so a file or directory another user could have written
// is refused rather than overwritten, hands its top-level mapping to edit,
// re-encodes it (keys edit did not touch, and their comments, are kept), gives
// the result to check, and replaces the file with a 0600 temporary file and a
// rename. A concurrent EditYAML in another process waits on a lock file; a
// reader never sees a partial file.
//
// A missing file is an empty mapping. check may be nil. When check refuses, or
// edit fails, the file is untouched. The returned errors name no path.
func EditYAML(path string, max int64, edit func(top *yaml.Node) error, check func(data []byte) error) (EditResult, error) {
	var res EditResult
	dir := filepath.Dir(path)
	if err := SecureDir(dir); err != nil {
		return res, errors.New("statepath: the state directory is not safe to write")
	}
	unlock, err := lockEdit(path + ".lock")
	if err != nil {
		return res, err
	}
	defer unlock()

	old, rerr := ReadTrusted(path, max+1)
	switch {
	case rerr == nil:
		if int64(len(old)) > max {
			return res, errors.New("statepath: the file is too large to edit")
		}
		sum := sha256.Sum256(old)
		res.SHABefore = hex.EncodeToString(sum[:])
	case errors.Is(rerr, fs.ErrNotExist):
		old = nil
	case IsUntrusted(rerr):
		return res, errors.New("statepath: the file is not trusted (a symlink, another user's, or group or world writable); refusing to write it")
	default:
		return res, errors.New("statepath: the file could not be read")
	}

	var doc yaml.Node
	if len(bytes.TrimSpace(old)) > 0 {
		if err := yaml.Unmarshal(old, &doc); err != nil {
			return res, errors.New("statepath: the file is not valid YAML; fix it by hand first")
		}
	}
	if doc.Kind == 0 || (doc.Kind == yaml.DocumentNode && len(doc.Content) == 0) {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return res, errors.New("statepath: the top level of the file must be a mapping")
	}
	top := doc.Content[0]
	if top.Kind == yaml.ScalarNode && top.Tag == "!!null" {
		*top = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if top.Kind != yaml.MappingNode {
		return res, errors.New("statepath: the top level of the file must be a mapping")
	}
	if err := edit(top); err != nil {
		return res, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return res, errors.New("statepath: cannot encode the result")
	}
	_ = enc.Close()
	data := buf.Bytes()
	if int64(len(data)) > max {
		return res, errors.New("statepath: the result would be too large")
	}
	if check != nil {
		if err := check(data); err != nil {
			return res, err
		}
	}
	sum := sha256.Sum256(data)
	res.SHAAfter = hex.EncodeToString(sum[:])
	if bytes.Equal(old, data) {
		res.SHAAfter = res.SHABefore
		return res, nil
	}
	if err := writeAtomic(dir, path, data); err != nil {
		return res, errors.New("statepath: the file could not be written")
	}
	res.Changed = true
	return res, nil
}

// writeAtomic replaces path with data via a 0600 temporary file in the same
// directory and a rename. The temporary file is created exclusively, checked
// through its descriptor, synced and closed before the rename.
func writeAtomic(dir, path string, data []byte) error {
	f, err := os.CreateTemp(dir, ".edit-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmp)
		}
	}()
	if err := SecureFile(f); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	done = true
	return nil
}

// lockEdit takes the sibling lock file with O_EXCL, waiting up to editLockWait
// and breaking a lock older than editLockStale (its holder died).
func lockEdit(lock string) (func(), error) {
	deadline := time.Now().Add(editLockWait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // state dir, checked above
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, errors.New("statepath: cannot take the edit lock")
		}
		if fi, serr := os.Lstat(lock); serr == nil && (time.Since(fi.ModTime()) > editLockStale || fi.Mode()&os.ModeSymlink != 0) {
			_ = os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, ErrBusy
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// YAMLGet returns the value node of key in mapping m, or nil.
func YAMLGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// YAMLSet sets key in mapping m to v, replacing an existing value in place or
// appending the pair.
func YAMLSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

// YAMLDelete removes key from mapping m.
func YAMLDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// YAMLMap returns the mapping at key in m, creating (and attaching) an empty one
// when the key is absent or null. ok is false when the key holds something that
// is not a mapping.
func YAMLMap(m *yaml.Node, key string) (n *yaml.Node, ok bool) {
	n = YAMLGet(m, key)
	if n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		n = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		YAMLSet(m, key, n)
		return n, true
	}
	return n, n.Kind == yaml.MappingNode
}

// String describes an EditResult for logs: the short shas, never a path.
func (r EditResult) String() string {
	short := func(s string) string {
		if s == "" {
			return "none"
		}
		return s[:8]
	}
	return fmt.Sprintf("%s -> %s", short(r.SHABefore), short(r.SHAAfter))
}
