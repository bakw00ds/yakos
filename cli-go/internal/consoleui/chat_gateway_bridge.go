package consoleui

// chat_gateway_bridge.go: the slice of the console's chat plumbing the OpenAI-
// compatible endpoint (internal/gateway/openai, K-150) reuses instead of a second
// copy: the secret scanner, the bounded handoff digest and the runtime-switch
// plan. Each is a thin export of the function the console chat already runs.

import "github.com/bakw00ds/yakos/internal/dispatch"

// ScanSecrets replaces anything that looks like a credential with [redacted] and
// counts the replacements (the scanner the handoff digest uses).
func ScanSecrets(s string) (string, int) { return scanSecrets(s) }

// CleanLine drops control characters from s and bounds it to max bytes.
func CleanLine(s string, max int) string { return cleanLine(s, max) }

// BuildHandoffDigest renders the latest user and assistant entries, oldest
// first, as the bounded, secret-scanned digest a new runtime's turn carries at
// its tail. from names where the earlier turns ran (at most 32 bytes are kept).
func BuildHandoffDigest(entries []TranscriptEntry, from string) (text string, turns, redactions int) {
	return buildHandoffDigest(entries, from)
}

// HandoffInfo is the public view of a runtime switch.
type HandoffInfo struct {
	From, To    string
	Turns       int
	DigestBytes int
	Redactions  int
}

// PlanHandoff is the console's runtime-switch decision over a transcript store:
// when the conversation last ran on another runtime and newRuntime has no
// native session of its own in it, the digest of the earlier turns to append to
// the task. explicit says an operator, not the router, picked newRuntime.
func PlanHandoff(tr *Transcripts, conversationID, operatorID, newRuntime string, explicit bool, taskLen int) (string, *HandoffInfo) {
	digest, hv := planHandoff(tr, conversationID, operatorID, newRuntime, explicit, taskLen)
	if hv == nil {
		return "", nil
	}
	return digest, &HandoffInfo{From: hv.From, To: hv.To, Turns: hv.Turns, DigestBytes: hv.DigestBytes, Redactions: hv.Redactions}
}

// NonClaudeTurn is the console's knowledge-pack step for a turn on rt: for codex
// and agy the stored, byte-stable pack to send as the persona (claude loads the
// rules natively and gets "") and the task with a leading /<skill> appended.
func NonClaudeTurn(tr *Transcripts, yakosRoot, workspaceRoot, rt, conversationID, operatorID, agent, task string) (block, taskOut string) {
	return nonClaudeTurn(tr, yakosRoot, workspaceRoot, rt, conversationID, operatorID, agent, task)
}

// ForgetDeadResume counts a failed turn that resumed a stored native session and
// forgets the id when the session is gone (or failures repeat), so a dead id does
// not fail every follow-up.
func ForgetDeadResume(tr *Transcripts, conversationID, operatorID, rt string, res dispatch.Result) {
	forgetDeadResume(tr, conversationID, operatorID, rt, res)
}
