package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/daemonclient"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
)

// daemonMode reads YAKOS_DAEMON from the environment.
// Returns "off", "on", or "auto".
func daemonMode() string {
	v := os.Getenv("YAKOS_DAEMON")
	switch v {
	case "1", "on":
		return "on"
	case "auto":
		return "auto"
	default:
		return "off"
	}
}

// restartStaleDaemonRequested reports whether the operator opted in to
// automatically restarting a stale daemon encountered on this call — via the
// literal "--restart-stale-daemon" token anywhere in args, or
// YAKOS_RESTART_STALE_DAEMON=1 — and returns args with that token stripped
// so a fall-through to the bash passthrough never forwards a flag bash
// does not understand.
//
// `yakos start` does not consult this: it already auto-restarts a stale
// daemon unconditionally (it owns the daemon's lifecycle — see runStart's
// build-id check). This flag only governs the other connect sites
// (maybeRouteToDaemon, runEvents), where the default is refuse-and-explain,
// never a silent restart — restarting a daemon out from under an arbitrary
// command could kill a daemon another session is attached to. See
// work/current/reports/s6-structural-plan-2026-09-23.md §4.4 (decision D5).
func restartStaleDaemonRequested(args []string) (bool, []string) {
	requested := os.Getenv("YAKOS_RESTART_STALE_DAEMON") == "1"
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--restart-stale-daemon" {
			requested = true
			continue
		}
		out = append(out, a)
	}
	return requested, out
}

// daemonMismatchMessage composes the refuse-and-explain message for a stale
// daemon (D5): both build ids, the daemon's pid when known, and the fix.
func daemonMismatchMessage(stale *daemonclient.ErrStaleDaemon, pid int) string {
	daemonID := stale.DaemonBuildID
	if daemonID == "" {
		daemonID = "unknown (pre-handshake daemon)"
	}
	pidStr := "unknown"
	if pid > 0 {
		pidStr = fmt.Sprintf("%d", pid)
	}
	return fmt.Sprintf(
		"yakos: daemon build mismatch\n"+
			"  daemon: %s (pid %s)\n"+
			"  cli:    %s\n"+
			"The running daemon predates this binary; its responses would not reflect\n"+
			"your current build. Fix it with:\n"+
			"  yakos serve stop            (then re-run this command to start a fresh daemon)\n"+
			"  or re-run with --restart-stale-daemon, or set YAKOS_RESTART_STALE_DAEMON=1,\n"+
			"  to restart it automatically\n",
		daemonID, pidStr, stale.WantBuildID,
	)
}

// restartStaleDaemonAndCheck stops the daemon at pidPath/socketPath, spawns a
// fresh one via spawnDaemonFn, waits for its socket, dials it, and verifies
// its build id now matches want. Returns the connected client on success.
// The caller is responsible for closing the returned client.
//
// The fresh daemon is spawned with no serve args (spawnDaemonFn(nil)):
// unlike runStart, this call site has no console/networked flags to
// reconstruct — the daemon it is replacing may have been started with
// flags (--console-bind, --networked, …) this opportunistic restart cannot
// recover. This is a known, documented limitation of restarting from a
// command other than `yakos start`; operators who need those flags
// preserved should use `yakos serve stop` + `yakos start` instead.
func restartStaleDaemonAndCheck(pidPath, socketPath, want string) (*jsonrpc.Client, error) {
	stopStaleDaemon(pidPath, socketPath)
	if err := spawnDaemonFn(nil); err != nil {
		return nil, fmt.Errorf("daemon restart failed: %w", err)
	}
	if !pollSocketFn(socketPath, 5*time.Second) {
		return nil, fmt.Errorf("daemon did not come back up within 5s after restart")
	}
	conn, err := jsonrpc.Dial(socketPath)
	if err != nil {
		return nil, fmt.Errorf("could not reconnect to restarted daemon: %w", err)
	}
	client := jsonrpc.NewClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := daemonclient.Check(ctx, client, want); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("daemon still mismatched after restart: %w", err)
	}
	return client, nil
}

// maybeRouteToDaemon checks YAKOS_DAEMON and routes the subcommand through the
// daemon JSON-RPC client if a daemon is reachable. Returns true if the
// subcommand was handled (or fatally errored). Returns false to fall through
// to the bash passthrough.
//
// Every connect made here is gated by the CLI↔daemon build handshake
// (internal/daemonclient.Check): a build-id mismatch refuses with an
// actionable message and exits 1 by default (D5), or — opt-in via
// --restart-stale-daemon / YAKOS_RESTART_STALE_DAEMON=1 — restarts the
// daemon and retries. See restartStaleDaemonRequested's doc comment for why
// this differs from `yakos start`'s unconditional auto-restart.
func maybeRouteToDaemon(yakosRoot string, args []string) bool {
	mode := daemonMode()
	if mode == "off" {
		return false
	}

	restartStale, args := restartStaleDaemonRequested(args)

	// Detect the daemon socket.
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	socketPath := jsonrpc.SocketPath(cwd)
	pidPath := jsonrpc.PIDPath(cwd)

	// Attempt to connect (200 ms timeout per design §2 detection).
	conn, err := jsonrpc.Dial(socketPath)
	if err != nil {
		// Daemon not running.
		if mode == "on" {
			fmt.Fprintf(os.Stderr, "yakos: WARN daemon not running at %s (YAKOS_DAEMON=on); falling through to local exec\n", socketPath)
		}
		// auto: silent fallback.
		return false
	}

	client := jsonrpc.NewClient(conn)
	defer func() { _ = client.Close() }()

	want := buildinfo.BuildID()
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
	checkErr := daemonclient.Check(checkCtx, client, want)
	checkCancel()

	if checkErr != nil {
		var stale *daemonclient.ErrStaleDaemon
		if !errors.As(checkErr, &stale) {
			// Query/decode failure — not a build-identity determination.
			fmt.Fprintf(os.Stderr, "yakos: daemon ping failed: %v; falling through to local exec\n", checkErr)
			return false
		}

		if !restartStale {
			pid, _ := readPIDFile(pidPath)
			fmt.Fprint(os.Stderr, daemonMismatchMessage(stale, pid))
			os.Exit(1)
		}

		_ = client.Close()
		freshClient, restartErr := restartStaleDaemonAndCheck(pidPath, socketPath, want)
		if restartErr != nil {
			fmt.Fprintf(os.Stderr, "yakos: %v; falling through to local exec\n", restartErr)
			return false
		}
		fmt.Fprintln(os.Stderr, "yakos: restarted stale daemon")
		// client is reassigned; the defer above (which closes over the
		// variable, not its value at defer time) will close freshClient at
		// function exit.
		client = freshClient
	}

	// Route --version.
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		raw, err := client.Call(context.Background(), "yakos.version", nil)
		if err == nil {
			var result struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(raw, &result); err == nil && result.Version != "" {
				fmt.Println(result.Version + " [via daemon]")
				return true
			}
		}
	}

	// Route kanban mutations through the daemon so the WS bus receives events.
	// Only the mutation subcommands are routed; reads fall through to in-process.
	if len(args) >= 2 && args[0] == "kanban" {
		routed, ok := routeKanbanViaDaemon(client, args[1:])
		if ok {
			if routed != "" {
				fmt.Println(routed)
			}
			return true
		}
	}

	return false
}

// routeKanbanViaDaemon handles kanban subcommand routing through the daemon RPC.
// Returns (output, true) if the subcommand was handled, ("", false) otherwise.
func routeKanbanViaDaemon(client *jsonrpc.Client, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	ctx := context.Background()
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return "", false // let in-process handle the error
		}
		title := args[1]
		var category, notes string
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--category":
				i++
				if i < len(args) {
					category = args[i]
				}
			case "--notes":
				i++
				if i < len(args) {
					notes = args[i]
				}
			}
		}
		params := map[string]string{"title": title}
		if category != "" {
			params["category"] = category
		}
		if notes != "" {
			params["notes"] = notes
		}
		raw, err := client.Call(ctx, "yakos.kanban.add", params)
		if err != nil {
			return "", false // fall through to in-process
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", false
		}
		return fmt.Sprintf("kanban: added: %s — %s (category: %s)", result.ID, title, func() string {
			if category == "" {
				return "other"
			}
			return category
		}()), true

	case "move":
		if len(args) < 3 {
			return "", false
		}
		raw, err := client.Call(ctx, "yakos.kanban.move", map[string]string{"id": args[1], "to": args[2]})
		if err != nil {
			return "", false
		}
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
			return "", false
		}
		return fmt.Sprintf("kanban: moved %s to %s", args[1], args[2]), true

	case "done":
		if len(args) < 2 {
			return "", false
		}
		raw, err := client.Call(ctx, "yakos.kanban.done", map[string]string{"id": args[1]})
		if err != nil {
			return "", false
		}
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
			return "", false
		}
		return fmt.Sprintf("kanban: %s moved to DONE", args[1]), true
	}
	return "", false
}
