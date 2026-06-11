package stackdetect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect_GoBackend(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\n\ngo 1.21\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if !Has(profiles, ProfileGoBackend) {
		t.Errorf("expected go-backend profile; got %v", profiles)
	}
}

func TestDetect_Node(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"foo","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if !Has(profiles, ProfileNode) {
		t.Errorf("expected node profile; got %v", profiles)
	}
}

func TestDetect_ReactNative(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"name":"app","dependencies":{"react-native":"0.72.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if !Has(profiles, ProfileReactNative) {
		t.Errorf("expected react-native profile; got %v", profiles)
	}
	if Has(profiles, ProfileNode) {
		t.Errorf("should not also have node profile when react-native detected; got %v", profiles)
	}
}

func TestDetect_Rust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(`[package]\nname = "foo"\n`), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if !Has(profiles, ProfileRust) {
		t.Errorf("expected rust profile; got %v", profiles)
	}
}

func TestDetect_Flutter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: myapp\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if !Has(profiles, ProfileFlutter) {
		t.Errorf("expected flutter profile; got %v", profiles)
	}
}

func TestDetect_Nested(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "backend")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "go.mod"), []byte("module x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if !Has(profiles, ProfileGoBackend) {
		t.Errorf("expected go-backend from nested dir; got %v", profiles)
	}
}

func TestDetect_SkipsVendor(t *testing.T) {
	dir := t.TempDir()
	vendor := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendor, 0755); err != nil {
		t.Fatal(err)
	}
	// go.mod inside vendor should NOT trigger go-backend
	if err := os.WriteFile(filepath.Join(vendor, "go.mod"), []byte("module vendored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if Has(profiles, ProfileGoBackend) {
		t.Errorf("should not detect go-backend inside vendor/; got %v", profiles)
	}
}

func TestDetect_Empty(t *testing.T) {
	dir := t.TempDir()
	profiles := Detect(dir)
	if len(profiles) != 0 {
		t.Errorf("expected no profiles for empty dir; got %v", profiles)
	}
}

func TestDetect_DotNet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MyApp.csproj"), []byte("<Project />"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	if !Has(profiles, ProfileDotNet) {
		t.Errorf("expected dotnet profile; got %v", profiles)
	}
}

func TestDetect_Sorted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := Detect(dir)
	for i := 1; i < len(profiles); i++ {
		if profiles[i-1] > profiles[i] {
			t.Errorf("profiles not sorted: %v", profiles)
		}
	}
}
