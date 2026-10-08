package consoleui

import (
	"os"
	"os/exec"
	"regexp"
	"testing"
)

func TestModelsJSSmoke(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	out, err := exec.Command(node, "testdata/models_smoke.js", "dist/models.js").CombinedOutput()
	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, out)
	}
}

// The tab builds every node with textContent; no markup sink may appear, and it
// has no write path: no method other than GET, no request body.
func TestModelsJSHasNoMarkupSinkAndNoWrite(t *testing.T) {
	src, err := os.ReadFile("dist/models.js")
	if err != nil {
		t.Fatal(err)
	}
	if re := regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function`); re.Match(src) {
		t.Fatalf("markup sink in models.js: %s", re.Find(src))
	}
	if re := regexp.MustCompile(`'(PUT|POST|PATCH|DELETE)'|"(PUT|POST|PATCH|DELETE)"|JSON\.stringify|localStorage|sessionStorage`); re.Match(src) {
		t.Fatalf("models.js has a write path or stores data: %s", re.Find(src))
	}
}
