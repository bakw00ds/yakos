package supervisorstream_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// bashLiteralKeys parses every single-line jq object literal passed to ho_log
// in the bash twin and returns the key lists in source order. These literals
// are the ground truth for the extras' names and order.
func bashLiteralKeys(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "lib", "hooks", "legacy", "supervisor-stream.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lit := regexp.MustCompile(`'\{([a-z_]+: [^'\n]*)\}'`)
	key := regexp.MustCompile(`(?:^|, )([a-z_]+): `)
	var out [][]string
	for _, m := range lit.FindAllStringSubmatch(string(data), -1) {
		if strings.HasPrefix(m[1], "ts:") {
			continue // synthetic findings, not log records
		}
		var keys []string
		for _, k := range key.FindAllStringSubmatch(m[1], -1) {
			keys = append(keys, k[1])
		}
		out = append(out, keys)
	}
	return out
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// TestLogExtraOrderMatchesBashLiterals: every bash ho_log extras literal lists
// its keys in an order the Go list reproduces, so Go emits the same bytes.
func TestLogExtraOrderMatchesBashLiterals(t *testing.T) {
	order := supervisorstream.LogExtraOrderForTest()
	lits := bashLiteralKeys(t)
	if len(lits) < 20 {
		t.Fatalf("parsed only %d bash literals; the parser is stale", len(lits))
	}
	for _, keys := range lits {
		last := -1
		for _, k := range keys {
			i := indexOf(order, k)
			if i < 0 {
				t.Errorf("bash key %q (in %v) missing from the Go order list", k, keys)
				continue
			}
			if i <= last {
				t.Errorf("bash order %v is not reproduced by the Go list", keys)
			}
			last = i
		}
	}
}

var baseKeys = []string{"ts", "hook", "severity", "decision", "reason", "agent", "session_id", "event"}

// recordShapeProblems validates one raw hook-log line: base fields first in
// bash's order, no legacy action/message, and every extra key one bash writes,
// in the order bash writes it.
func recordShapeProblems(t *testing.T, line string) []string {
	t.Helper()
	keys := rawKeys(line)
	if len(keys) < len(baseKeys) {
		return []string{"too few fields"}
	}
	for i, k := range baseKeys {
		if keys[i] != k {
			return []string{"base field " + k + " missing or out of order"}
		}
	}
	known := map[string]bool{}
	for _, lit := range bashLiteralKeys(t) {
		for _, k := range lit {
			known[k] = true
		}
	}
	order := supervisorstream.LogExtraOrderForTest()
	var probs []string
	last := -1
	for _, k := range keys[len(baseKeys):] {
		if !known[k] {
			probs = append(probs, "extra field "+k+" is not one bash writes")
		}
		if i := indexOf(order, k); i <= last {
			probs = append(probs, "extra field "+k+" out of bash order")
		} else {
			last = i
		}
	}
	return probs
}

// checkRecordShape fails t on any shape problem. logMessages runs it on every
// record the gate, budget, coalesce, ceiling, backoff and deferred tests read,
// so a renamed or reordered field fails those tests.
func checkRecordShape(t *testing.T, line string) {
	t.Helper()
	for _, p := range recordShapeProblems(t, line) {
		t.Errorf("%s: %s", p, line)
	}
}

// rawKeys returns the top-level keys of a flat JSON object line in byte order.
func rawKeys(line string) []string {
	var keys []string
	depth, inStr, esc := 0, false, false
	start := -1
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
				if depth == 1 && start >= 0 && i+1 < len(line) && line[i+1] == ':' {
					keys = append(keys, line[start+1:i])
				}
				start = -1
			}
			continue
		}
		switch c {
		case '"':
			inStr, start = true, i
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return keys
}

func TestCheckRecordShapeRejectsRenamedAndReordered(t *testing.T) {
	base := `{"ts":"x","hook":"h","severity":"WARN","decision":"pass","reason":"r","agent":"a","session_id":"s","event":"e"`
	if p := recordShapeProblems(t, base+`,"agent":"supervisor","spent_usd":1,"limit_usd":2,"budget_reason":"budget_warning"}`); len(p) != 0 {
		t.Fatalf("good record rejected: %v", p)
	}
	for name, bad := range map[string]string{
		"renamed":     base + `,"agent":"supervisor","spent_usd":1,"limit_usd":2,"budget_code":"budget_warning"}`,
		"reordered":   base + `,"budget_reason":"budget_warning","ceiling_usd":3,"limit_usd":2,"spent_usd":1}`,
		"legacy name": `{"ts":"x","hook":"h","severity":"WARN","action":"pass","message":"r"}`,
	} {
		if len(recordShapeProblems(t, bad)) == 0 {
			t.Errorf("%s record accepted", name)
		}
	}
	if got := rawKeys(`{"a":"x,\"b\":1","b":{"c":1},"d":[1]}`); strings.Join(got, ",") != "a,b,d" {
		t.Fatalf("rawKeys = %v", got)
	}
}

// TestRiskLabelsMatchBash: the logged trigger spells each built-in risk pattern
// exactly as bash's default_patterns array does, in the same order.
func TestRiskLabelsMatchBash(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "lib", "hooks", "legacy", "supervisor-stream.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, "default_patterns=(")
	end := strings.Index(src[start:], "\n        )\n")
	var want []string
	for _, l := range strings.Split(src[start:start+end], "\n")[1:] {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		v := l[1 : len(l)-1]
		if l[0] == '"' {
			v = strings.NewReplacer("${_ss_bt}", "`", `\$`, "$", `\"`, `"`).Replace(v)
		}
		want = append(want, v)
	}
	got, n := supervisorstream.RiskLabelsForTest()
	if len(got) != n || len(got) != len(want) {
		t.Fatalf("labels=%d patterns=%d bash=%d", len(got), n, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label %d = %q, bash %q", i, got[i], want[i])
		}
	}
}

// TestWrapperLogOrderMatchesBash: the detached wrapper's records list extras in
// the order bash's _ssw_log call sites write them.
func TestWrapperLogOrderMatchesBash(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "lib", "hooks", "legacy", "supervisor-stream.sh"))
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`_ssw_log (?:WARN|REPORT) "[^"]*" "((?:\\"[a-z_]+\\":[^,"]*,?)+)"`)
	key := regexp.MustCompile(`\\"([a-z_]+)\\":`)
	order := supervisorstream.WrapLogOrderForTest()
	n := 0
	for _, m := range call.FindAllStringSubmatch(string(data), -1) {
		n++
		last := -1
		for _, k := range key.FindAllStringSubmatch(m[1], -1) {
			i := indexOf(order, k[1])
			if i < 0 || i <= last {
				t.Errorf("wrapper key %q (in %s) missing or out of order", k[1], m[1])
			}
			last = i
		}
	}
	if n < 6 {
		t.Fatalf("parsed only %d _ssw_log extras; the parser is stale", n)
	}
}
