//go:build !windows

package start

// pump_resize_test.go — unit tests for clampDims (Fix 2 — LOW, ADR-0008 P2
// security review).
//
// Tests:
//  1. TestResizeClamp_ZeroZero   — cols=0, rows=0   → 80×24
//  2. TestResizeClamp_Oversized  — cols=9999, rows=9999 → 1000×1000
//  3. TestResizeClamp_Normal     — cols=120, rows=40  → 120×40 (pass-through)

import "testing"

func TestResizeClamp_ZeroZero(t *testing.T) {
	cols, rows := clampDims(0, 0)
	if cols != 80 || rows != 24 {
		t.Errorf("clampDims(0, 0) = (%d, %d); want (80, 24)", cols, rows)
	}
}

func TestResizeClamp_Oversized(t *testing.T) {
	cols, rows := clampDims(9999, 9999)
	if cols != 1000 || rows != 1000 {
		t.Errorf("clampDims(9999, 9999) = (%d, %d); want (1000, 1000)", cols, rows)
	}
}

func TestResizeClamp_Normal(t *testing.T) {
	cols, rows := clampDims(120, 40)
	if cols != 120 || rows != 40 {
		t.Errorf("clampDims(120, 40) = (%d, %d); want (120, 40)", cols, rows)
	}
}

// Additional boundary cases.

func TestResizeClamp_ZeroCols(t *testing.T) {
	cols, rows := clampDims(0, 40)
	if cols != 80 || rows != 40 {
		t.Errorf("clampDims(0, 40) = (%d, %d); want (80, 40)", cols, rows)
	}
}

func TestResizeClamp_ZeroRows(t *testing.T) {
	cols, rows := clampDims(120, 0)
	if cols != 120 || rows != 24 {
		t.Errorf("clampDims(120, 0) = (%d, %d); want (120, 24)", cols, rows)
	}
}

func TestResizeClamp_ExactlyAtLimit(t *testing.T) {
	cols, rows := clampDims(1000, 1000)
	if cols != 1000 || rows != 1000 {
		t.Errorf("clampDims(1000, 1000) = (%d, %d); want (1000, 1000)", cols, rows)
	}
}

func TestResizeClamp_OneOverLimit(t *testing.T) {
	cols, rows := clampDims(1001, 1001)
	if cols != 1000 || rows != 1000 {
		t.Errorf("clampDims(1001, 1001) = (%d, %d); want (1000, 1000)", cols, rows)
	}
}
