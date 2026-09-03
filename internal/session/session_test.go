package session

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestRecordMarshalJSONCost pins the one thing recordJSON's pointer field
// exists for: "cost_usd" must be absent when the cost is unknown and
// present when a transcript really stated 0, because a ccusage-compatible
// consumer cannot tell "unknown" from "free" if both render as 0.
func TestRecordMarshalJSONCost(t *testing.T) {
	tests := []struct {
		name      string
		costUSD   float64
		costKnown bool
		wantKey   bool
		wantValue float64
	}{
		{name: "unknown cost omits the key", costUSD: 0, costKnown: false, wantKey: false},
		{name: "known free session states 0", costUSD: 0, costKnown: true, wantKey: true, wantValue: 0},
		{name: "known cost states its value", costUSD: 1.25, costKnown: true, wantKey: true, wantValue: 1.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(Record{Agent: "claude", ID: "s1", CostUSD: tt.costUSD, CostKnown: tt.costKnown})
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			value, present := got["cost_usd"]
			if present != tt.wantKey {
				t.Fatalf("cost_usd present = %v, want %v (json: %s)", present, tt.wantKey, raw)
			}
			if tt.wantKey && value != tt.wantValue {
				t.Errorf("cost_usd = %v, want %v", value, tt.wantValue)
			}
		})
	}
}

// TestTranscriptMarshalJSONKeepsTurns is the guard on Transcript's
// MarshalJSON override. Without it Record's promoted MarshalJSON wins and
// "turns" silently becomes the int count, so `friday session show --json`
// loses the entire transcript.
func TestTranscriptMarshalJSONKeepsTurns(t *testing.T) {
	at := time.Date(2026, 8, 26, 9, 59, 0, 0, time.UTC)
	tr := Transcript{
		Record: Record{Agent: "codex", ID: "s1", Turns: 2},
		Turns: []Turn{
			{Role: RoleUser, Text: "hi", At: at},
			{Role: RoleTool, Tool: "read", Files: []string{"/a.go"}, At: at},
		},
	}

	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Turns []Turn `json:"turns"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("turns did not decode as an array: %v (json: %s)", err, raw)
	}
	if len(got.Turns) != 2 {
		t.Fatalf("len(turns) = %d, want 2 (json: %s)", len(got.Turns), raw)
	}
	if got.Turns[0].Role != RoleUser || got.Turns[0].Text != "hi" {
		t.Errorf("turns[0] = %+v", got.Turns[0])
	}
	if got.Turns[1].Tool != "read" || !reflect.DeepEqual(got.Turns[1].Files, []string{"/a.go"}) {
		t.Errorf("turns[1] = %+v", got.Turns[1])
	}
	// The embedded Record's own fields must survive the override.
	var meta struct {
		Agent string `json:"agent"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Agent != "codex" || meta.ID != "s1" {
		t.Errorf("record fields = %+v, want agent=codex id=s1", meta)
	}
}

func TestSortedKeys(t *testing.T) {
	if got := sortedKeys(nil); got != nil {
		t.Errorf("sortedKeys(nil) = %v, want nil so the omitempty key disappears", got)
	}
	got := sortedKeys(map[string]struct{}{"b": {}, "a": {}, "c": {}})
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sortedKeys = %v, want %v", got, want)
	}
}

func TestSortedDistinct(t *testing.T) {
	if got := sortedDistinct(nil); got != nil {
		t.Errorf("sortedDistinct(nil) = %v, want nil", got)
	}
	got := sortedDistinct([]string{"b", "a", "b", "c", "a"})
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sortedDistinct = %v, want %v", got, want)
	}
}
