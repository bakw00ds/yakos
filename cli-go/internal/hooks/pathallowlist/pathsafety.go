package pathallowlist

import (
	"os"
	"path/filepath"
	"strings"
)

// lexicalNormalize mirrors ps_lexical_normalize: collapse "." segments,
// drop empty segments (duplicate and leading "/"), and resolve ".."
// against a preceding segment without touching the filesystem. A ".."
// that cannot be resolved survives at the FRONT of the result; callers
// detect an escape with escapesRoot.
//
// One deliberate difference from the bash helper: bash's `read -r -a`
// only reads the FIRST LINE of its input, so a path containing a newline
// is silently truncated there before normalization. This port splits the
// whole string on "/" (a newline is an ordinary filename character), which
// is what the operating system does with such a path.
func lexicalNormalize(p string) string {
	var out []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			if n := len(out); n > 0 && out[n-1] != ".." {
				out = out[:n-1]
			} else {
				out = append(out, "..")
			}
		default:
			out = append(out, seg)
		}
	}
	return strings.Join(out, "/")
}

// escapesRoot mirrors ps_escapes_root.
func escapesRoot(normalized string) bool {
	return normalized == ".." || strings.HasPrefix(normalized, "../")
}

// maxSymlinkHops bounds symlink chasing (matches the bash fallback's 40).
const maxSymlinkHops = 40

// realpathM resolves p to an absolute, symlink-free path WITHOUT requiring
// it to exist — the semantics of `realpath -m` / python's os.path.realpath:
// components are processed left to right, each existing symlink is
// replaced by its target (relative targets resolved against the link's own
// directory), and ".." pops the ALREADY-RESOLVED prefix. That last point is
// what makes "api/link/../x" resolve to the parent of link's TARGET, which
// is where the operating system will actually send the write — unlike a
// purely lexical collapse, which would produce "api/x".
//
// ok is false when the symlink chain is too deep or cyclic; callers must
// treat that as an escape (fail closed).
func realpathM(p string) (string, bool) {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		p = filepath.Join(wd, p)
	}
	vol := filepath.VolumeName(p)
	rest := filepath.ToSlash(p[len(vol):])
	root := vol + "/"

	queue := reverse(strings.Split(rest, "/"))
	cur := root
	hops := 0
	for len(queue) > 0 {
		c := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = parentOf(cur, root)
			continue
		}
		next := joinSlash(cur, c)
		fi, err := os.Lstat(next)
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return "", false
			}
			target, err := os.Readlink(next)
			if err != nil {
				return "", false
			}
			target = filepath.ToSlash(target)
			if strings.HasPrefix(target, "/") || filepath.VolumeName(target) != "" {
				cur = root
				if v := filepath.VolumeName(target); v != "" {
					root = v + "/"
					cur = root
					target = target[len(v):]
				}
			}
			queue = append(queue, reverse(strings.Split(target, "/"))...)
			continue
		}
		cur = next
	}
	return cur, true
}

func reverse(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[len(in)-1-i] = s
	}
	return out
}

func joinSlash(dir, name string) string {
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

func parentOf(cur, root string) string {
	if cur == root {
		return root
	}
	i := strings.LastIndexByte(cur, '/')
	if i < 0 {
		return root
	}
	parent := cur[:i]
	if len(parent) < len(root) {
		return root
	}
	return parent
}

// isWithin mirrors ps_is_within: path is root itself or a descendant.
// Pure string comparison — resolve both sides with realpathM first.
func isWithin(root, path string) bool {
	root = strings.TrimSuffix(root, "/")
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+"/")
}
