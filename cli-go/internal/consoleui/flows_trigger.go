package consoleui

// flows_trigger.go — K-152: the webhook trigger endpoint and the template
// gallery's read-only listing.
//
//	POST /flows/api/trigger/{name}   (RoleDispatch)
//	GET  /flows/api/templates[?name=] (RoleRead)
//
// Webhook security model:
//   - A workflow can declare `triggers.webhook`, but it answers only when the
//     operator enabled it in the trusted user-level schedules file (bound to
//     this workspace's canonical path), repeated the declared secret_env name
//     there, and the workflow file still hashes to the entry's workflow_sha.
//   - The sender signs: `X-Yakos-Signature: sha256=<hex HMAC-SHA256(secret,
//     timestamp + "." + body)>` with `X-Yakos-Timestamp: <unix seconds>`. The
//     timestamp must be within +-5 minutes and a signature is accepted once
//     (bounded cache of 1000), so a captured request cannot be replayed. The
//     secret is read from the daemon environment at request time. There is no
//     bare-secret header path.
//   - Every "not available" cause (undeclared, not enabled, untrusted file,
//     changed workflow, unset or short secret, bad/stale/replayed signature)
//     answers the same 404, so the endpoint is neither a configuration nor a
//     secret-validity oracle. Requests are limited to 6 per minute per
//     workflow name, answered 429 before any configuration is read.
//   - The body is capped at 64 KiB and must be UTF-8. The raw payload passes
//     through the engine's OutputScanFn (the same blocking injection scan node
//     output gets) before it reaches any node; the scan being absent fails
//     closed. Accepted payload is handed to the workflow as the declared input
//     `payload`, wrapped as inert delimited data.
//   - The run's owner is the caller's resolved identity, never a body field.
//
// Role decision (K-152 review, medium finding): the route is RoleDispatch, not
// the RoleFlowsRun that /flows/api/run requires. The operator's explicit,
// content-pinned entry in the trusted schedules file is the consent to run
// this one workflow on an external event; the HMAC secret is held by the
// sender, not by the console user. /flows/api/run keeps its stricter role.
// The edge still requires the console token or session: an external sender
// needs the token as well as the signature.
//
// Idempotency: POST is NOT idempotent and takes no Idempotency-Key. Each call
// starts a new run, unless one is already active (409): a webhook sender's
// retry therefore cannot pile up concurrent runs of the same workflow. A
// retry must carry a fresh timestamp (the replay cache rejects a repeated
// signature).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/workflow"
)

const (
	// maxTriggerBodyBytes caps a webhook payload.
	maxTriggerBodyBytes = 64 << 10
	// triggerSignatureHeader carries "sha256=<hex>"; triggerTimestampHeader the
	// unix-seconds timestamp that is part of the signed message.
	triggerSignatureHeader = "X-Yakos-Signature"
	triggerTimestampHeader = "X-Yakos-Timestamp"
	// minWebhookSecretLen is the shortest secret the endpoint will accept
	// from the environment; a shorter or empty one disables the webhook.
	minWebhookSecretLen = 16
	triggerPathPrefix   = "/flows/api/trigger/"
)

type flowsTriggerResponse struct {
	RunID string `json:"run_id"`
}

// TriggerSignature returns the header value a sender sets as
// X-Yakos-Signature for a body signed with secret at timestamp ts.
func TriggerSignature(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// signatureValid verifies the MAC in constant time.
func signatureValid(secret, ts string, body []byte, got string) bool {
	return hmac.Equal([]byte(TriggerSignature(secret, ts, body)), []byte(got))
}

// handleTrigger serves POST /flows/api/trigger/{name}.
func (h *flowsHandlers) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, triggerPathPrefix)
	if err := workflow.ValidateID("name", name); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid workflow name")
		return
	}
	notAvailable := func() { writeGenericError(w, http.StatusNotFound, "webhook not available") }
	if h.engine == nil {
		writeGenericError(w, http.StatusServiceUnavailable, "workflow engine not configured")
		return
	}

	if !h.trigGuard.allow(name) {
		w.Header().Set("Retry-After", "60")
		writeGenericError(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	// Enablement first (a small trusted file), then the workflow file: the
	// workflow is only opened for a name the operator enabled.
	sched, err := workflow.LoadSchedules(h.workspaceRoot)
	if err != nil {
		slog.Warn("flows: webhook refused, schedules file unusable", "workflow", name, "reason", err.Error())
		notAvailable()
		return
	}
	ent, enabled := sched.Workflows[name]
	if !enabled || !ent.Webhook {
		notAvailable()
		return
	}
	wf, sha, err := workflow.LoadFile(h.workflowPath(name))
	if err != nil || workflow.Validate(wf) != nil || wf.Triggers == nil || wf.Triggers.Webhook == nil {
		notAvailable()
		return
	}
	envName := wf.Triggers.Webhook.SecretEnv
	if ent.SecretEnv != envName {
		notAvailable()
		return
	}
	if perr := workflow.CheckPin(ent, sha); perr != nil {
		slog.Warn("flows: webhook refused", "workflow", name, "reason", perr.Error())
		h.engine.RecordTriggerRefusal(name, workflow.TriggerWebhook, perr.Error())
		notAvailable()
		return
	}
	secret := os.Getenv(envName)
	if len(secret) < minWebhookSecretLen {
		slog.Warn("flows: webhook refused, secret not configured", "workflow", name)
		notAvailable()
		return
	}

	if r.ContentLength > maxTriggerBodyBytes {
		writeGenericError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTriggerBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeGenericError(w, http.StatusRequestEntityTooLarge, "payload too large")
			return
		}
		writeGenericError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if !utf8.Valid(body) {
		writeGenericError(w, http.StatusBadRequest, "payload must be UTF-8")
		return
	}

	// Authenticate: signed timestamp inside the window, valid MAC, first use.
	ts := r.Header.Get(triggerTimestampHeader)
	sig := r.Header.Get(triggerSignatureHeader)
	secs, perr := strconv.ParseInt(ts, 10, 64)
	if perr != nil || !h.trigGuard.fresh(time.Unix(secs, 0)) || !signatureValid(secret, ts, body, sig) {
		slog.Warn("flows: webhook refused, bad or stale signature", "workflow", name)
		notAvailable()
		return
	}
	if !h.trigGuard.firstUse(sig) {
		slog.Warn("flows: webhook refused, replayed signature", "workflow", name)
		notAvailable()
		return
	}
	// Cheap 409 before the payload scan spawns a subprocess.
	if h.engine.RunActive(wf.Name) {
		writeGenericError(w, http.StatusConflict, "a run of this workflow is already active")
		return
	}

	inputs := map[string]string(nil)
	if len(body) > 0 {
		if h.engine.OutputScanFn == nil {
			writeGenericError(w, http.StatusServiceUnavailable, "payload scan unavailable")
			return
		}
		if err := h.engine.OutputScanFn(r.Context(), "webhook", "", body); err != nil {
			slog.Warn("flows: webhook payload rejected by the injection scan", "workflow", name)
			writeGenericError(w, http.StatusUnprocessableEntity, "payload rejected by injection scan")
			return
		}
		wrapped, err := workflow.WrapTriggerPayload(body)
		if err != nil {
			writeGenericError(w, http.StatusInternalServerError, "failed to prepare payload")
			return
		}
		inputs = map[string]string{"payload": wrapped}
	}

	resolved := netid.IdentityFrom(r.Context())
	operatorID, err := resolveRunOperatorID(r)
	if err != nil {
		writeGenericError(w, http.StatusForbidden, "forbidden: unable to resolve operator identity")
		return
	}
	carrier := dispatch.IdentityCarrier{Populated: resolved.Resolved, Identity: resolved}
	runID, err := h.engine.StartTriggered(h.serverCtx, wf, workflow.TriggerWebhook, operatorID, carrier, inputs)
	if errors.Is(err, workflow.ErrRunActive) {
		writeGenericError(w, http.StatusConflict, "a run of this workflow is already active")
		return
	}
	if err != nil {
		slog.Error("flows: webhook start failed", "workflow", name, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to start run")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(flowsTriggerResponse{RunID: runID})
}

// ---- templates gallery -----------------------------------------------------

// maxTemplateBytes caps one template file read.
const maxTemplateBytes = 64 << 10

type flowsTemplateInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *flowsHandlers) templatesDir() string {
	if h.yakosRoot == "" {
		return ""
	}
	return filepath.Join(h.yakosRoot, "lib", "workflows", "templates")
}

// readTemplate reads one template by name (path-safe ID, regular file only).
func (h *flowsHandlers) readTemplate(name string) ([]byte, error) {
	dir := h.templatesDir()
	if dir == "" {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(dir, name+".yaml")
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(path) //nolint:gosec // name validated, regular file checked
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxTemplateBytes))
}

// templateDescription is the text of the file's first `# ` comment line.
func templateDescription(data []byte) string {
	for _, line := range strings.SplitN(string(data), "\n", 8) {
		if d, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(d)
		}
	}
	return ""
}

// handleTemplates serves GET /flows/api/templates (list) and
// GET /flows/api/templates?name=<n> (one template's YAML). Templates are
// read-only framework files; using one saves a copy through the normal
// POST /flows/api/workflow path, which validates it.
func (h *flowsHandlers) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if name := r.URL.Query().Get("name"); name != "" {
		if err := workflow.ValidateID("name", name); err != nil {
			writeGenericError(w, http.StatusBadRequest, "invalid template name")
			return
		}
		data, err := h.readTemplate(name)
		if err != nil {
			writeGenericError(w, http.StatusNotFound, "template not found")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": name, "yaml": string(data)})
		return
	}
	out := []flowsTemplateInfo{}
	if dir := h.templatesDir(); dir != "" {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			stem, ok := strings.CutSuffix(e.Name(), ".yaml")
			if !ok || workflow.ValidateID("template name", stem) != nil {
				continue
			}
			data, err := h.readTemplate(stem)
			if err != nil {
				continue
			}
			out = append(out, flowsTemplateInfo{Name: stem, Description: templateDescription(data)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	_ = json.NewEncoder(w).Encode(map[string][]flowsTemplateInfo{"templates": out})
}
