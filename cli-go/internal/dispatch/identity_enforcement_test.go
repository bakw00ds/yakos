package dispatch

// identity_enforcement_test.go — Phase 6b tests for dispatch role enforcement
// and dual-regime operator_id.
//
// Coverage:
//  1. Service.Run: RoleRead identity → forbidden error.
//  2. Service.Run: RoleDispatch identity → passes enforcement.
//  3. Service.Run: Populated=false (loopback) → no enforcement, no error.
//  4. Service.Run: Authenticated identity → cert CN used, caller OperatorID ignored.
//  5. Service.Run: Unauthenticated identity → caller OperatorID preserved.
//  6. Service.RunStream: RoleRead identity → forbidden error.
//  7. Service.RunStream: Populated=false → no enforcement, passes.
//  8. Service.RunStream: Authenticated identity → cert CN used as operator_id.
//  9. Service.RunStream: Unauthenticated identity → caller OperatorID preserved.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// ---- Service.Run enforcement tests -------------------------------------------

// TestRun_InsufficientRole_Forbidden verifies that a resolved identity with
// RoleRead is rejected with a forbidden error before any dispatch fires.
func TestRun_InsufficientRole_Forbidden(t *testing.T) {
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     logDir, // doesn't matter; rejection happens before roster compose
		WorkspaceRoot: logDir,
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "any",
		Task:    "do something",
		Project: logDir,
		ResolvedIdentity: IdentityCarrier{
			Populated: true,
			Identity: netid.Identity{
				Role:       netid.RoleRead,
				Resolved:   true,
				OperatorID: "alice",
			},
		},
	})
	if err == nil {
		t.Fatal("expected forbidden error for RoleRead identity; got nil")
	}
	if !errors.Is(err, err) { // always true — just confirm it's non-nil
		t.Error("err should be non-nil")
	}
	// Error message must mention "forbidden" and not contain sensitive details.
	errMsg := err.Error()
	if len(errMsg) == 0 {
		t.Error("expected non-empty error message")
	}
}

// TestRun_SufficientRole_PassesEnforcement verifies that a resolved identity
// with RoleDispatch passes enforcement (no forbidden error).
func TestRun_SufficientRole_PassesEnforcement(t *testing.T) {
	logDir := isolatedLogDir(t)
	// Use a fake runFn so we don't need a real agent roster or LLM call.
	var capturedOperatorID string
	withRunFn(func(_ context.Context, req Request) ([]byte, Result, error) {
		capturedOperatorID = req.OperatorID
		return []byte("ok"), Result{ExitCode: 0}, nil
	}, func() {
		svc := NewService(ServiceConfig{
			YakosRoot:     logDir,
			WorkspaceRoot: logDir,
		})
		_, _, err := svc.Run(context.Background(), Params{
			Agent:   "any",
			Task:    "do something",
			Project: logDir,
			ResolvedIdentity: IdentityCarrier{
				Populated: true,
				Identity: netid.Identity{
					Role:       netid.RoleDispatch,
					Resolved:   true,
					OperatorID: "bob",
				},
			},
			OperatorID: "bob",
		})
		if err != nil {
			t.Errorf("expected no error for RoleDispatch identity; got %v", err)
		}
		_ = capturedOperatorID
	})
}

// TestRun_PopulatedFalse_NoEnforcement verifies that when Populated=false
// (loopback / unresolved path), no role enforcement fires.
func TestRun_PopulatedFalse_NoEnforcement(t *testing.T) {
	logDir := isolatedLogDir(t)
	withRunFn(func(_ context.Context, req Request) ([]byte, Result, error) {
		return []byte("ok"), Result{ExitCode: 0}, nil
	}, func() {
		svc := NewService(ServiceConfig{
			YakosRoot:     logDir,
			WorkspaceRoot: logDir,
			OperatorID:    "daemon-op",
		})
		_, _, err := svc.Run(context.Background(), Params{
			Agent:            "any",
			Task:             "do something",
			Project:          logDir,
			ResolvedIdentity: IdentityCarrier{Populated: false}, // loopback: no enforcement
		})
		if err != nil {
			t.Errorf("Populated=false should not gate; got error: %v", err)
		}
	})
}

// TestRun_AuthenticatedIdentity_OperatorID_UsedFromCertCN verifies that when
// the identity is Authenticated (mTLS cert path), the cert CN overrides the
// caller-supplied OperatorID stamped in Params.OperatorID.
func TestRun_AuthenticatedIdentity_OperatorID_UsedFromCertCN(t *testing.T) {
	logDir := isolatedLogDir(t)

	var capturedOperatorID string
	withRunFn(func(_ context.Context, req Request) ([]byte, Result, error) {
		capturedOperatorID = req.OperatorID
		return []byte("ok"), Result{ExitCode: 0}, nil
	}, func() {
		svc := NewService(ServiceConfig{
			YakosRoot:     logDir,
			WorkspaceRoot: logDir,
			OperatorID:    "daemon-default",
		})
		_, _, err := svc.Run(context.Background(), Params{
			Agent:      "any",
			Task:       "do something",
			Project:    logDir,
			OperatorID: "caller-supplied", // must be ignored when Authenticated=true
			ResolvedIdentity: IdentityCarrier{
				Populated: true,
				Identity: netid.Identity{
					Role:          netid.RoleAdmin,
					Authenticated: true,
					Resolved:      true,
					OperatorID:    "cert-cn-alice", // must win
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedOperatorID != "cert-cn-alice" {
			t.Errorf("operator_id in Request: got %q; want %q (cert CN must override caller-supplied)",
				capturedOperatorID, "cert-cn-alice")
		}
	})
}

// TestRun_UnauthenticatedIdentity_OperatorID_Preserved verifies that when
// the identity is Populated but NOT Authenticated (loopback bearer or
// non-loopback with no cert), the caller-supplied OperatorID is preserved.
func TestRun_UnauthenticatedIdentity_OperatorID_Preserved(t *testing.T) {
	logDir := isolatedLogDir(t)

	var capturedOperatorID string
	withRunFn(func(_ context.Context, req Request) ([]byte, Result, error) {
		capturedOperatorID = req.OperatorID
		return []byte("ok"), Result{ExitCode: 0}, nil
	}, func() {
		svc := NewService(ServiceConfig{
			YakosRoot:     logDir,
			WorkspaceRoot: logDir,
			OperatorID:    "daemon-default",
		})
		_, _, err := svc.Run(context.Background(), Params{
			Agent:      "any",
			Task:       "do something",
			Project:    logDir,
			OperatorID: "caller-op", // must be preserved when Authenticated=false
			ResolvedIdentity: IdentityCarrier{
				Populated: true,
				Identity: netid.Identity{
					Role:          netid.RoleAdmin,
					Authenticated: false, // unauthenticated — cooperative label path
					Resolved:      true,
					OperatorID:    "label-ignored", // unauthenticated: resolver OperatorID not used here
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedOperatorID != "caller-op" {
			t.Errorf("operator_id in Request: got %q; want %q (unauthenticated: caller OperatorID must be used)",
				capturedOperatorID, "caller-op")
		}
	})
}

// ---- Service.RunStream enforcement tests ------------------------------------

// TestRunStream_InsufficientRole_Forbidden verifies that RunStream rejects a
// RoleRead identity before any streaming dispatch fires.
func TestRunStream_InsufficientRole_Forbidden(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
	})

	_, err := svc.RunStream(context.Background(), Params{
		Agent:   "chat-agent",
		Task:    "hello",
		Project: logDir,
		ResolvedIdentity: IdentityCarrier{
			Populated: true,
			Identity: netid.Identity{
				Role:       netid.RoleRead,
				Resolved:   true,
				OperatorID: "alice",
			},
		},
	}, func(StreamChunk) {})

	if err == nil {
		t.Fatal("expected forbidden error for RoleRead identity in RunStream; got nil")
	}
}

// TestRunStream_PopulatedFalse_NoEnforcement verifies that RunStream with
// Populated=false (loopback path) skips enforcement entirely.
func TestRunStream_PopulatedFalse_NoEnforcement(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "daemon-op",
	})

	withStreamRunFn(func(_ context.Context, _ Request, _ runtime.Adapter, _ runtime.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		return Result{ExitCode: 0}, nil
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:            "chat-agent",
			Task:             "hello",
			Project:          logDir,
			ResolvedIdentity: IdentityCarrier{Populated: false},
		}, func(StreamChunk) {})
		if err != nil {
			t.Errorf("Populated=false should not gate; got error: %v", err)
		}
	})
}

// TestRunStream_AuthenticatedIdentity_OperatorID_UsedFromCertCN verifies that
// for an Authenticated identity, the cert CN overrides Params.OperatorID in
// the Request stamped on RunStream.
func TestRunStream_AuthenticatedIdentity_OperatorID_UsedFromCertCN(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "daemon-default",
	})

	var capturedOperatorID string
	withStreamRunFn(func(_ context.Context, req Request, _ runtime.Adapter, _ runtime.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		capturedOperatorID = req.OperatorID
		return Result{ExitCode: 0}, nil
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:      "chat-agent",
			Task:       "hello",
			Project:    logDir,
			OperatorID: "caller-supplied",
			ResolvedIdentity: IdentityCarrier{
				Populated: true,
				Identity: netid.Identity{
					Role:          netid.RoleAdmin,
					Authenticated: true,
					Resolved:      true,
					OperatorID:    "cert-cn-bob",
				},
			},
		}, func(StreamChunk) {})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if capturedOperatorID != "cert-cn-bob" {
		t.Errorf("RunStream operator_id: got %q; want %q (cert CN must override caller-supplied)",
			capturedOperatorID, "cert-cn-bob")
	}
}

// TestRunStream_UnauthenticatedIdentity_OperatorID_Preserved verifies that for
// an unauthenticated identity (Populated=true, Authenticated=false),
// the caller-supplied OperatorID is preserved.
func TestRunStream_UnauthenticatedIdentity_OperatorID_Preserved(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "daemon-default",
	})

	var capturedOperatorID string
	withStreamRunFn(func(_ context.Context, req Request, _ runtime.Adapter, _ runtime.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		capturedOperatorID = req.OperatorID
		return Result{ExitCode: 0}, nil
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:      "chat-agent",
			Task:       "hello",
			Project:    logDir,
			OperatorID: "caller-op",
			ResolvedIdentity: IdentityCarrier{
				Populated: true,
				Identity: netid.Identity{
					Role:          netid.RoleAdmin,
					Authenticated: false,
					Resolved:      true,
					OperatorID:    "ignored",
				},
			},
		}, func(StreamChunk) {})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if capturedOperatorID != "caller-op" {
		t.Errorf("RunStream operator_id: got %q; want %q (unauthenticated: caller OperatorID must be used)",
			capturedOperatorID, "caller-op")
	}
}

// ---- L8: project path scope ---------------------------------------------

// TestRun_RejectsFilesystemRootProject is the core L8 regression: a caller
// passing "/" as Project must be rejected before it becomes --add-dir /
// cwd for the dispatched agent (which would widen tool scope to everything
// the operator's user account can read or write). Populated=false so the
// role gate does not fire first.
//
// Round-2 review R6: the original version of this test asserted only
// `err == nil -> Fatal`. Run() also fails for the UNRELATED reason of
// "any" not existing in the composed agent roster
// (`dispatch: agent "any" not found in composed set`), so the test passed
// even with validateProjectPath's call sites removed entirely and would not
// have caught that regression. Asserting the actual message closes that.
func TestRun_RejectsFilesystemRootProject(t *testing.T) {
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     logDir,
		WorkspaceRoot: logDir,
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "any",
		Task:    "do something",
		Project: "/",
	})
	if err == nil || !strings.Contains(err.Error(), "must not be the filesystem root") {
		t.Fatalf("Run with Project=%q: got %v; want filesystem-root rejection", "/", err)
	}
}

// TestRun_RejectsBroadScopeDirs is the R6 regression proper: the original
// validator rejected only the literal string "/", so a caller who wanted
// "/" and got rejected could pass "/Users" (or "/home", "/etc", ...) and
// obtain an effectively equivalent scope -- every local user's files,
// commonly including SSH keys and every other project on the machine. Each
// of these must now be rejected too, with the broad-scope message (not the
// literal-root message, which broadScopeDirs is a distinct check from).
func TestRun_RejectsBroadScopeDirs(t *testing.T) {
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     logDir,
		WorkspaceRoot: logDir,
	})

	for _, project := range []string{"/Users", "/home", "/etc", "/private", "/tmp"} {
		t.Run(project, func(t *testing.T) {
			_, _, err := svc.Run(context.Background(), Params{
				Agent:   "any",
				Task:    "do something",
				Project: project,
			})
			if err == nil || !strings.Contains(err.Error(), "scope materially equivalent to the filesystem root") {
				t.Fatalf("Run with Project=%q: got %v; want broad-scope rejection", project, err)
			}
		})
	}
}

// TestRun_RejectsRelativeDotDotEscapingToRoot is the R6 relative-path
// regression: a relative ".." value was previously accepted unresolved and
// escaped against the DAEMON's cwd rather than being validated as the
// caller intended. Resolving to an absolute path before the check closes
// this for the concrete case where the relative path resolves onto a
// broad-scope directory.
func TestRun_RejectsRelativeDotDotEscapingToRoot(t *testing.T) {
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     logDir,
		WorkspaceRoot: logDir,
	})

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// Enough ".." segments to reach the filesystem root from the daemon's
	// actual working directory, regardless of how deep the test runner's
	// cwd happens to be.
	depth := strings.Count(filepath.Clean(wd), string(filepath.Separator))
	dotdot := strings.Repeat(".."+string(filepath.Separator), depth+2)

	_, _, runErr := svc.Run(context.Background(), Params{
		Agent:   "any",
		Task:    "do something",
		Project: dotdot,
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "invalid project") {
		t.Fatalf("Run with Project=%q (relative escape to root): got %v; want a project-validation rejection", dotdot, runErr)
	}
}

// TestRun_AllowsOrdinaryProjectPath verifies the L8/R6 fix does not
// overtighten -- an ordinary project directory (the whole point of this
// field) must still be accepted, all the way through to Run/RunStream, not
// just at the validator function in isolation. Round-2 review R6: the
// original version of this test called validateProjectPath directly and
// never called Run despite its name, so it could not catch a regression in
// how Run wires the validator in. This drives it through svc.Run/RunStream
// with a real ordinary temp-dir path and asserts the failure (there will be
// one, since "any" is not a real agent) is NOT a project-validation
// rejection.
func TestRun_AllowsOrdinaryProjectPath(t *testing.T) {
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     logDir,
		WorkspaceRoot: logDir,
	})
	ordinary := filepath.Join(t.TempDir(), "myapp")
	if err := os.MkdirAll(ordinary, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "any",
		Task:    "do something",
		Project: ordinary,
	})
	if err != nil && strings.Contains(err.Error(), "invalid project") {
		t.Errorf("Run with an ordinary project path %q was rejected as invalid: %v", ordinary, err)
	}

	if err := validateProjectPath(ordinary); err != nil {
		t.Errorf("validateProjectPath(ordinary path): want nil, got %v", err)
	}
	if err := validateProjectPath(""); err != nil {
		t.Errorf("validateProjectPath(\"\"): want nil (falls back to WorkspaceRoot), got %v", err)
	}
}

// TestRunStream_RejectsFilesystemRootProject mirrors
// TestRun_RejectsFilesystemRootProject for the streaming path.
func TestRunStream_RejectsFilesystemRootProject(t *testing.T) {
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     logDir,
		WorkspaceRoot: logDir,
	})

	_, err := svc.RunStream(context.Background(), Params{
		Agent:   "any",
		Task:    "do something",
		Project: "/",
	}, func(StreamChunk) {})
	if err == nil || !strings.Contains(err.Error(), "must not be the filesystem root") {
		t.Fatalf("RunStream with Project=%q: got %v; want filesystem-root rejection", "/", err)
	}
}

// ---- notes ------------------------------------------------------------------
// withRunFn is defined in service_test.go (same package dispatch).
// withStreamRunFn is defined in stream_test.go (same package dispatch).
// buildFakeRoster is defined in stream_test.go (same package dispatch).
// isolatedLogDir is defined in dispatch_test.go (same package dispatch).
