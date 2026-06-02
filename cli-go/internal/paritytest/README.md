# paritytest

Package `paritytest` provides the parity-test harness for the yakOS Go port.
Every Phase 1 subcommand port uses it to verify that the Go binary produces
output equivalent to the bash baseline.

## Overview

A `Case` describes one scenario. `Run` invokes both bash yakos and Go yakos
with identical args/env/workdir, captures their stdout, stderr, and exit code,
then compares the results according to the comparison mode(s) you specify.

```go
paritytest.Run(t, paritytest.Case{
    Name:          "status-empty-workdir",
    Args:          []string{"status"},
    WorkdirSetup:  func(t testing.TB, dir string) { /* write fixture files */ },
    StdoutCompare: paritytest.CompareGolden,
    StderrCompare: paritytest.CompareIgnore,
    ExitCodeMatch: true,
})
```

## Binary resolution

| Variable            | Default                                    |
|---------------------|--------------------------------------------|
| `YAKOS_BASH_BINARY` | `/Users/tw/github/yakOS/cli/yakos`         |
| `YAKOS_GO_BINARY`   | `<repo-root>/bin/yakos`                    |

Set these env vars in CI or locally to point at non-default locations.

## Comparison modes

| Mode              | Use for                                                          |
|-------------------|------------------------------------------------------------------|
| `CompareExact`    | Byte-for-byte identical output (pure read commands)              |
| `CompareJSONL`    | Line-by-line JSON with configurable ignored fields               |
| `CompareRegex`    | Both sides match a pattern (dynamic but structured output)       |
| `CompareGolden`   | Go output compared against a captured-from-bash baseline file    |
| `CompareIgnore`   | Skip this stream (commonly used for stderr)                      |

## Golden files

Golden files live under `testdata/golden/<case-name>.{stdout,stderr,exit}`
relative to the calling test's source package directory.

They are git-tracked baseline files. CI always runs in comparison mode.
Developers re-capture when the bash baseline legitimately changes:

```
go test ./cmd/yakos/... -update-goldens
```

You can also capture a single test:

```
go test ./cmd/yakos/... -run TestStatusParity -update-goldens -v
```

After capturing, commit the updated golden files alongside the Go implementation.

## JSONL mode and field allowlist

When bash and Go both emit JSONL (e.g., dispatch-log entries), timestamps and
PIDs will differ per invocation. Strip them before comparison:

```go
paritytest.Case{
    Name:              "dispatch-log-entry",
    StdoutCompare:     paritytest.CompareJSONL,
    JSONLIgnoreFields: []string{"ts", "pid"},
}
```

Fields are deleted from each parsed JSON object before the objects are compared.

## Transform hooks

When the output formats legitimately differ between bash and Go (e.g., the
`(go)` suffix on version output), use `StdoutTransformBash` and
`StdoutTransformGo` to normalize before comparison:

```go
paritytest.Case{
    StdoutTransformBash: func(b []byte) []byte {
        // "yakos 0.36.0.0" → "0.36.0.0\n"
        return extractVersion(b)
    },
    StdoutTransformGo: func(b []byte) []byte {
        // "0.36.0.0 (go)" → "0.36.0.0\n"
        return extractVersion(b)
    },
}
```

## Fixture projects

Use `MakeFixtureProject` to populate a temp directory with a specific file
layout before invoking either binary:

```go
paritytest.Case{
    WorkdirSetup: func(t testing.TB, dir string) {
        paritytest.MakeFixtureProject(t, map[string]string{
            "CLAUDE.md":       "# Project\n",
            ".yakos/kanban.md": kanbanFixture,
        })
    },
}
```

The directory is cleaned up by `t.Cleanup`.

## Reference: version parity test

`cli-go/cmd/yakos/version_parity_test.go` is the canonical worked example.
It shows:

1. How to skip when binaries are absent (safe in environments with only one binary)
2. How to use `StdoutTransformBash` and `StdoutTransformGo` to normalize formats
3. How to combine `CompareGolden` for the normalized value with `CompareRegex`
   for a format assertion and `CompareIgnore` for stderr
4. How to assert exit code agreement independently of output content

Copy that file when starting a new subcommand port; adapt the comparison mode
and transforms for that command's parity contract.
