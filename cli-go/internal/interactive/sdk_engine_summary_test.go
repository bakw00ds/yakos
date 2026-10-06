package interactive

// sdk_engine_summary_test.go — K-136: the SDK sidecar's summary frame carries the
// turn's token usage (the Agent SDK's ResultMessage.usage), and the engine puts it
// on the summary chunk so the chat handler can account the turn in tokens, the
// primary unit. Before, only the dollar cost was passed on.

import (
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

func summaryChunks(t *testing.T, lines ...string) []dispatch.StreamChunk {
	t.Helper()
	var got []dispatch.StreamChunk
	e := &SDKEngine{conversationID: "conv", onChunk: func(c dispatch.StreamChunk) { got = append(got, c) }}
	for _, l := range lines {
		e.dispatchLine([]byte(l))
	}
	return got
}

func TestSDKEngine_SummaryFrameCarriesTokenUsage(t *testing.T) {
	got := summaryChunks(t, `{"v":1,"kind":"summary","totalCostUsd":0.5,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20,"service_tier":"standard"}}`)
	if len(got) != 1 || got[0].Type != "summary" || got[0].TotalCostUSD != 0.5 {
		t.Fatalf("chunks = %+v", got)
	}
	u := got[0].Usage
	if u == nil || u.InputTokens != 10 || u.OutputTokens != 5 || u.CacheRead != 100 || u.CacheCreation != 20 || u.TotalCostUSD != 0.5 {
		t.Fatalf("usage = %+v, want the frame's counts and cost", u)
	}
}

// A frame with no counts leaves Usage nil (no usage reported), and the cost is
// still passed on; hostile numbers never become counts.
func TestSDKEngine_SummaryFrameWithoutUsageStaysNil(t *testing.T) {
	for _, line := range []string{
		`{"v":1,"kind":"summary","totalCostUsd":0.25,"usage":{}}`,
		`{"v":1,"kind":"summary","totalCostUsd":0.25,"usage":null}`,
		`{"v":1,"kind":"summary","totalCostUsd":0.25}`,
		`{"v":1,"kind":"summary","totalCostUsd":0.25,"usage":"not an object"}`,
		`{"v":1,"kind":"summary","totalCostUsd":0.25,"usage":{"input_tokens":-5,"output_tokens":1e30}}`,
	} {
		got := summaryChunks(t, line)
		if len(got) != 1 || got[0].Usage != nil || got[0].TotalCostUSD != 0.25 {
			t.Errorf("%s: chunks = %+v, want Usage nil and cost 0.25", line, got)
		}
	}
	// One valid count among invalid ones is kept; the invalid ones are 0.
	got := summaryChunks(t, `{"v":1,"kind":"summary","totalCostUsd":0,"usage":{"input_tokens":-5,"output_tokens":7}}`)
	if len(got) != 1 || got[0].Usage == nil || got[0].Usage.InputTokens != 0 || got[0].Usage.OutputTokens != 7 {
		t.Errorf("chunks = %+v", got)
	}
}
