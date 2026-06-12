// Package stackdetect detects the technology stack profile of a project
// directory by reading manifest files — never by executing project code.
//
// It mirrors the tooling-matrix table in
// lib/skills/release-audit/references/tooling-matrix.md so that metrics and
// release-audit agree on what a project "is".
//
// Walk depth is bounded (maxDepth) and vendor/build directories are skipped.
// Returns a sorted, deduplicated slice of Profile constants.
package stackdetect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Profile is a stack identifier string understood by the metrics analyzer
// registry.
type Profile string

const (
	ProfileGoBackend   Profile = "go-backend"
	ProfileNode        Profile = "node"
	ProfileRust        Profile = "rust"
	ProfilePython      Profile = "python"
	ProfileRuby        Profile = "ruby"
	ProfileJava        Profile = "java"
	ProfileDotNet      Profile = "dotnet"
	ProfileFlutter     Profile = "flutter"
	ProfileReactNative Profile = "react-native"
)

// maxDepth is the maximum directory depth walked from the project root.
// Keeps detection bounded on large monorepos.
const maxDepth = 4

// skipDirs are directory names that are always skipped during the walk.
var skipDirs = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	".git":         true,
	"target":       true, // Rust/Java build output
	"dist":         true,
	"build":        true,
	"_build":       true,
	".build":       true,
	"__pycache__":  true,
	".next":        true,
	".cache":       true,
}

// Detect walks projectDir up to maxDepth and returns the set of detected
// profiles, sorted and deduplicated. An empty slice means the stack is
// unknown or the directory is unreadable.
func Detect(projectDir string) []Profile {
	found := make(map[Profile]bool)
	_ = walkBounded(projectDir, projectDir, 0, found)
	profiles := make([]Profile, 0, len(found))
	for p := range found {
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i] < profiles[j]
	})
	return profiles
}

// walkBounded recursively walks dir, stopping at maxDepth or on unreadable
// entries. It populates found with any profiles detected by manifest files.
func walkBounded(root, dir string, depth int, found map[Profile]bool) error {
	if depth > maxDepth {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // unreadable directory is non-fatal
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if skipDirs[name] {
				continue
			}
			_ = walkBounded(root, filepath.Join(dir, name), depth+1, found)
			continue
		}
		detectFile(name, dir, found)
	}
	return nil
}

// detectFile maps individual manifest file names to profiles.
func detectFile(name, dir string, found map[Profile]bool) {
	switch name {
	case "go.mod":
		found[ProfileGoBackend] = true
	case "package.json":
		// Distinguish Flutter/React-Native from plain Node by checking deps.
		p := classifyPackageJSON(filepath.Join(dir, name))
		found[p] = true
	case "Cargo.toml":
		found[ProfileRust] = true
	case "pyproject.toml", "setup.py", "setup.cfg", "requirements.txt":
		found[ProfilePython] = true
	case "Gemfile":
		found[ProfileRuby] = true
	case "pom.xml", "build.gradle", "build.gradle.kts":
		found[ProfileJava] = true
	case "pubspec.yaml":
		found[ProfileFlutter] = true
	}

	// .csproj / .fsproj / .vbproj matched by suffix: os.ReadDir returns real
	// file names, never glob patterns, so suffix matching is the correct
	// approach. The old "*.csproj" case arm was dead code.
	if strings.HasSuffix(name, ".csproj") ||
		strings.HasSuffix(name, ".fsproj") ||
		strings.HasSuffix(name, ".vbproj") {
		found[ProfileDotNet] = true
	}
}

// classifyPackageJSON reads the package.json at path and returns the most
// specific Node-family profile: flutter > react-native > node.
// On any read/parse error it falls back to ProfileNode.
func classifyPackageJSON(path string) Profile {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ProfileNode
	}
	s := string(data)
	// React Native uses "react-native" in deps.
	if strings.Contains(s, `"react-native"`) {
		return ProfileReactNative
	}
	return ProfileNode
}

// Has returns true if profiles contains p.
func Has(profiles []Profile, p Profile) bool {
	for _, q := range profiles {
		if q == p {
			return true
		}
	}
	return false
}
