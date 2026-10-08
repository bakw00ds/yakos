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

// The write controls (K-175) run against the same kind of fake DOM, with the
// stub server playing step-up, CSRF refusal and success.
func TestModelsWriteJSSmoke(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	out, err := exec.Command(node, "testdata/models_write_smoke.js", "dist/models.js", "dist/models_write.js").CombinedOutput()
	if err != nil {
		t.Fatalf("write smoke failed: %v\n%s", err, out)
	}
}

// models_write.js builds every node with textContent, has no markup sink, never
// touches storage (the step-up secret is sent once and cleared), and models.js
// itself still has no write path: it only hands the overview to the panel.
func TestModelsWriteJSHasNoMarkupSinkOrStorage(t *testing.T) {
	src, err := os.ReadFile("dist/models_write.js")
	if err != nil {
		t.Fatal(err)
	}
	if re := regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function|localStorage|sessionStorage|indexedDB|document\.cookie`); re.Match(src) {
		t.Fatalf("markup sink or storage in models_write.js: %s", re.Find(src))
	}
	if !regexp.MustCompile(`can_write`).MatchString(string(mustRead(t, "dist/models.js"))) {
		t.Error("models.js does not gate the panel on can_write")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
