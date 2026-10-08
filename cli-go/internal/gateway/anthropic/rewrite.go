package anthropic

// rewrite.go: the only change the gateway ever makes to a request body is the
// top-level `model` value, and only among Claude models, and only when the
// request carries a Claude Code hint header (x-claude-code-request-class, or
// x-claude-code-agent-type) naming a class that the trusted user policy's
// gateway_classes table assigns a model to (K-141). Every other request is
// forwarded byte-identical. The splice replaces just the bytes of the model
// string, so the rest of the body (and with it the prompt-cache prefix) is
// unchanged.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

const (
	hdrRequestClass = "X-Claude-Code-Request-Class"
	hdrAgentType    = "X-Claude-Code-Agent-Type"
)

// claudeModelRe is a first-party Claude id the public API accepts: the shape the
// policy validates (^[a-z0-9][a-z0-9._:-]{0,63}$) and the claude- prefix.
var claudeModelRe = regexp.MustCompile(`^claude-[a-z0-9][a-z0-9._:-]{0,57}$`)

var classNameRe = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// bodyInfo is what scanBody learns about a request body without changing it.
type bodyInfo struct {
	model      string
	stream     bool
	modelStart int // offset of the opening quote of the model value
	modelEnd   int // offset one past the closing quote
	modelKeys  int // top-level "model" keys seen; splice only when exactly one
	ok         bool
}

// scanBody walks the top level of a JSON object with the token decoder (no
// copy of the large values) and records the model value's byte span.
func scanBody(b []byte) bodyInfo {
	var info bodyInfo
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // a number out of float64 range must not stop the scan
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return info
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return info
		}
		key, _ := kt.(string)
		// Value start: after the colon and any whitespace.
		pos := int(dec.InputOffset())
		for pos < len(b) && (b[pos] == ':' || b[pos] == ' ' || b[pos] == '\t' || b[pos] == '\n' || b[pos] == '\r') {
			pos++
		}
		switch key {
		case "model":
			info.modelKeys++
			vt, err := dec.Token()
			if err != nil {
				return info
			}
			if s, isStr := vt.(string); isStr && info.modelKeys == 1 {
				info.model, info.modelStart, info.modelEnd = s, pos, int(dec.InputOffset())
			} else if isStr {
				info.model = s
			} else if _, isDelim := vt.(json.Delim); isDelim {
				if skipValue(dec) != nil {
					return info
				}
			}
		case "stream":
			vt, err := dec.Token()
			if err != nil {
				return info
			}
			if v, isBool := vt.(bool); isBool {
				info.stream = v
			} else if _, isDelim := vt.(json.Delim); isDelim {
				if skipValue(dec) != nil {
					return info
				}
			}
		default:
			if skipAny(dec) != nil {
				return info
			}
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return info
	}
	info.ok = true
	return info
}

// skipAny consumes one whole value.
func skipAny(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if _, isDelim := t.(json.Delim); isDelim {
		return skipValue(dec)
	}
	return nil
}

// skipValue consumes the rest of a container whose opening delimiter was just read.
func skipValue(dec *json.Decoder) error {
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, isDelim := t.(json.Delim); isDelim {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// requestClass reads the hint headers: request class first, then agent type.
// The result is a bounded lowercase name or "".
func requestClass(h http.Header) string {
	for _, name := range []string{hdrRequestClass, hdrAgentType} {
		c := strings.ToLower(strings.TrimSpace(h.Get(name)))
		if classNameRe.MatchString(c) {
			return c
		}
	}
	return ""
}

// planModel returns the model to send for modelIn under class, or modelIn
// itself when no rewrite applies. A rewrite needs: a known class, a table entry
// for it whose model is a concrete Claude id, and a Claude model coming in.
func (s *Server) planModel(class, modelIn string) string {
	if class == "" || s.cfg.Classes == nil || !claudeModelRe.MatchString(modelIn) {
		return modelIn
	}
	for _, c := range s.cfg.Classes() {
		if c.Class == class && claudeModelRe.MatchString(c.Model) {
			return c.Model
		}
	}
	return modelIn
}

// splice returns b with the model string replaced by modelOut. The caller has
// checked info.modelKeys == 1 and that modelOut is a valid Claude id (no
// character that needs escaping).
func splice(b []byte, info bodyInfo, modelOut string) []byte {
	out := make([]byte, 0, len(b)+len(modelOut))
	out = append(out, b[:info.modelStart]...)
	out = append(out, '"')
	out = append(out, modelOut...)
	out = append(out, '"')
	out = append(out, b[info.modelEnd:]...)
	return out
}
