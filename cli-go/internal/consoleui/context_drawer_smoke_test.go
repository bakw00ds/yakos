package consoleui

import (
	"os"
	"os/exec"
	"regexp"
	"testing"
)

func TestContextDrawerSmoke(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	out, err := exec.Command(node, "testdata/context_drawer_smoke.js", "dist/context-drawer.js").CombinedOutput()
	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, out)
	}
}

// The drawer builds every node with textContent; no markup sink may appear.
func TestContextDrawerHasNoMarkupSink(t *testing.T) {
	src, err := os.ReadFile("dist/context-drawer.js")
	if err != nil {
		t.Fatal(err)
	}
	if re := regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function`); re.Match(src) {
		t.Fatalf("markup sink in context-drawer.js: %s", re.Find(src))
	}
}
