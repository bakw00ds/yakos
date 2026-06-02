// Package deploydrift detects drift between framework-supplied hook scripts
// and their installed counterparts.
//
// YakOS writes a .framework-hash sidecar file alongside each hook it installs.
// The sidecar contains the SHA-256 hex digest of the framework source at
// install time. Doctor and refresh both read these sidecars to surface drift.
//
// This package is shared between the doctor (rank 5) and refresh (rank 6) ports
// so that drift-detection logic is not duplicated.
package deploydrift

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HookState describes the drift state of a single hook file.
type HookState int

const (
	// HookClean means the installed hook matches the framework source hash.
	HookClean HookState = iota
	// HookDrifted means the installed hook has been modified since install.
	HookDrifted
	// HookUnhashed means no .framework-hash sidecar exists for this file.
	HookUnhashed
)

// HookResult holds per-file drift information.
type HookResult struct {
	// Path is the absolute path to the hook file.
	Path string
	// RelPath is the path relative to the project directory (for display).
	RelPath string
	// State describes whether the hook is clean, drifted, or unhashed.
	State HookState
	// ExpectedHash is the hash stored in the .framework-hash sidecar.
	// Empty when State == HookUnhashed.
	ExpectedHash string
	// ActualHash is the SHA-256 of the current file contents.
	ActualHash string
}

// DriftReport summarizes the drift state of all hooks in a directory.
type DriftReport struct {
	Dir      string
	Results  []HookResult
	Clean    int
	Drifted  int
	Unhashed int
}

// CheckDir inspects all hook files under hooksDir (skipping .framework-hash,
// .gitkeep, and README.md) and returns a DriftReport.
//
// projectDir is the project root used to compute RelPath values for display.
// If projectDir is empty, RelPath equals Path.
func CheckDir(hooksDir, projectDir string) (*DriftReport, error) {
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return nil, fmt.Errorf("deploydrift: reading hooks dir %s: %w", hooksDir, err)
	}

	report := &DriftReport{Dir: hooksDir}
	var results []HookResult

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip sidecar files and non-hook files.
		if strings.HasSuffix(name, ".framework-hash") ||
			name == ".gitkeep" ||
			name == "README.md" {
			continue
		}

		absPath := filepath.Join(hooksDir, name)
		relPath := absPath
		if projectDir != "" {
			if rel, err := filepath.Rel(projectDir, absPath); err == nil {
				relPath = rel
			}
		}

		hashFile := absPath + ".framework-hash"
		actualHash, err := sha256File(absPath)
		if err != nil {
			// Skip files we can't hash (permissions, etc.).
			continue
		}

		expectedHashBytes, err := os.ReadFile(hashFile) //nolint:gosec
		if err != nil {
			// No sidecar → unhashed.
			results = append(results, HookResult{
				Path:       absPath,
				RelPath:    relPath,
				State:      HookUnhashed,
				ActualHash: actualHash,
			})
			report.Unhashed++
			continue
		}

		expectedHash := strings.TrimSpace(string(expectedHashBytes))
		if expectedHash == actualHash {
			results = append(results, HookResult{
				Path:         absPath,
				RelPath:      relPath,
				State:        HookClean,
				ExpectedHash: expectedHash,
				ActualHash:   actualHash,
			})
			report.Clean++
		} else {
			results = append(results, HookResult{
				Path:         absPath,
				RelPath:      relPath,
				State:        HookDrifted,
				ExpectedHash: expectedHash,
				ActualHash:   actualHash,
			})
			report.Drifted++
		}
	}

	// Sort results by path for deterministic output (mirrors bash's `find | sort`).
	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})
	report.Results = results

	return report, nil
}

// CheckFile computes the drift state for a single installed file against a
// framework source file. Used by doctor's pre-push gate check and refresh's
// hook-refresh logic.
//
// installedPath is the deployed hook file.
// hashSiblingPath is the .framework-hash sidecar beside the installed file.
// frameworkSrcPath is the canonical source in the yakOS framework tree.
//
// Returns HookUnhashed when no sidecar exists, HookClean when the sidecar
// hash matches the framework source hash, HookDrifted otherwise.
func CheckFile(installedPath, hashSiblingPath, frameworkSrcPath string) (HookState, string, string, error) {
	// Read the sidecar hash (written at install time against the framework src).
	expectedHashBytes, err := os.ReadFile(hashSiblingPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return HookUnhashed, "", "", nil
		}
		return HookUnhashed, "", "", fmt.Errorf("deploydrift: reading hash sidecar %s: %w", hashSiblingPath, err)
	}
	expectedHash := strings.TrimSpace(string(expectedHashBytes))

	// Hash the current framework source (not the installed copy — the sidecar
	// was written against the framework src at install time, so we compare
	// framework-src hash against the sidecar to detect framework updates).
	frameworkHash, err := sha256File(frameworkSrcPath)
	if err != nil {
		return HookUnhashed, expectedHash, "", fmt.Errorf("deploydrift: hashing framework src %s: %w", frameworkSrcPath, err)
	}

	_ = installedPath // retained for signature symmetry with future --fix callers.

	if expectedHash == frameworkHash {
		return HookClean, expectedHash, frameworkHash, nil
	}
	return HookDrifted, expectedHash, frameworkHash, nil
}

// sha256File computes the lowercase hex SHA-256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// SHA256File is an exported wrapper around sha256File for use by other packages
// (e.g., refresh) that need to compute file hashes for sidecar updates.
func SHA256File(path string) (string, error) {
	return sha256File(path)
}
