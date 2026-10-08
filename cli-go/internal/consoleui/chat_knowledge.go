package consoleui

// chat_knowledge.go: the knowledge pack for non-claude panes and the context
// drawer's endpoint (K-149).
//
//	GET /api/chat/context?conv=<conversationId> -> 200 JSON
//
// {"conversationId":"…","knowledge":{"sha":"…","bytes":N,"cap":N,"parts":[…]}|null,"soul":"…"}
//
// Auth: RoleRead at the middleware. Only the conversation's owner sees its
// pack; anyone else (and a conversation without one) gets knowledge:null, so
// the answer does not say whether a conversation exists. The soul text belongs
// to the host's HOME, so it is sent only to the loopback host operator (the
// identity in <stateDir>/loopback-operator-id, never a cert or session
// identity) for a lead conversation they own, and only when it passes the
// secret scanner; secret-shaped text is left out with a path-free note.
// Names, byte counts and the hash only: never the rule or agent text.
//
// Cache stability: the pack is composed on a conversation's first non-claude
// turn and stored; every later turn re-sends the stored bytes. A skill
// (`/<slug> …`) is appended to the END of the user turn, never to the pack.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
	"github.com/bakw00ds/yakos/internal/knowledge"
	"github.com/bakw00ds/yakos/internal/netid"
)

// nonClaudeTurn returns the knowledge block to send with a turn on rt and the
// task text to dispatch. For claude it changes nothing. A skill invoked as the
// task's first word is appended to the task's tail.
func (ch *chatHandlers) nonClaudeTurn(rt, conversationID, operatorID, agent, task string) (block, taskOut string) {
	return nonClaudeTurn(ch.transcripts, ch.yakosRoot, ch.workspaceRoot, rt, conversationID, operatorID, agent, task)
}

// nonClaudeTurn is the method's body over any transcript store and roots; the
// OpenAI-compatible endpoint (K-150) calls it through NonClaudeTurn.
func nonClaudeTurn(tr *Transcripts, yakosRoot, workspaceRoot, rt, conversationID, operatorID, agent, task string) (block, taskOut string) {
	if rt == "claude" {
		return "", task
	}
	taskOut = task
	if slug, ok := skillSlug(task); ok {
		tail, err := knowledge.SkillText(yakosRoot, workspaceRoot, slug)
		switch {
		case err == nil:
			taskOut = task + tail
		case errors.Is(err, knowledge.ErrNotFound):
		default:
			slog.Warn("consoleui: skill not appended", "skill", slug, "err", err)
		}
	}
	if yakosRoot == "" {
		return "", taskOut
	}
	pack, err := tr.EnsureKnowledge(conversationID, operatorID, func() knowledge.Pack {
		p := knowledge.Compose(knowledge.Options{
			YakosRoot: yakosRoot,
			Project:   workspaceRoot,
			Agent:     agent,
			AgentBody: resolveAgentSystemPrompt(yakosRoot, workspaceRoot, agent),
		})
		for _, w := range p.Warnings {
			slog.Warn("consoleui: knowledge pack", "note", w)
		}
		return p
	})
	if err != nil {
		slog.Warn("consoleui: knowledge pack unavailable; using the agent body", "err", err)
		return "", taskOut
	}
	return pack.Text, taskOut
}

// skillSlug returns the slug of a task that begins with /<slug> followed by
// whitespace or the end of the text.
func skillSlug(task string) (string, bool) {
	if !strings.HasPrefix(task, "/") {
		return "", false
	}
	rest := task[1:]
	end := strings.IndexAny(rest, " \t\r\n")
	if end < 0 {
		end = len(rest)
	}
	if end == 0 || end > 64 {
		return "", false
	}
	return rest[:end], true
}

type contextKnowledge struct {
	SHA   string           `json:"sha"`
	Bytes int              `json:"bytes"`
	Cap   int              `json:"cap"`
	Parts []knowledge.Part `json:"parts"`
}

type contextResponse struct {
	ConversationID string            `json:"conversationId"`
	Knowledge      *contextKnowledge `json:"knowledge"`
	Soul           string            `json:"soul,omitempty"`
	SoulNote       string            `json:"soulNote,omitempty"`
}

// soulFor returns the soul text for the requester, or "" and an optional note.
// Only the loopback host operator gets it: the soul file is the daemon host's
// own, and a networked identity (cert or session) must never read it.
func (ch *chatHandlers) soulFor(id netid.Identity) (text, note string) {
	if !ch.loopbackHost || ch.loopbackOwnerID == "" || id.Authenticated || id.OperatorID != ch.loopbackOwnerID {
		return "", ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	text = knowledge.ReadSoul(home)
	if secretscan.Redact(text) != text {
		return "", "soul omitted: it holds a secret-shaped value"
	}
	return text, ""
}

// handleChatContext serves GET /api/chat/context.
func (ch *chatHandlers) handleChatContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	conv := r.URL.Query().Get("conv")
	if conv == "" {
		http.Error(w, "conv is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("conversation_id", conv); err != nil {
		http.Error(w, "invalid conv", http.StatusBadRequest)
		return
	}
	id := netid.IdentityFrom(r.Context())
	operatorID := id.OperatorID
	if operatorID == "" {
		operatorID = r.URL.Query().Get("operatorId")
		if err := dispatch.ValidateIdentityField("operator_id", operatorID); err != nil {
			http.Error(w, "operatorId is required", http.StatusBadRequest)
			return
		}
	}

	resp := contextResponse{ConversationID: conv}
	if sha, size, parts, ok := ch.transcripts.KnowledgeInfo(conv, operatorID); ok {
		resp.Knowledge = &contextKnowledge{SHA: sha, Bytes: size, Cap: knowledge.MaxBytes, Parts: parts}
		for _, p := range parts {
			if p.Kind == knowledge.KindAgent && p.Name == "lead" {
				resp.Soul, resp.SoulNote = ch.soulFor(id)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("consoleui: context: encode response", "err", err)
	}
}
