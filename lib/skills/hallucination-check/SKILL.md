---
name: hallucination-check
description: Citation-grounding verifier — for each claim in an LLM response, confirm support in the retrieved context and report ungrounded claims. Use when validating a RAG/LLM answer for factual grounding before trusting or shipping it.
allowed-tools: Bash Read
argument-hint: "<response-file> <context-file> [--strict] [--threshold <0..1>]"
mode: [review]
---

# Hallucination Check

## Purpose

Verify that an LLM response is grounded in the context it was given.
For each atomic claim in the response, check whether the retrieved
context actually supports that claim. Anything not supported is
flagged as ungrounded.

This is the standard RAG safety net: the model retrieves N documents,
generates an answer, and the verifier confirms the answer doesn't
invent details that weren't in the documents.

The skill is not a fact-checker against the open web — it only
checks grounding *against the provided context*. If the context is
wrong, the response can be perfectly grounded and still factually
incorrect; that's a retrieval-quality problem, not a grounding one.

## Scope

- **In:** atomic-claim extraction from the response, support-checking
  each claim against the context, structured ungrounded-claim report.
- **Out:** retrieval quality (was the right context retrieved?),
  factual correctness against ground truth (was the context itself
  right?), citation formatting (does the answer have inline `[1]`
  markers?). Those belong to retrieval-evaluator and fact-checker
  skills respectively.

Designed for `rag-architect` (debugging RAG pipelines) and
`eval-engineer` (gating release of RAG-backed features).

## When to use

- During RAG pipeline development, on a sample of responses, to find
  systematic hallucination patterns.
- As a per-response runtime check before showing answers to the user
  (high-stakes domains: medical, legal, finance).
- In CI, on a fixed eval set of `(query, retrieved_context, response)`
  triples, to catch grounding regressions across prompt or retrieval
  changes.
- When user feedback complains "the bot made up sources" —
  reproduce the case and run the check against the captured context.

## When NOT to use

- For pure-generation tasks with no retrieved context (creative
  writing, code generation from spec). There's nothing to ground
  against.
- As the *only* defense in a high-stakes RAG. Combine with retrieval
  evals, source-citation requirements, and human review.
- On responses where the LLM was explicitly allowed to use general
  knowledge (the prompt says "answer from these docs OR your training
  data"). The skill will flag training-data claims as ungrounded,
  which is correct but not actionable in that mode.

## Automated pass

1. **Extract atomic claims.** Decompose the response into single-
   proposition sentences. A "claim" is a fact assertion: subject,
   predicate, object. Lists/bullets become one claim per item.
   Hedged language ("might", "perhaps") is preserved — a hedged
   claim still needs grounding for the hedged version, just not for
   a stronger version.
   ```sh
   yakos eval extract-claims \
       --response "$RESPONSE_FILE" \
       --out /tmp/claims.jsonl
   ```

2. **For each claim, check support against the context.** The
   support-check is a smaller LLM call that returns
   `{supported: bool, evidence_span: "...", confidence: 0..1}`. The
   model must point to a verbatim span from the context as evidence,
   not paraphrase. If no span supports the claim, `supported: false`.
   ```sh
   while IFS= read -r claim; do
       yakos dispatch grounding-checker \
           --claim "$claim" \
           --context-file "$CONTEXT_FILE" \
           --json
   done < /tmp/claims.jsonl > /tmp/grounded.jsonl
   ```

3. **Apply the threshold.** Default `--threshold 0.8`: claims with
   `confidence >= 0.8 AND supported == true` are accepted; everything
   else is "ungrounded." `--strict` raises the threshold to 1.0 and
   requires evidence-span verbatim presence in the context (string
   match, not LLM judgment).

4. **Compose the report.** Markdown:
   - **Ungrounded claims** (the lede): each claim, the response line
     it came from, why it's ungrounded ("no supporting span found"
     vs. "supported span has confidence 0.6 below threshold").
   - **Grounded claims:** count + percentage. List on `--verbose`.
   - **Coverage:** % of context that was cited as evidence. Low
     coverage with high grounding = the response under-uses the
     context (not necessarily wrong; useful signal).
   - Pin block: response file hash, context file hash, threshold,
     checker model id.

5. **Exit code.** Zero if all claims are grounded above threshold.
   Non-zero if any ungrounded. Configurable per project — some
   projects accept 5% ungrounded as the cost of fluency.

## Manual pass

```sh
# 1. Print the response and context side-by-side
diff <(fold -s -w 80 "$RESPONSE_FILE") <(fold -s -w 80 "$CONTEXT_FILE")

# 2. For each sentence in the response, search the context
while IFS= read -r sentence; do
    echo "CLAIM: $sentence"
    grep -F "$(echo "$sentence" | head -c 40)" "$CONTEXT_FILE" || echo "  NOT FOUND"
done < <(yakos eval split-sentences "$RESPONSE_FILE")
```

This finds the obvious cases (verbatim copies are fine; rephrased
truths look unsupported under naive search). The automated pass uses
a checker model precisely to handle paraphrase.

## Known gotchas

- **Paraphrase vs. fabrication.** The response may correctly
  paraphrase the context — that's not a hallucination. The checker
  is asked to point to an evidence span; if the span exists and
  *entails* the claim, supported. The naive "string match" approach
  produces too many false positives on paraphrased correctness.
- **Compositional claims.** "X is Y AND Z is W" is two claims, not
  one. The extractor splits these. Beware: poorly-split claims hide
  partial grounding (X is supported, Z isn't, but the compound
  reads as one fact).
- **Context window.** If the context is larger than the checker's
  context window, the support-check may miss evidence in a chunk it
  didn't see. The skill chunks the context and runs the checker per-
  chunk, then OR-aggregates. Document the chunking in the report.
- **Negative claims.** "The document does not mention X" is a claim
  that is "supported" by absence — the checker has to verify the
  whole context, not find a span. Mark these for human review.
- **Self-grounding.** If the response contains an LLM-generated
  citation (a fake URL, a fake doi), the checker shouldn't accept
  the citation as evidence — only the *retrieved* context counts.
  The skill ignores response-internal citations.
- **Cost.** One checker call per claim. A 30-claim response =
  30 calls. Use a cheap model for the checker (haiku, gpt-5-nano);
  cost is ~$0.01–$0.05 per response. Don't run with the same
  expensive model that generated the response.
- **Checker hallucination.** The checker can also hallucinate — it
  might claim an evidence span exists that doesn't. `--strict` mode
  verifies the evidence span is a verbatim substring of the context.
  Use `--strict` when the stakes warrant.

## References

- `lib/agents/rag-architect.md` — uses this skill to debug pipelines.
- `lib/agents/eval-engineer.md` — uses this skill in CI gates.
- `lib/skills/prompt-eval/SKILL.md` — broader prompt eval; pair with
  hallucination-check for RAG-specific runs.
- `docs/rag-evaluation.md` — recommended retrieval + grounding
  metrics together.
