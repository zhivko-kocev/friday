package cli

import (
	"testing"
	"time"

	"github.com/zhivko-kocev/friday/internal/session"
)

func TestPeriodKeyGranularities(t *testing.T) {
	ts := time.Date(2026, 8, 26, 15, 30, 0, 0, time.UTC) // a Wednesday
	cases := map[string]string{
		"daily":   "2026-08-26",
		"monthly": "2026-08",
	}
	for group, want := range cases {
		if got := periodKey(ts, group); got != want {
			t.Errorf("periodKey(%s) = %q, want %q", group, got, want)
		}
	}
	// ISO week: 2026-08-26 falls in ISO week 35 of 2026.
	if got, want := periodKey(ts, "weekly"), "2026-W35"; got != want {
		t.Errorf("periodKey(weekly) = %q, want %q", got, want)
	}
}

func TestStatRollupTopToolsDeterministicTieBreak(t *testing.T) {
	r := newRollup()
	r.add(session.Record{Tools: []string{"Bash", "Edit"}})
	r.add(session.Record{Tools: []string{"Bash", "Read"}})
	// Bash: 2, Edit: 1, Read: 1 -- Edit/Read tie on count, alphabetical order breaks it.
	got := r.topTools(3)
	want := []string{"Bash(2)", "Edit(1)", "Read(1)"}
	if len(got) != len(want) {
		t.Fatalf("topTools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("topTools[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStatRollupCostOnlyFromKnownSessions(t *testing.T) {
	r := newRollup()
	r.add(session.Record{CostKnown: true, CostUSD: 0.01})
	r.add(session.Record{CostKnown: false, CostUSD: 0}) // must never contribute, even if CostUSD happened to be nonzero garbage
	if r.CostCount != 1 {
		t.Errorf("CostCount = %d, want 1 (only the CostKnown session counts)", r.CostCount)
	}
	if r.CostUSD != 0.01 {
		t.Errorf("CostUSD = %v, want 0.01", r.CostUSD)
	}
}
