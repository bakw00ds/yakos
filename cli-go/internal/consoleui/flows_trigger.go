package consoleui

// flows_trigger.go — K-152: the webhook trigger endpoint and the template
// gallery's read-only listing.
//
//	POST /flows/api/trigger/{name}   (RoleDispatch)
//	GET  /flows/api/templates[?name=] (RoleRead)
//
// Webhook security model:
//   - A workflow can declare `triggers.webhook`, but it answers only when the
//     operator enabled it in the trusted user-level schedules file AND repeated
//     the declared secret_env name there (a cloned repo cannot pick which
//     environment variable the daemon reads). Every "not available" cause
//     (undeclared, not enabled, untrusted file, unset or short secret) answers
//     the same 404, so the endpoint is not a configuration oracle.
//   - The shared secret is read from the environment at request time, and
//     compared in constant time over SHA-256 digests (no length leak).
//   - The body is capped at 64 KiB and must be UTF-8. The raw payload passes
//     through the engine's OutputScanFn (the same blocking injection scan node
//     output gets) before it reaches any node; the scan being absent fails
//     closed. Accepted payload is handed to the workflow as the declared input
//     `payload`, wrapped as inert delimited data.
//   - The run's owner is the caller's resolved identity, never a body field.
//
// Idempotency: POST is NOT idempotent and takes no Idempotency-Key. Each call
// starts a new run, unless one is already active (409): a webhook sender's
// retry therefore cannot pile up concurrent runs of the same workflow.

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/workflow"
)

const (
	// maxTriggerBodyBytes caps a webhook payload.
	maxTriggerBodyBytes = 64 << 10
	// triggerSecretHeader carries the shared secret.
	triggerSecretHeader = "X-Yakos-Webhook-Secret"
	// minWebhookSecretLen is the shortest secret the endpoint will accept
	// from the environment; a shorter or empty one disables the webhook.
	minWebhookSecretLen = 16
	triggerPathPrefix   = "/flows/api/trigger/"
)

type flowsTriggerResponse struct {
	RunID string `json:"run_id"`
}

// secretsEqual compares in constant time without leaking either length.
func secretsEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
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

	wf, err := workflow.Load(h.workflowPath(name))
	if err != nil || workflow.Validate(wf) != nil || wf.Triggers == nil || wf.Triggers.Webhook == nil {
		notAvailable()
		return
	}
	sched, err := workflow.LoadSchedules(h.slug)
	if err != nil {
		slog.Warn("flows: webhook refused, schedules file unusable", "workflow", name, "reason", err.Error())
		notAvailable()
		return
	}
	ent := sched.Workflows[name]
	envName := wf.Triggers.Webhook.SecretEnv
	if !ent.Webhook || ent.SecretEnv != envName {
		notAvailable()
		return
	}
	secret := os.Getenv(envName)
	if len(secret) < minWebhookSecretLen {
		slog.Warn("flows: webhook refused, secret not configured", "workflow", name)
		notAvailable()
		return
	}
	if !secretsEqual(r.Header.Get(triggerSecretHeader), secret) {
		writeGenericError(w, http.StatusUnauthorized, "unauthorized")
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
