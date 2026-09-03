package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/session"
)

// statsGroups is the set of --group values periodKey has a bucket for, and
// the single source of truth for both the flag's usage text and its
// validation.
var statsGroups = []string{"session", "daily", "weekly", "monthly"}

// validateGroup rejects a --group typo instead of silently falling through
// to session granularity, which would hand the user the wrong rollup with
// no hint the requested granularity was dropped.
func validateGroup(group string) error {
	if slices.Contains(statsGroups, group) {
		return nil
	}
	return fmt.Errorf("unknown --group %q (want %s)", group, strings.Join(statsGroups, ", "))
}

// statRollup accumulates the ccusage/OTel-GenAI vocabulary token/cost
// fields (D28) plus a tool-frequency count across however many sessions
// share whatever key a caller is grouping by (agent, model, or period).
type statRollup struct {
	Sessions                                                                     int
	Turns                                                                        int
	InputTokens, OutputTokens, CacheCreationTokens, CacheReadTokens, TotalTokens int
	CostUSD                                                                      float64
	CostCount                                                                    int // sessions in this bucket whose transcript stated a cost
	ToolCounts                                                                   map[string]int
}

func newRollup() *statRollup { return &statRollup{ToolCounts: map[string]int{}} }

func (r *statRollup) add(rec session.Record) {
	r.Sessions++
	r.Turns += rec.Turns
	r.InputTokens += rec.InputTokens
	r.OutputTokens += rec.OutputTokens
	r.CacheCreationTokens += rec.CacheCreationTokens
	r.CacheReadTokens += rec.CacheReadTokens
	r.TotalTokens += rec.TotalTokens
	if rec.CostKnown {
		r.CostUSD += rec.CostUSD
		r.CostCount++
	}
	for _, t := range rec.Tools {
		r.ToolCounts[t]++
	}
}

// topTools returns at most n tool names, most-used first, ties broken
// alphabetically for a deterministic, testable order.
func (r *statRollup) topTools(n int) []string {
	type kv struct {
		name  string
		count int
	}
	list := make([]kv, 0, len(r.ToolCounts))
	for k, v := range r.ToolCounts {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].name < list[j].name
	})
	if len(list) > n {
		list = list[:n]
	}
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = fmt.Sprintf("%s(%d)", e.name, e.count)
	}
	return out
}

func rollupBy(entries []indexEntry, keyOf func(indexEntry) string) (order []string, rollups map[string]*statRollup) {
	rollups = map[string]*statRollup{}
	for _, e := range entries {
		key := keyOf(e)
		if rollups[key] == nil {
			rollups[key] = newRollup()
			order = append(order, key)
		}
		rollups[key].add(e.Record)
	}
	sort.Strings(order)
	return order, rollups
}

// periodKey buckets a timestamp to the calendar period --group asks for.
// weekly uses ISO-8601 week numbering (the same "which week is this"
// definition Go's own time.ISOWeek implements), matching the granularity
// set ccusage established. Callers pass an already zone-normalized time
// (see displayTime): a calendar day is only meaningful in one zone.
func periodKey(t time.Time, group string) string {
	switch group {
	case "daily":
		return t.Format("2006-01-02")
	case "weekly":
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case "monthly":
		return t.Format("2006-01")
	default:
		return ""
	}
}

// runSessionStats prints the token/cost rollups. group is already validated
// by validateGroup at the CLI boundary, so "session" (or an unset value)
// simply means "no time bucketing".
func runSessionStats(entries []indexEntry, unavailable []unavailableAgent, group string, asJSON bool) error {
	agentOrder, agentRollups := rollupBy(entries, func(e indexEntry) string { return e.Agent })
	modelOrder, modelRollups := rollupBy(entries, func(e indexEntry) string { return valueOr(e.Model, "(unknown)") })

	var periodOrder []string
	var periodRollups map[string]*statRollup
	switch group {
	case "", "session":
		// No time bucketing: per-agent/per-model rollups above already are
		// the full requested view at session granularity.
	default:
		periodOrder, periodRollups = rollupBy(entries, func(e indexEntry) string {
			return periodKey(displayTime(e.Started), group)
		})
	}

	if asJSON {
		out := struct {
			ByAgent     []statRollupJSON   `json:"by_agent"`
			ByModel     []statRollupJSON   `json:"by_model"`
			ByPeriod    []statRollupJSON   `json:"by_period,omitempty"`
			Unavailable []unavailableAgent `json:"unavailable,omitempty"`
		}{
			ByAgent:     toRollupJSON(agentOrder, agentRollups),
			ByModel:     toRollupJSON(modelOrder, modelRollups),
			Unavailable: unavailable,
		}
		if periodRollups != nil {
			out.ByPeriod = toRollupJSON(periodOrder, periodRollups)
		}
		return printJSON(out)
	}

	printRollupTable("by agent:", agentOrder, agentRollups)
	printRollupTable("by model:", modelOrder, modelRollups)
	if periodRollups != nil {
		printRollupTable(fmt.Sprintf("by %s:", group), periodOrder, periodRollups)
	}
	printUnavailable(unavailable)
	return nil
}

func printRollupTable(title string, order []string, rollups map[string]*statRollup) {
	output.Header(title)
	if len(order) == 0 {
		output.Dim("no sessions")
		return
	}
	rows := make([][]string, len(order))
	for i, k := range order {
		r := rollups[k]
		note := strings.Join(r.topTools(3), " ")
		if r.CostCount > 0 {
			note = fmt.Sprintf("$%.4f(n=%d) %s", r.CostUSD, r.CostCount, note)
		}
		rows[i] = []string{
			k, strconv.Itoa(r.Sessions), strconv.Itoa(r.Turns), strconv.Itoa(r.InputTokens),
			strconv.Itoa(r.OutputTokens), strconv.Itoa(r.CacheCreationTokens), strconv.Itoa(r.CacheReadTokens),
			strconv.Itoa(r.TotalTokens), note,
		}
	}
	lines := alignTable(
		[]string{"KEY", "SESSIONS", "TURNS", "IN_TOK", "OUT_TOK", "CACHE_C", "CACHE_R", "TOTAL", "COST / TOP TOOLS"}, rows,
		lipgloss.Left, lipgloss.Right, lipgloss.Right, lipgloss.Right, lipgloss.Right, lipgloss.Right, lipgloss.Right, lipgloss.Right, lipgloss.Left,
	)
	for _, line := range lines {
		output.Line(output.LevelInfo, "%s", line)
	}
}

// statRollupJSON is a statRollup's on-the-wire shape. costUSD/costCount are
// unexported (encoding/json never marshals unexported fields) and only
// populated via toRollupJSON's CostCount check, so cost_usd is omitted
// entirely for a bucket with zero cost-stating sessions rather than ever
// writing a misleading 0 — the same reasoning as Record's own MarshalJSON.
type statRollupJSON struct {
	Key                                                                          string
	Sessions                                                                     int
	Turns                                                                        int
	InputTokens, OutputTokens, CacheCreationTokens, CacheReadTokens, TotalTokens int
	TopTools                                                                     []string
	costUSD                                                                      *float64
	costCount                                                                    int
}

// MarshalJSON renders the snake_case wire shape and omits cost_usd/
// cost_session_count together whenever costUSD is nil.
func (s statRollupJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Key                 string   `json:"key"`
		Sessions            int      `json:"sessions"`
		Turns               int      `json:"turns"`
		InputTokens         int      `json:"input_tokens"`
		OutputTokens        int      `json:"output_tokens"`
		CacheCreationTokens int      `json:"cache_creation_tokens"`
		CacheReadTokens     int      `json:"cache_read_tokens"`
		TotalTokens         int      `json:"total_tokens"`
		CostUSD             *float64 `json:"cost_usd,omitempty"`
		CostSessionCount    int      `json:"cost_session_count,omitempty"`
		TopTools            []string `json:"top_tools,omitempty"`
	}{
		Key:                 s.Key,
		Sessions:            s.Sessions,
		Turns:               s.Turns,
		InputTokens:         s.InputTokens,
		OutputTokens:        s.OutputTokens,
		CacheCreationTokens: s.CacheCreationTokens,
		CacheReadTokens:     s.CacheReadTokens,
		TotalTokens:         s.TotalTokens,
		CostUSD:             s.costUSD,
		CostSessionCount:    s.costCount,
		TopTools:            s.TopTools,
	})
}

func toRollupJSON(order []string, rollups map[string]*statRollup) []statRollupJSON {
	out := make([]statRollupJSON, 0, len(order))
	for _, k := range order {
		r := rollups[k]
		row := statRollupJSON{
			Key:                 k,
			Sessions:            r.Sessions,
			Turns:               r.Turns,
			InputTokens:         r.InputTokens,
			OutputTokens:        r.OutputTokens,
			CacheCreationTokens: r.CacheCreationTokens,
			CacheReadTokens:     r.CacheReadTokens,
			TotalTokens:         r.TotalTokens,
			TopTools:            r.topTools(5),
		}
		if r.CostCount > 0 {
			cost := r.CostUSD
			row.costUSD = &cost
			row.costCount = r.CostCount
		}
		out = append(out, row)
	}
	return out
}
