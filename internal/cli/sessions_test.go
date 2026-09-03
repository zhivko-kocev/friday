package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zhivko-kocev/friday/internal/session"
)

func entry(agent, id string, started time.Time) indexEntry {
	return indexEntry{Record: session.Record{Agent: agent, ID: id, Started: started}}
}

func TestResolveIDExactMatch(t *testing.T) {
	entries := []indexEntry{entry("claude", "abc123", time.Time{}), entry("codex", "xyz789", time.Time{})}
	got, err := resolveID(entries, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "claude" {
		t.Errorf("resolved agent = %q, want claude", got.Agent)
	}
}

func TestResolveIDPrefixMatch(t *testing.T) {
	entries := []indexEntry{entry("claude", "abc123def", time.Time{}), entry("codex", "xyz789", time.Time{})}
	got, err := resolveID(entries, "abc1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "abc123def" {
		t.Errorf("resolved id = %q, want abc123def", got.ID)
	}
}

func TestResolveIDAmbiguousPrefix(t *testing.T) {
	entries := []indexEntry{entry("claude", "abc111", time.Time{}), entry("codex", "abc222", time.Time{})}
	_, err := resolveID(entries, "abc")
	if err == nil {
		t.Fatal("want error for a prefix matching two sessions")
	}
	if !strings.Contains(err.Error(), "abc111") || !strings.Contains(err.Error(), "abc222") {
		t.Errorf("error %q must list both candidates", err.Error())
	}
}

// TestResolveIDAmbiguousExactMatch guards a real bug found during manual
// testing: two different agents' sessions can carry the exact same ID
// string (a real fixture accidentally exercised this — real IDs are UUIDs
// so it is rare, but silently returning whichever one the loop happened to
// see first would resolve `session show`/`export` to the wrong session's
// data with no indication anything was wrong).
func TestResolveIDAmbiguousExactMatch(t *testing.T) {
	entries := []indexEntry{entry("claude", "sess-1", time.Time{}), entry("copilot", "sess-1", time.Time{})}
	_, err := resolveID(entries, "sess-1")
	if err == nil {
		t.Fatal("want error when two different agents share the exact same session id")
	}
	if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "copilot") {
		t.Errorf("error %q must name both colliding agents", err.Error())
	}
}

func TestResolveIDNotFound(t *testing.T) {
	entries := []indexEntry{entry("claude", "abc123", time.Time{})}
	if _, err := resolveID(entries, "nope"); err == nil {
		t.Fatal("want error for an id with no match")
	}
}

func TestParseSinceStdlibDuration(t *testing.T) {
	d, err := parseSince("24h")
	if err != nil {
		t.Fatal(err)
	}
	if d != 24*time.Hour {
		t.Errorf("parseSince(24h) = %v, want 24h", d)
	}
}

func TestParseSinceDays(t *testing.T) {
	d, err := parseSince("7d")
	if err != nil {
		t.Fatal(err)
	}
	if d != 7*24*time.Hour {
		t.Errorf("parseSince(7d) = %v, want 168h", d)
	}
}

func TestParseSinceWeeks(t *testing.T) {
	d, err := parseSince("2w")
	if err != nil {
		t.Fatal(err)
	}
	if d != 14*24*time.Hour {
		t.Errorf("parseSince(2w) = %v, want 336h", d)
	}
}

func TestParseSinceInvalid(t *testing.T) {
	if _, err := parseSince("banana"); err == nil {
		t.Fatal("want error for an unparseable --since value")
	}
}

func TestFilterByProjectSubstring(t *testing.T) {
	entries := []indexEntry{
		{Record: session.Record{Agent: "claude", Project: "/home/dev/my-project"}},
		{Record: session.Record{Agent: "codex", Project: "/repo"}},
	}
	got := filterByProject(entries, "my-project")
	if len(got) != 1 || got[0].Agent != "claude" {
		t.Errorf("filterByProject = %+v, want only claude", got)
	}
}

func TestFilterByProjectEmptyIsNoop(t *testing.T) {
	entries := []indexEntry{{Record: session.Record{Agent: "claude"}}, {Record: session.Record{Agent: "codex"}}}
	if got := filterByProject(entries, ""); len(got) != 2 {
		t.Errorf("filterByProject with empty filter = %d entries, want 2", len(got))
	}
}

func TestFilterBySinceCutoff(t *testing.T) {
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	entries := []indexEntry{
		entry("claude", "recent", now.Add(-1*time.Hour)),
		entry("codex", "old", now.Add(-100*24*time.Hour)),
	}
	got := filterBySince(entries, now.Add(-24*time.Hour))
	if len(got) != 1 || got[0].ID != "recent" {
		t.Errorf("filterBySince = %+v, want only the recent session", got)
	}
}

// TestSortNewestFirstBreaksTiesDeterministically: --limit slices straight
// after this sort, so a single-key unstable sort made "which of two tied
// sessions survives --limit" arbitrary. Ties are ordinary here — same-second
// starts across agents, and the zero time every unparseable timestamp
// carries.
func TestSortNewestFirstBreaksTiesDeterministically(t *testing.T) {
	tie := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	newer := tie.Add(time.Hour)
	// Deliberately scrambled input, including two entries with no timestamp.
	entries := []indexEntry{
		entry("codex", "b", tie),
		entry("claude", "zzz", time.Time{}),
		entry("gemini", "newest", newer),
		entry("claude", "a", tie),
		entry("claude", "aaa", time.Time{}),
		entry("codex", "a", tie),
	}
	sortNewestFirst(entries)

	want := []string{"newest", "a", "a", "b", "aaa", "zzz"}
	wantAgents := []string{"gemini", "claude", "codex", "codex", "claude", "claude"}
	for i := range want {
		if entries[i].ID != want[i] || entries[i].Agent != wantAgents[i] {
			t.Fatalf("entry %d = %s/%s, want %s/%s", i, entries[i].Agent, entries[i].ID, wantAgents[i], want[i])
		}
	}
}

// TestDisplayTimeMakesRenderingZoneIndependent is the mixed-zone defect:
// five readers hand back UTC instants and opencode hands back local ones,
// so the same instant arriving in two Locations must render and bucket
// identically or a correctly newest-first table shows an earlier wall clock
// above a later one.
func TestDisplayTimeMakesRenderingZoneIndependent(t *testing.T) {
	utc := time.Date(2026, 8, 26, 23, 30, 0, 0, time.UTC)
	elsewhere := utc.In(time.FixedZone("UTC+5", 5*60*60))
	if !utc.Equal(elsewhere) {
		t.Fatal("fixture bug: the two values must be the same instant")
	}

	const layout = "2006-01-02 15:04"
	if got, want := displayTime(elsewhere).Format(layout), displayTime(utc).Format(layout); got != want {
		t.Errorf("same instant renders as %q and %q", got, want)
	}
	for _, group := range []string{"daily", "weekly", "monthly"} {
		got := periodKey(displayTime(elsewhere), group)
		want := periodKey(displayTime(utc), group)
		if got != want {
			t.Errorf("--group %s buckets the same instant as %q and %q", group, got, want)
		}
	}
}

func TestFirstLineTruncatesOnRuneBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		maxLen int
		want   string
	}{
		{"ascii under the limit", "hello", 10, "hello"},
		{"ascii at the limit", "hello", 5, "hello"},
		{"ascii truncated", "hello world", 5, "hello..."},
		{"multibyte truncated", "日本語のテキスト", 3, "日本語..."},
		{"emoji truncated", "🙂🙂🙂🙂", 2, "🙂🙂..."},
		{"first line only", "first\nsecond", 100, "first"},
		{"crlf leaves no carriage return", "first\r\nsecond", 100, "first"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstLine(c.text, c.maxLen)
			if got != c.want {
				t.Errorf("firstLine(%q, %d) = %q, want %q", c.text, c.maxLen, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("firstLine(%q, %d) = %q, which is not valid UTF-8", c.text, c.maxLen, got)
			}
		})
	}
}

func TestFilterByProjectIgnoresCaseAndSeparators(t *testing.T) {
	entries := []indexEntry{
		{Record: session.Record{Agent: "claude", Project: `D:\repo\MyProject`}},
		{Record: session.Record{Agent: "codex", Project: "/other/place"}},
	}
	for _, filter := range []string{"myproject", "MyProject", "D:/repo", `D:\repo`, "repo/myproject"} {
		got := filterByProject(entries, filter)
		if len(got) != 1 || got[0].Agent != "claude" {
			t.Errorf("filterByProject(%q) = %+v, want only claude", filter, got)
		}
	}
	if got := filterByProject(entries, "nowhere"); len(got) != 0 {
		t.Errorf("filterByProject(nowhere) = %+v, want nothing", got)
	}
}

func TestValidateAgentName(t *testing.T) {
	if err := validateAgentName(""); err != nil {
		t.Errorf("an empty --agent means every agent, got %v", err)
	}
	if err := validateAgentName(session.Names()[0]); err != nil {
		t.Errorf("validateAgentName(%q) = %v", session.Names()[0], err)
	}
	err := validateAgentName("not-an-agent")
	if err == nil {
		t.Fatal("want an error for an unknown agent")
	}
	if !strings.Contains(err.Error(), session.Names()[0]) {
		t.Errorf("error %q must list the known agents", err)
	}
}

// TestSessionsJSONReportsUnavailableAgents: `--json` used to emit a bare
// array, so a CI consumer seeing `[]` could not tell "no sessions exist"
// from "every reader errored" — including a real exec failure.
func TestSessionsJSONReportsUnavailableAgents(t *testing.T) {
	blob, err := json.Marshal(sessionsJSON{
		Sessions:    []session.Record{},
		Unavailable: []unavailableAgent{{Agent: "opencode", Reason: "unexpected output shape"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sessions    []session.Record `json:"sessions"`
		Unavailable []struct {
			Agent  string `json:"agent"`
			Reason string `json:"reason"`
		} `json:"unavailable"`
	}
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatal(err)
	}
	if got.Sessions == nil {
		t.Error("sessions must always be present, as [] rather than null")
	}
	if len(got.Unavailable) != 1 || got.Unavailable[0].Agent != "opencode" || got.Unavailable[0].Reason == "" {
		t.Errorf("unavailable = %+v, want the failing reader and its reason", got.Unavailable)
	}
}
