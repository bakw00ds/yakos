// Package yamlblock reads the direct scalar children of one top-level-style
// block in a .yakos.yml WITHOUT parsing the file as YAML.
//
// The plan-quality hooks must agree with their bash twins on what the file
// says, and bash reads it with awk. A YAML parser disagrees the moment an
// unrelated key is malformed: it errors, the hook falls back to "no config",
// and a gate keeps enforcing a marker its bash twin has cleared. So both
// sides use this line-oriented reader, whose bash twin is
// hi_yaml_block_children in lib/hooks/lib/hook-input.sh. Keep them in step.
//
// Rules (identical on both sides):
//   - Blank lines and comment-only lines are ignored everywhere. A trailing
//     "\r" is dropped.
//   - The block starts at the first line whose text, after leading spaces or
//     tabs, is `<block>` followed by optional spaces and a ":". That line's
//     indent (count of leading space/tab characters) is the block indent.
//   - The block ends at the first later line whose indent is <= the block
//     indent. A sibling under a common parent therefore ends it.
//   - The first line inside the block fixes the child indent. Only lines at
//     exactly that indent are keys; deeper lines (a child map's own keys) and
//     shallower-but-inside lines are ignored, so a nested `enabled:` cannot
//     bleed up into the block.
//   - A key line is `<key>[ \t]*:[ \t]*<value>` with key in [A-Za-z0-9_.-]+.
//     The value has its trailing whitespace trimmed, then an inline
//     `[ \t]+#...` comment removed, then every ' and " deleted.
package yamlblock

import (
	"regexp"
	"strings"
)

// KV is one direct child of the block, in file order.
type KV struct {
	Key   string
	Value string
}

var (
	reKey           = regexp.MustCompile(`^([A-Za-z0-9_.-]+)[ \t]*:[ \t]*`)
	reInlineComment = regexp.MustCompile(`[ \t]+#.*$`)
)

func indentOf(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}

// Children returns the block's direct scalar children in file order. A missing
// block yields nil.
func Children(data []byte, block string) []KV {
	var out []KV
	inBlock := false
	blockIndent, childIndent := 0, -1
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		n := indentOf(line)
		if n == len(line) || line[n] == '#' { // blank or comment-only
			continue
		}
		rest := line[n:]
		if !inBlock {
			if strings.HasPrefix(rest, block) {
				r := strings.TrimLeft(rest[len(block):], " \t")
				if strings.HasPrefix(r, ":") {
					inBlock = true
					blockIndent = n
				}
			}
			continue
		}
		if n <= blockIndent {
			break
		}
		if childIndent < 0 {
			childIndent = n
		}
		if n != childIndent {
			continue
		}
		rest = strings.TrimRight(rest, " \t")
		rest = reInlineComment.ReplaceAllString(rest, "")
		m := reKey.FindStringSubmatch(rest)
		if m == nil {
			continue
		}
		v := strings.NewReplacer(`"`, "", `'`, "").Replace(rest[len(m[0]):])
		out = append(out, KV{Key: m[1], Value: v})
	}
	return out
}

// Last returns the value of key from the block's children the way the bash
// callers apply them: a later duplicate wins, and an empty value never
// overrides an earlier one (`[ -n "$v" ] && VAR="$v"`).
func Last(data []byte, block, key string) (string, bool) {
	val, ok := "", false
	for _, kv := range Children(data, block) {
		if kv.Key == key && kv.Value != "" {
			val, ok = kv.Value, true
		}
	}
	return val, ok
}
