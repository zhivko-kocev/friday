package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/session"
)

// newSessionsCommand is the read-only, cross-agent session index (D4:
// friday never writes into any agent's session store). It is the analysis
// half of "friday should be the single point where a developer analyses
// agent work and compiles a session from one agent to another" — `session
// show`/`session export` (session.go) are the per-session detail views this
// index links to.
func newSessionsCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "sessions",
		Category: "porcelain",
		Usage:    "list coding-agent sessions across every installed agent",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "agent", Usage: "only this agent (" + strings.Join(session.Names(), ", ") + ")"},
			&ucli.StringFlag{Name: "project", Usage: "only sessions whose project path contains this substring"},
			&ucli.StringFlag{Name: "since", Usage: "only sessions started within this long ago (e.g. 24h, 7d, 2w)"},
			&ucli.IntFlag{Name: "limit", Usage: "show at most N sessions (0 = no limit)"},
			&ucli.BoolFlag{Name: "json", Usage: "machine-readable output"},
			&ucli.BoolFlag{Name: "stats", Usage: "print per-agent/per-model token and cost rollups instead of the session list"},
			&ucli.StringFlag{Name: "group", Value: "session", Usage: "rollup granularity with --stats: " + strings.Join(statsGroups, ", ")},
		},
		Action: runSessions,
	}
}

func runSessions(_ context.Context, cmd *ucli.Command) error {
	if err := validateAgentName(cmd.String("agent")); err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	group := cmd.String("group")
	if err := validateGroup(group); err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	stats := cmd.Bool("stats")
	if cmd.IsSet("group") && !stats {
		output.Warn("--group only groups the --stats rollups; ignoring it for the session list")
	}

	entries, unavailable := buildIndex(cmd.String("agent"))
	entries = filterByProject(entries, cmd.String("project"))
	if since := cmd.String("since"); since != "" {
		d, err := parseSince(since)
		if err != nil {
			output.Err("%v", err)
			return ucli.Exit("", 1)
		}
		entries = filterBySince(entries, time.Now().Add(-d))
	}
	sortNewestFirst(entries)
	// --limit is applied before the --stats branch: a limit the user typed
	// must mean the same "consider only the N newest sessions" on both
	// paths, not be silently dropped on one of them.
	if limit := cmd.Int("limit"); limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	asJSON := cmd.Bool("json")
	if stats {
		return runSessionStats(entries, unavailable, group, asJSON)
	}

	if asJSON {
		records := make([]session.Record, len(entries))
		for i, e := range entries {
			records[i] = e.Record
		}
		return printJSON(sessionsJSON{Sessions: records, Unavailable: unavailable})
	}

	printSessionsTable(entries)
	printUnavailable(unavailable)
	return nil
}

// sessionsJSON is `friday sessions --json`'s shape. It is an object rather
// than a bare array so unavailable readers travel with the sessions: a
// consumer that sees only `[]` cannot tell "this machine has no sessions"
// from "every reader errored", and the second is a problem it must report.
type sessionsJSON struct {
	Sessions    []session.Record   `json:"sessions"`
	Unavailable []unavailableAgent `json:"unavailable,omitempty"`
}

// indexEntry pairs one Record with the Reader that produced it, so a
// caller that already paid for List() across every agent can Load() a
// chosen session without re-discovering which reader owns it.
type indexEntry struct {
	session.Record
	reader session.Reader
}

type unavailableAgent struct {
	Agent  string `json:"agent"`
	Reason string `json:"reason"`
}

// buildIndex calls Available/List on every reader (or just agentFilter's,
// if non-empty). A reader that is unavailable, or whose List() itself
// errors, is reported in unavailable and skipped — never fatal to the
// caller, matching every Reader's own "never fail the command" contract.
func buildIndex(agentFilter string) (entries []indexEntry, unavailable []unavailableAgent) {
	for _, r := range session.Readers() {
		if agentFilter != "" && r.Agent() != agentFilter {
			continue
		}
		if ok, reason := r.Available(); !ok {
			unavailable = append(unavailable, unavailableAgent{Agent: r.Agent(), Reason: reason})
			continue
		}
		records, err := r.List()
		if err != nil {
			unavailable = append(unavailable, unavailableAgent{Agent: r.Agent(), Reason: err.Error()})
			continue
		}
		for _, rec := range records {
			entries = append(entries, indexEntry{Record: rec, reader: r})
		}
	}
	return entries, unavailable
}

// printUnavailable reports the readers that contributed nothing. Every
// output path owes the user this line: without it, an empty result is
// indistinguishable from six broken readers.
func printUnavailable(unavailable []unavailableAgent) {
	for _, u := range unavailable {
		output.Dim("%s: unavailable (%s)", u.Agent, u.Reason)
	}
}

// validateAgentName rejects an --agent no reader answers to. The known set
// is the reader registry, never friday.yaml — sessions are discovered from
// the agents installed on this machine, not from the manifest.
func validateAgentName(agent string) error {
	if agent == "" || slices.Contains(session.Names(), agent) {
		return nil
	}
	return fmt.Errorf("unknown agent %q (known: %s)", agent, strings.Join(session.Names(), ", "))
}

func filterByProject(entries []indexEntry, project string) []indexEntry {
	if project == "" {
		return entries
	}
	needle := normalizeProject(project)
	out := entries[:0:0]
	for _, e := range entries {
		if strings.Contains(normalizeProject(e.Project), needle) {
			out = append(out, e)
		}
	}
	return out
}

// normalizeProject makes --project match what a user actually types.
// Readers do not agree on spelling: claude decodes a slug into a POSIX-
// looking path while copilot and opencode surface the workspace path as the
// agent recorded it, so the same directory shows up as both `D:\repo\friday`
// and `/d/repo/friday` — and Windows paths are case-insensitive anyway.
func normalizeProject(p string) string {
	return strings.ToLower(filepath.ToSlash(p))
}

func filterBySince(entries []indexEntry, cutoff time.Time) []indexEntry {
	out := entries[:0:0]
	for _, e := range entries {
		if !e.Started.Before(cutoff) {
			out = append(out, e)
		}
	}
	return out
}

// parseSince extends time.ParseDuration with day/week suffixes it doesn't
// support natively (stdlib tops out at "h") — "7d"/"2w" are the units a
// --since flag is actually typed with day to day.
func parseSince(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if days, err := strconv.Atoi(n); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	if n, ok := strings.CutSuffix(s, "w"); ok {
		if weeks, err := strconv.Atoi(n); err == nil {
			return time.Duration(weeks) * 7 * 24 * time.Hour, nil
		}
	}
	return 0, fmt.Errorf("invalid --since duration %q (examples: 24h, 7d, 2w)", s)
}

// sortNewestFirst orders the index newest first, breaking ties by agent
// then id. Ties are not exotic — same-second starts across agents, and the
// zero time every session with an unparseable timestamp carries — and
// --limit slices right after this sort, so without a total order which of
// two tied sessions survives `--limit 10` would be arbitrary and could
// change when an unrelated session is added elsewhere in the slice.
func sortNewestFirst(entries []indexEntry) {
	slices.SortStableFunc(entries, func(a, b indexEntry) int {
		if c := b.Started.Compare(a.Started); c != 0 {
			return c
		}
		if c := strings.Compare(a.Agent, b.Agent); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// resolveID finds the one entry whose ID equals or is uniquely prefixed by
// id. Two or more sessions sharing the exact same ID (a cross-agent
// coincidence — real IDs are UUIDs, so this is vanishingly unlikely, but
// silently picking the first would be a wrong-session bug, not a
// convenience) is reported as ambiguous exactly like a shared prefix.
func resolveID(entries []indexEntry, id string) (indexEntry, error) {
	var exact, prefix []indexEntry
	for _, e := range entries {
		switch {
		case e.ID == id:
			exact = append(exact, e)
		case strings.HasPrefix(e.ID, id):
			prefix = append(prefix, e)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return indexEntry{}, ambiguousSessionErr(id, exact)
	}
	switch len(prefix) {
	case 0:
		return indexEntry{}, fmt.Errorf("no session matches id %q", id)
	case 1:
		return prefix[0], nil
	default:
		return indexEntry{}, ambiguousSessionErr(id, prefix)
	}
}

func ambiguousSessionErr(id string, matches []indexEntry) error {
	lines := make([]string, len(matches))
	for i, m := range matches {
		lines[i] = fmt.Sprintf("%s (%s)", m.ID, m.Agent)
	}
	sort.Strings(lines)
	return fmt.Errorf("ambiguous id %q matches %d sessions:\n  %s", id, len(matches), strings.Join(lines, "\n  "))
}

// displayTime is the single place a session timestamp changes zone.
// Readers hand back a mix: five parse RFC3339 strings ending in "Z" and so
// carry time.UTC, while opencode's time.UnixMilli is documented to be
// local. Any view that prints or buckets a wall clock without a zone
// suffix must normalize first, or two sessions started at the same instant
// render hours apart and can land in different --group buckets. Views that
// print RFC3339 (which carries its own offset) are already unambiguous and
// deliberately left alone, so `session export` stays byte-stable.
func displayTime(t time.Time) time.Time { return t.Local() }

func printSessionsTable(entries []indexEntry) {
	if len(entries) == 0 {
		output.Dim("no sessions found")
		return
	}
	rows := make([][]string, len(entries))
	for i, e := range entries {
		rows[i] = []string{
			e.Agent, shortID(e.ID, 12), displayTime(e.Started).Format("2006-01-02 15:04"),
			strconv.Itoa(e.Turns), valueOr(e.Model, "-"), e.Project,
		}
	}
	lines := alignTable(
		[]string{"AGENT", "ID", "STARTED (LOCAL)", "TURNS", "MODEL", "PROJECT"}, rows,
		lipgloss.Left, lipgloss.Left, lipgloss.Left, lipgloss.Right, lipgloss.Left, lipgloss.Left,
	)
	for _, line := range lines {
		output.Line(output.LevelInfo, "%s", line)
	}
}

func shortID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// firstLine reduces transcript text to one display line of at most maxLen
// runes. Both cuts are rune-aware on purpose: transcripts routinely carry
// CJK, Cyrillic and emoji, and a byte-offset slice would sever a multi-byte
// sequence — leaving invalid UTF-8 in a table row and, worse, in the
// markdown file `session export` writes. The newline search accepts either
// terminator so a CRLF transcript doesn't leave a stray carriage return
// mid-row.
func firstLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	runes := 0
	for i := range s { // ranging a string yields rune start offsets
		if runes == maxLen {
			return s[:i] + "..."
		}
		runes++
	}
	return s
}
