package anthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// handleModels forwards GET /v1/models and keeps only the Claude ids: a client
// that lists models through this gateway must not be offered anything else.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	q, release, ok := s.admit(w, r, "models", http.MethodGet)
	if !ok {
		return
	}
	defer release()
	defer q.finish()

	req, err := q.upstreamRequest("/v1/models", nil)
	if err != nil {
		q.refuse(http.StatusInternalServerError, "api_error", "bad_request", "could not build the upstream request")
		return
	}
	// The list is rewritten, so it must arrive uncompressed.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := s.client.Do(req)
	if err != nil {
		q.upstreamFailed(err)
		return
	}
	defer resp.Body.Close()
	q.ev.Status = resp.StatusCode
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONBody+1))
	if err != nil || len(raw) > maxJSONBody {
		q.refuse(http.StatusBadGateway, "api_error", "bad_upstream", "unreadable model list from upstream")
		return
	}
	out := raw
	if resp.StatusCode == http.StatusOK {
		filtered, ok := filterModels(raw)
		if !ok {
			q.refuse(http.StatusBadGateway, "api_error", "bad_upstream", "unexpected model list from upstream")
			return
		}
		out = filtered
	}
	copyHeaders(w.Header(), resp.Header)
	w.Header().Del("Content-Encoding")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

// filterModels keeps the entries of a list reply whose id is a Claude model id,
// leaving every other field and entry property as the upstream sent it.
func filterModels(raw []byte) ([]byte, bool) {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil, false
	}
	var data []json.RawMessage
	if json.Unmarshal(top["data"], &data) != nil {
		return nil, false
	}
	kept := make([]json.RawMessage, 0, len(data))
	var firstID, lastID string
	for _, e := range data {
		var m struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(e, &m) != nil || !claudeModelRe.MatchString(m.ID) {
			continue
		}
		if firstID == "" {
			firstID = m.ID
		}
		lastID = m.ID
		kept = append(kept, e)
	}
	d, _ := json.Marshal(kept)
	top["data"] = d
	if _, has := top["first_id"]; has {
		top["first_id"], _ = json.Marshal(firstID)
		top["last_id"], _ = json.Marshal(lastID)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(top) != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}
