package consoleui

// hooks_endpoint.go — POST /api/hooks/run/{name}?shape=codex|agy (K-145).
//
// Lets a codex or agy process on the same host run a registered yakOS hook
// without a yakos binary on its PATH. The body is the harness's tool-call
// envelope; the answer is {"exit_code","stdout","stderr"}, which the caller
// replays (codex: exit 2 + stderr to deny; agy: the stdout JSON).
//
// Gates, outermost first:
//   - OFF unless hooks_endpoint: true in the owner-only router policy; the
//     route is then not registered at all (404), so a project cannot enable it.
//   - RoleDispatch (requireRoleFunc in registerRoutes; not re-checked here).
//   - Host header via dashauth.RequireLocalHost and a loopback RemoteAddr:
//     refused (403) on the networked path, so it is never exposed over mTLS.
//   - A browser Origin must be a loopback origin on the console port (403),
//     the DNS-rebinding defence (same rule as mcpserver's streamable HTTP).
//   - X-Yakos-Hook-Nonce must equal the per-daemon nonce (401). The nonce is
//     random per daemon start and written 0600 to the trusted state dir.
//   - Body capped at 64 KiB (413); only registered hook names (404); the shape
//     must be codex or agy (400). A caller MUST treat 413 (and any non-200
//     answer) as DENY: a tool call too large to inspect is not allowed by
//     default, and fail-open on an endpoint error would bypass the gate.
//   - The nonce is bound to one project directory when it is issued
//     (HooksEndpoint.ProjectDir, the daemon's project). Every hook runs against
//     that project, whatever the caller sends. An envelope whose cwd or
//     workspacePaths name a directory outside it, or a relative one, is refused
//     (403): the message and the audit line carry no path.
//   - ?agent=<id> names the dispatched agent for path-allowlist. Without a valid
//     id, path-allowlist refuses file-path calls when a policy file exists.
//
// Idempotency-Key: not declared. Hooks are pure gates plus append-only
// telemetry (budget-guard counts calls), so a retry can double-count one tool
// call; callers must not retry a hook call that reached the daemon.
// Rate limiting: inherits the console default class. Audit: hooks write their
// own NDJSON records; this handler logs name, shape and verdict only (no body,
// no path).

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
)

const (
	hooksEndpointMaxBody = 64 << 10
	hooksNonceHeader     = "X-Yakos-Hook-Nonce"
	hooksRoutePrefix     = "/api/hooks/run/"
)

// HooksEndpoint wires the endpoint. A nil Config.HooksEndpoint leaves it off.
type HooksEndpoint struct {
	// Run executes hook name on one envelope (shaperun.Run bound to the
	// daemon's dependencies).
	Run func(ctx context.Context, shape, name string, body []byte) hookio.Response
	// Known reports whether name is a registered hook.
	Known func(name string) bool
	// NonceFile is where the per-daemon nonce is written (0600). Required.
	NonceFile string
	// ProjectDir is the absolute project directory the nonce is bound to at
	// issue time. Required: the endpoint stays off without it.
	ProjectDir string
}

type hooksResponseDTO struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type hooksHandler struct {
	ep    HooksEndpoint
	nonce string
	port  string
	// project is ep.ProjectDir, symlink-resolved once at issue time.
	project string
}

// newHooksHandler generates the nonce, writes it, and returns the handler
// (without the role gate). A failure to persist the nonce leaves the endpoint
// off rather than usable by nobody-knows-whom.
func newHooksHandler(ep *HooksEndpoint, addr string) (http.Handler, error) {
	if ep == nil || ep.Run == nil || ep.Known == nil || ep.NonceFile == "" || !isAbsFor(runtime.GOOS, ep.ProjectDir) {
		return nil, errors.New("hooks endpoint: incomplete configuration")
	}
	project, err := resolveDir(ep.ProjectDir)
	if err != nil {
		return nil, errors.New("hooks endpoint: bound project directory is not usable")
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	h := &hooksHandler{ep: *ep, nonce: hex.EncodeToString(raw[:]), project: project}
	if _, p, err := net.SplitHostPort(addr); err == nil {
		h.port = p
	}
	if err := writeNonceFile(ep.NonceFile, h.nonce); err != nil {
		return nil, err
	}
	return dashauth.RequireLocalHost(addr, http.HandlerFunc(h.serve)), nil
}

// resolveDir resolves the symlinks of the longest existing prefix of dir and
// appends the rest, so a link inside the project cannot be hidden behind a
// component that does not exist yet ("P/link-out/missing"). A path with a ".."
// component is refused: the OS would apply it to a link's target, which a
// lexical Clean cannot see.
func resolveDir(dir string) (string, error) {
	for _, c := range strings.FieldsFunc(dir, func(r rune) bool { return r == '/' || r == '\\' }) {
		if c == ".." {
			return "", errors.New("parent component")
		}
	}
	p := filepath.Clean(dir)
	var rest []string
	for {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				r = filepath.Join(r, rest[i])
			}
			return r, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if fi, lerr := os.Lstat(p); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			// A dangling link: its target (maybe outside the project) does not
			// exist yet, but the OS would follow it when something is created.
			return "", errors.New("dangling symlink")
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		rest = append(rest, filepath.Base(p))
		p = parent
	}
}

// withinProject reports whether dir is the bound project or inside it.
func (h *hooksHandler) withinProject(dir string) bool {
	if !isAbsFor(runtime.GOOS, dir) {
		return false
	}
	d, err := resolveDir(dir)
	if err != nil {
		return false
	}
	return pathWithin(runtime.GOOS, h.project, d)
}

// canonPath is the comparison form of an absolute path on goos: separators
// unified to "/", cleaned, and, on Windows, case-folded (its file systems are
// case-insensitive). It is lexical; callers resolve symlinks first.
func canonPath(goos, p string) string {
	if goos == "windows" {
		p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	}
	return path.Clean(p)
}

// isAbsFor reports whether p is absolute on goos: a rooted path, or on Windows
// also a drive path ("C:\x", "c:/x") or a UNC path.
func isAbsFor(goos, p string) bool {
	if goos != "windows" {
		return strings.HasPrefix(p, "/")
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(p, "//") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && p[2] == '/' && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z')
}

// pathWithin reports whether dir equals project or lies below it, comparing
// canonical forms for goos. Both arguments must be absolute.
func pathWithin(goos, project, dir string) bool {
	p, d := canonPath(goos, project), canonPath(goos, dir)
	if d == p {
		return true
	}
	return strings.HasPrefix(d, strings.TrimRight(p, "/")+"/")
}

func writeNonceFile(path, nonce string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("hooks endpoint: nonce file is a symlink")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".hooks-nonce-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.WriteString(nonce + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp)
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func hooksJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *hooksHandler) originOK(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) {
		return false
	}
	return h.port == "" || u.Port() == h.port
}

func (h *hooksHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		hooksJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	host, _, err := splitHostAddr(r.RemoteAddr)
	if err != nil || !isLoopbackHost(host) {
		hooksJSONError(w, http.StatusForbidden, "hooks endpoint is loopback-only")
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !h.originOK(o) {
		hooksJSONError(w, http.StatusForbidden, "unexpected Origin")
		return
	}
	got := r.Header.Get(hooksNonceHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.nonce)) != 1 {
		hooksJSONError(w, http.StatusUnauthorized, "missing or invalid hook nonce")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, hooksRoutePrefix)
	if name == "" || strings.ContainsAny(name, "/\\") || !h.ep.Known(name) {
		hooksJSONError(w, http.StatusNotFound, "unknown hook")
		return
	}
	shape := r.URL.Query().Get("shape")
	if !hookio.IsShape(shape) {
		hooksJSONError(w, http.StatusBadRequest, "shape must be codex or agy")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, hooksEndpointMaxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			hooksJSONError(w, http.StatusRequestEntityTooLarge, "body exceeds 64 KiB")
			return
		}
		hooksJSONError(w, http.StatusBadRequest, "cannot read body")
		return
	}
	for _, d := range hookio.EnvelopeDirs(shape, body) {
		if !h.withinProject(d) {
			slog.Warn("hooks endpoint refused", "hook", name, "shape", shape, "reason", "envelope names a directory outside the nonce's project")
			hooksJSONError(w, http.StatusForbidden, "envelope names a directory outside the project this nonce is bound to")
			return
		}
	}
	ctx := hookio.WithProject(r.Context(), h.project)
	if a := r.URL.Query().Get("agent"); hookio.ValidAgent(a) {
		ctx = hookio.WithAgent(ctx, a)
	}
	resp := h.ep.Run(ctx, shape, name, body)
	slog.Info("hooks endpoint", "hook", name, "shape", shape, "exit", resp.ExitCode)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(hooksResponseDTO{ExitCode: resp.ExitCode, Stdout: string(resp.Stdout), Stderr: string(resp.Stderr)})
}
