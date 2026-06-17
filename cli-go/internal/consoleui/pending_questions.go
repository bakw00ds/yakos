package consoleui

// pending_questions.go — server-side pending-question store for forged-answer
// prevention.
//
// When an ask_user_question SSE event is emitted for a conversation, the
// handler records the pending {toolUseID, question options} here.  The
// /api/chat/answer handler validates the incoming toolUseID against this store
// before forwarding the answer to the engine.
//
// # Forged-answer prevention
//
//   - toolUseID MUST match the currently-pending question for this conversation
//     (ErrNoPendingQuestion → 404 when it does not match; 404 not 400 so we
//     don't leak whether a different question is in flight).
//   - Single-use: clearing the pending entry on accept means a second answer
//     for the same question returns ErrAnswerAlreadyConsumed (→ 409).
//   - Option validation: the chosen option labels/keys must be among the ones
//     emitted in the original ask_user_question (→ 400 for unknown options).
//   - multiSelect enforcement: at most one option when multiSelect is false.
//
// # Concurrency
//
// A single sync.Mutex guards all state.  All methods are goroutine-safe.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// questionOption represents one option from an AskUserQuestion.
type questionOption struct {
	Label string `json:"label"`
	// key is an optional machine-readable alias; when present, either the
	// label or the key is accepted in the answer.
	Key string `json:"key,omitempty"`
}

// pendingQuestion holds the state for one in-flight AskUserQuestion.
type pendingQuestion struct {
	// toolUseID is the unique ID for this question invocation.
	toolUseID string

	// questions holds the structured question list decoded from questionsJSON.
	// Each element has the question text and its valid option labels/keys.
	questions []parsedQuestion

	// questionsJSON is the raw JSON echoed back to the engine in the answer
	// frame.
	questionsJSON string

	// consumed is set to true after ValidateAndConsume succeeds.
	// A subsequent call with the same toolUseID returns errPendingAlreadyConsumed (→ 409)
	// instead of errPendingNoPendingQuestion (→ 404), allowing callers to distinguish
	// a replay from a forged toolUseID.
	consumed bool
}

// parsedQuestion is one question from the ask_user_question frame.
type parsedQuestion struct {
	Text        string           `json:"text"`
	Options     []questionOption `json:"options,omitempty"`
	MultiSelect bool             `json:"multiSelect,omitempty"`
}

var (
	// errPendingNoPendingQuestion is returned when no pending question exists
	// or the toolUseID does not match.
	errPendingNoPendingQuestion = errors.New("no pending question matching this toolUseID")

	// errPendingAlreadyConsumed is returned when a pending question was already
	// answered (single-use enforcement).
	errPendingAlreadyConsumed = errors.New("pending question already answered")

	// errPendingUnknownOption is returned when the submitted answer contains an
	// option label/key that was not offered in the original question.
	errPendingUnknownOption = errors.New("answer contains unknown option")

	// errPendingMultiSelectViolation is returned when the caller submits more
	// than one answer for a single-select question.
	errPendingMultiSelectViolation = errors.New("question is single-select: at most one answer allowed")
)

// pendingQuestionStore holds the per-conversation pending question state.
type pendingQuestionStore struct {
	mu      sync.Mutex
	pending map[string]*pendingQuestion // conversationID → pending question
}

// newPendingQuestionStore allocates an empty store.
func newPendingQuestionStore() *pendingQuestionStore {
	return &pendingQuestionStore{
		pending: make(map[string]*pendingQuestion),
	}
}

// Set records a pending question for conversationID.  Any previously-pending
// question is silently replaced (the sidecar only parks one canUseTool promise
// at a time; a new ask_user_question supersedes any earlier one that was never
// answered).
func (s *pendingQuestionStore) Set(conversationID, toolUseID, questionsJSON string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	questions, err := parseQuestions(questionsJSON)
	if err != nil {
		slog.Warn("pendingQuestionStore: failed to parse questions JSON; recording with empty questions",
			"conversationID", conversationID,
			"err", err,
		)
		questions = nil
	}

	s.pending[conversationID] = &pendingQuestion{
		toolUseID:     toolUseID,
		questions:     questions,
		questionsJSON: questionsJSON,
	}
}

// Clear removes the pending question for conversationID.  No-op if none exists.
func (s *pendingQuestionStore) Clear(conversationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, conversationID)
}

// ValidateAndConsume checks that toolUseID matches the current pending question,
// validates the submitted answers against the allowed option set, enforces
// multiSelect rules, and on success returns the raw questionsJSON to be echoed
// back to the engine and clears the pending entry (single-use).
//
// On success returns the questionsJSON (may be "[]" if no options were declared).
// On failure returns one of the sentinel errors for the caller to map to HTTP status.
func (s *pendingQuestionStore) ValidateAndConsume(conversationID, toolUseID string, answers map[string]string) (questionsJSON string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pq, ok := s.pending[conversationID]
	if !ok {
		return "", errPendingNoPendingQuestion
	}

	// toolUseID must match exactly.
	if pq.toolUseID != toolUseID {
		// Don't leak what the actual pending ID is.
		return "", errPendingNoPendingQuestion
	}

	// Single-use: if this question was already consumed, return 409.
	if pq.consumed {
		return "", errPendingAlreadyConsumed
	}

	// Validate answers against declared options.
	if err := validateAnswers(pq.questions, answers); err != nil {
		return "", err
	}

	// Mark as consumed (single-use).  We keep the entry so that a retry with
	// the same toolUseID returns errPendingAlreadyConsumed (→ 409) rather than
	// errPendingNoPendingQuestion (→ 404), distinguishing a replay from a forged ID.
	qs := pq.questionsJSON
	pq.consumed = true
	return qs, nil
}

// parseQuestions decodes the questionsJSON slice from an ask_user_question frame.
func parseQuestions(questionsJSON string) ([]parsedQuestion, error) {
	if questionsJSON == "" || questionsJSON == "null" || questionsJSON == "[]" {
		return nil, nil
	}
	var qs []parsedQuestion
	if err := json.Unmarshal([]byte(questionsJSON), &qs); err != nil {
		return nil, fmt.Errorf("parseQuestions: %w", err)
	}
	return qs, nil
}

// validateAnswers checks that each answer's chosen option is among the options
// declared for its question, and enforces multiSelect constraints.
//
// When questions is nil (no options declared), all answers pass validation —
// the questions were free-text or the options JSON was unparseable.
func validateAnswers(questions []parsedQuestion, answers map[string]string) error {
	if len(questions) == 0 {
		// No option constraints declared; any answer is valid.
		return nil
	}

	// Build a per-question map from text → allowed {label, key} set.
	type allowedSet struct {
		labels      map[string]struct{}
		multiSelect bool
	}
	byText := make(map[string]allowedSet, len(questions))
	for _, q := range questions {
		allowed := make(map[string]struct{}, len(q.Options)*2)
		for _, opt := range q.Options {
			if opt.Label != "" {
				allowed[opt.Label] = struct{}{}
			}
			if opt.Key != "" {
				allowed[opt.Key] = struct{}{}
			}
		}
		byText[q.Text] = allowedSet{
			labels:      allowed,
			multiSelect: q.MultiSelect,
		}
	}

	// Validate each answer's chosen option.
	//
	// R6 (multiSelect note): answers is map[string]string, so JSON decoding
	// guarantees at most one value per question-text key.  The answerCounts
	// counter below can never exceed 1, making the "> 1" multiSelect check dead.
	// True multi-select support requires changing the wire type to
	// map[string][]string (AnswerRequest.Answers, QuestionAnswer.Answers, the
	// sdk_engine serialiser, and all tests) — that change is deferred as a
	// follow-up to avoid broad ripple in this security-gate PR.  The current
	// map[string]string type implicitly enforces single-select on the wire;
	// the multiSelect=true path is therefore partially unimplemented.
	answerCounts := make(map[string]int, len(answers))
	for questionText, chosenLabel := range answers {
		as, exists := byText[questionText]
		if !exists {
			// Unknown question text — pass (may be a free-text question not in the
			// structured list; we only enforce options for known questions).
			continue
		}
		if len(as.labels) > 0 {
			// Options were declared: validate the chosen label.
			if _, ok := as.labels[chosenLabel]; !ok {
				return fmt.Errorf("%w: question %q received option %q which is not among the declared options",
					errPendingUnknownOption, questionText, chosenLabel)
			}
		}
		answerCounts[questionText]++
		// Dead check: answerCounts[questionText] can never exceed 1 because
		// answers is map[string]string (see R6 note above).  Kept for
		// documentation intent and to make the enforcement live automatically
		// if the wire type is later upgraded to map[string][]string.
		if !as.multiSelect && answerCounts[questionText] > 1 {
			return fmt.Errorf("%w: question %q", errPendingMultiSelectViolation, questionText)
		}
	}
	return nil
}
