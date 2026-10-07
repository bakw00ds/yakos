package consoleui

// knowledge_store.go: per-conversation storage of the knowledge block (K-149).
//
// The block is composed on the first non-claude turn and kept, text in
// <conversationId>.knowledge.txt and its hash, size and part list in the meta
// file. Later turns, and turns after a restart, re-send the stored bytes
// whatever lib/rules has become since (rule:cache-stability).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/bakw00ds/yakos/internal/knowledge"
	"github.com/bakw00ds/yakos/internal/statepath"
)

var (
	sha256HexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	partNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

const maxKnowledgeParts = 256

// validKnowledgeMeta reports whether the knowledge fields of m are well formed:
// a damaged or hand-edited meta file must not reach the API or argv.
func validKnowledgeMeta(m *conversationMeta) bool {
	if m.KnowledgeSHA == "" && m.KnowledgeBytes == 0 && len(m.KnowledgeParts) == 0 {
		return true
	}
	if !sha256HexRe.MatchString(m.KnowledgeSHA) || m.KnowledgeBytes < 0 ||
		m.KnowledgeBytes > knowledge.MaxBytes || len(m.KnowledgeParts) > maxKnowledgeParts {
		return false
	}
	for _, p := range m.KnowledgeParts {
		if !partNameRe.MatchString(p.Name) || p.Bytes < 0 || p.Bytes > knowledge.MaxFileBytes ||
			(p.Kind != knowledge.KindRule && p.Kind != knowledge.KindProjectRule && p.Kind != knowledge.KindAgent) {
			return false
		}
	}
	return true
}

func (tr *Transcripts) knowledgePath(id string) (string, error) {
	mp, err := tr.metaPath(id)
	if err != nil {
		return "", err
	}
	return mp[:len(mp)-len(".meta.json")] + ".knowledge.txt", nil
}

// EnsureKnowledge returns the conversation's stored knowledge block, composing
// and storing it with compose when there is none (or the stored text no longer
// matches its hash). The operator must own the conversation's stored state.
func (tr *Transcripts) EnsureKnowledge(conversationID, operatorID string, compose func() knowledge.Pack) (knowledge.Pack, error) {
	if operatorID == "" {
		return knowledge.Pack{}, errors.New("transcript: knowledge needs an operator")
	}
	mp, err := tr.metaPath(conversationID)
	if err != nil {
		return knowledge.Pack{}, err
	}
	kp, err := tr.knowledgePath(conversationID)
	if err != nil {
		return knowledge.Pack{}, err
	}
	tr.metaMu.Lock()
	defer tr.metaMu.Unlock()
	m := readMeta(mp)
	if m.Owner != "" && m.Owner != operatorID {
		return knowledge.Pack{}, errNativeSessionOwner
	}
	if m.KnowledgeSHA != "" {
		if text, ok := readKnowledgeText(kp); ok && knowledge.SHA(text) == m.KnowledgeSHA {
			return knowledge.Pack{Text: text, SHA: m.KnowledgeSHA, Parts: m.KnowledgeParts}, nil
		}
	}
	pack := compose()
	if err := writeKnowledgeText(kp, pack.Text); err != nil {
		return knowledge.Pack{}, err
	}
	m.Owner = operatorID
	m.KnowledgeSHA, m.KnowledgeBytes, m.KnowledgeParts = pack.SHA, len(pack.Text), pack.Parts
	if err := tr.writeMeta(mp, m); err != nil {
		return knowledge.Pack{}, err
	}
	return pack, nil
}

// KnowledgeInfo returns the stored block's hash, size and parts for the
// conversation's owner; ok is false when there is none or the operator is not
// the owner.
func (tr *Transcripts) KnowledgeInfo(conversationID, operatorID string) (sha string, size int, parts []knowledge.Part, ok bool) {
	mp, err := tr.metaPath(conversationID)
	if err != nil || operatorID == "" {
		return "", 0, nil, false
	}
	tr.metaMu.Lock()
	defer tr.metaMu.Unlock()
	m := readMeta(mp)
	if m.Owner != operatorID || m.KnowledgeSHA == "" {
		return "", 0, nil, false
	}
	return m.KnowledgeSHA, m.KnowledgeBytes, m.KnowledgeParts, true
}

func readKnowledgeText(path string) (string, bool) {
	f, err := os.Open(path) //nolint:gosec // path built by knowledgePath
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, knowledge.MaxBytes+1))
	if err != nil || len(data) > knowledge.MaxBytes {
		return "", false
	}
	return string(data), true
}

func writeKnowledgeText(path, text string) error {
	dir := filepath.Dir(path)
	if err := statepath.SecureDir(dir); err != nil {
		return fmt.Errorf("transcript: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".knowledge-*.tmp") // 0600
	if err != nil {
		return fmt.Errorf("transcript: knowledge temp file: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(text)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return errors.New("transcript: write knowledge")
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return errors.New("transcript: replace knowledge")
	}
	return nil
}
