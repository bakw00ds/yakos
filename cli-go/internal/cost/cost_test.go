package cost

import (
	"strings"
	"testing"
)

// ---- ParseAxis ---------------------------------------------------------------

func TestParseAxis(t *testing.T) {
	cases := []struct {
		input   string
		want    Axis
		wantErr bool
	}{
		{"runtime", AxisRuntime, false},
		{"agent", AxisAgent, false},
		{"day", AxisDay, false},
		{"project", AxisProject, false},
		{"model", 0, true},
		{"", 0, true},
		{"RUNTIME", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseAxis(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseAxis(%q) expected error, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseAxis(%q) unexpected error: %v", tc.input, err)
				return
			}
			if got != tc.want {
				t.Errorf("ParseAxis(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// ---- StreamFinished ----------------------------------------------------------

func makeReader(lines ...string) *strings.Reader {
	return strings.NewReader(strings.Join(lines, "\n") + "\n")
}

func TestStreamFinished_FiltersType(t *testing.T) {
	r := makeReader(
		`{"type":"dispatch_started","ts":"2026-01-01T00:00:00Z","agent":"a","runtime":"claude"}`,
		`{"type":"dispatch_finished","ts":"2026-01-01T00:00:01Z","agent":"a","runtime":"claude","exit_code":0,"duration_s":1,"est_input_tokens":10,"est_output_tokens":5}`,
	)
	var got []Event
	for ev := range StreamFinished(r, "") {
		got = append(got, ev)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != "dispatch_finished" {
		t.Errorf("type = %q, want dispatch_finished", got[0].Type)
	}
}

func TestStreamFinished_SinceFilter(t *testing.T) {
	r := makeReader(
		`{"type":"dispatch_finished","ts":"2026-01-01T00:00:00Z","agent":"a","runtime":"claude","exit_code":0}`,
		`{"type":"dispatch_finished","ts":"2026-06-01T00:00:00Z","agent":"b","runtime":"claude","exit_code":0}`,
	)
	var got []Event
	for ev := range StreamFinished(r, "2026-05-01") {
		got = append(got, ev)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event after since filter, got %d", len(got))
	}
	if got[0].Agent != "b" {
		t.Errorf("agent = %q, want b", got[0].Agent)
	}
}

func TestStreamFinished_MalformedLines(t *testing.T) {
	r := makeReader(
		`not json`,
		`{"type":"dispatch_finished","ts":"2026-01-01T00:00:00Z","agent":"a","runtime":"claude","exit_code":0}`,
		`{"broken":`,
	)
	var got []Event
	for ev := range StreamFinished(r, "") {
		got = append(got, ev)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event (malformed skipped), got %d", len(got))
	}
}

func TestStreamFinished_EmptyInput(t *testing.T) {
	r := strings.NewReader("")
	var got []Event
	for ev := range StreamFinished(r, "") {
		got = append(got, ev)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events for empty input, got %d", len(got))
	}
}

// ---- axisKey ----------------------------------------------------------------

func TestAxisKey(t *testing.T) {
	ev := Event{
		Agent:   "myagent",
		Runtime: "claude",
		Ts:      "2026-05-15T12:34:56Z",
		Project: "/home/user/proj",
	}
	if k := axisKey(ev, AxisAgent); k != "myagent" {
		t.Errorf("AxisAgent key = %q, want myagent", k)
	}
	if k := axisKey(ev, AxisRuntime); k != "claude" {
		t.Errorf("AxisRuntime key = %q, want claude", k)
	}
	if k := axisKey(ev, AxisDay); k != "2026-05-15" {
		t.Errorf("AxisDay key = %q, want 2026-05-15", k)
	}
	if k := axisKey(ev, AxisProject); k != "/home/user/proj" {
		t.Errorf("AxisProject key = %q, want /home/user/proj", k)
	}
}

func TestAxisKey_ProjectUnknown(t *testing.T) {
	ev := Event{Project: ""}
	if k := axisKey(ev, AxisProject); k != "(unknown)" {
		t.Errorf("empty project key = %q, want (unknown)", k)
	}
}

// ---- Aggregate --------------------------------------------------------------

func evChan(events ...Event) <-chan Event {
	ch := make(chan Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch
}

func TestAggregate_BasicByAgent(t *testing.T) {
	events := []Event{
		{Agent: "a", Runtime: "claude", Ts: "2026-01-01T00:00:00Z", ExitCode: 0, DurationS: 10, EstInputTokens: 100, EstOutputTokens: 50},
		{Agent: "b", Runtime: "claude", Ts: "2026-01-01T00:00:00Z", ExitCode: 1, DurationS: 5, EstInputTokens: 200, EstOutputTokens: 100},
		{Agent: "a", Runtime: "claude", Ts: "2026-01-01T00:00:00Z", ExitCode: 0, DurationS: 3, EstInputTokens: 50, EstOutputTokens: 25},
	}
	rpt := Aggregate(evChan(events...), AxisAgent, 0)
	if rpt.Events != 3 {
		t.Errorf("events = %d, want 3", rpt.Events)
	}
	if len(rpt.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rpt.Rows))
	}
	// b has 300 total tokens; a has 225 → b should be first
	if rpt.Rows[0].Key != "b" {
		t.Errorf("rows[0].Key = %q, want b (highest tokens)", rpt.Rows[0].Key)
	}
	if rpt.Rows[1].Key != "a" {
		t.Errorf("rows[1].Key = %q, want a", rpt.Rows[1].Key)
	}
	// Verify a's aggregation
	var aRow Row
	for _, r := range rpt.Rows {
		if r.Key == "a" {
			aRow = r
		}
	}
	if aRow.Count != 2 {
		t.Errorf("a.Count = %d, want 2", aRow.Count)
	}
	if aRow.OK != 2 {
		t.Errorf("a.OK = %d, want 2", aRow.OK)
	}
	if aRow.Fail != 0 {
		t.Errorf("a.Fail = %d, want 0", aRow.Fail)
	}
	if aRow.TotalInTokens != 150 {
		t.Errorf("a.TotalInTokens = %d, want 150", aRow.TotalInTokens)
	}
	if aRow.TotalOutTokens != 75 {
		t.Errorf("a.TotalOutTokens = %d, want 75", aRow.TotalOutTokens)
	}
}

func TestAggregate_ByDay(t *testing.T) {
	events := []Event{
		{Agent: "a", Runtime: "claude", Ts: "2026-01-01T08:00:00Z", ExitCode: 0, EstInputTokens: 10},
		{Agent: "a", Runtime: "claude", Ts: "2026-01-01T18:00:00Z", ExitCode: 0, EstInputTokens: 20},
		{Agent: "a", Runtime: "claude", Ts: "2026-01-02T10:00:00Z", ExitCode: 0, EstInputTokens: 5},
	}
	rpt := Aggregate(evChan(events...), AxisDay, 0)
	if len(rpt.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rpt.Rows))
	}
	// day 2026-01-01 has 30 tokens; 2026-01-02 has 5 → 2026-01-01 first
	if rpt.Rows[0].Key != "2026-01-01" {
		t.Errorf("rows[0].Key = %q, want 2026-01-01", rpt.Rows[0].Key)
	}
}

func TestAggregate_ByProject_Unknown(t *testing.T) {
	events := []Event{
		{Agent: "a", Runtime: "claude", Ts: "2026-01-01T00:00:00Z", ExitCode: 0, Project: ""},
		{Agent: "b", Runtime: "claude", Ts: "2026-01-01T00:00:00Z", ExitCode: 0, Project: "/proj"},
	}
	rpt := Aggregate(evChan(events...), AxisProject, 0)
	keys := make(map[string]bool)
	for _, r := range rpt.Rows {
		keys[r.Key] = true
	}
	if !keys["(unknown)"] {
		t.Error("expected (unknown) key for empty project")
	}
	if !keys["/proj"] {
		t.Error("expected /proj key")
	}
}

func TestAggregate_Empty(t *testing.T) {
	ch := make(chan Event)
	close(ch)
	rpt := Aggregate(ch, AxisRuntime, 0)
	if rpt.Events != 0 {
		t.Errorf("events = %d, want 0", rpt.Events)
	}
	if len(rpt.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rpt.Rows))
	}
}

func TestAggregate_Limit(t *testing.T) {
	events := []Event{
		{Agent: "a", Runtime: "r", Ts: "2026-01-01T00:00:00Z", ExitCode: 0, EstInputTokens: 100},
		{Agent: "b", Runtime: "r", Ts: "2026-01-01T00:00:00Z", ExitCode: 0, EstInputTokens: 200},
		{Agent: "c", Runtime: "r", Ts: "2026-01-01T00:00:00Z", ExitCode: 0, EstInputTokens: 50},
	}
	rpt := Aggregate(evChan(events...), AxisAgent, 2)
	if len(rpt.Rows) != 2 {
		t.Errorf("rows with limit=2: got %d, want 2", len(rpt.Rows))
	}
}

// ---- sortRows ---------------------------------------------------------------

func TestSortRows_Descending(t *testing.T) {
	rows := []Row{
		{Key: "a", TotalInTokens: 10, TotalOutTokens: 5},
		{Key: "b", TotalInTokens: 100, TotalOutTokens: 0},
		{Key: "c", TotalInTokens: 0, TotalOutTokens: 0},
	}
	sortRows(rows)
	if rows[0].Key != "b" {
		t.Errorf("rows[0] = %q, want b", rows[0].Key)
	}
	if rows[1].Key != "a" {
		t.Errorf("rows[1] = %q, want a", rows[1].Key)
	}
	if rows[2].Key != "c" {
		t.Errorf("rows[2] = %q, want c", rows[2].Key)
	}
}

func TestSortRows_TieBreakByKey(t *testing.T) {
	// For equal token sums, tie-break is descending key (matching jq's
	// group_by + sort_by + reverse behavior in bash cost.sh).
	rows := []Row{
		{Key: "a", TotalInTokens: 10},
		{Key: "z", TotalInTokens: 10},
	}
	sortRows(rows)
	// "z" > "a" alphabetically → "z" appears first (descending key tie-break)
	if rows[0].Key != "z" {
		t.Errorf("rows[0] = %q, want z (tie-break descending key)", rows[0].Key)
	}
}

// ---- formatDurationS --------------------------------------------------------

func TestFormatDurationS(t *testing.T) {
	cases := []struct {
		d    float64
		want string
	}{
		{0, "0"},
		{10, "10"},
		{123, "123"},
		{1.5, "1.5"},
	}
	for _, tc := range cases {
		got := formatDurationS(tc.d)
		if got != tc.want {
			t.Errorf("formatDurationS(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
