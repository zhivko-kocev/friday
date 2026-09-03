package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestAlignTableRowCounts is the regression guard for a genuine lipgloss/
// table v1.1.0 bug: with no header row and BorderColumn/BorderRow both
// false, BorderHeader(false) silently drops every row past the first. Each
// case below pins a row count that previously exposed it (and the
// no-rows/header-only edges around it) so a lipgloss bump that changes this
// behavior fails loudly here instead of silently truncating a doctor/status/
// sessions table in production.
func TestAlignTableRowCounts(t *testing.T) {
	cases := []struct {
		name    string
		headers []string
		rows    [][]string
		want    []string
	}{
		{
			name: "no header, single row",
			rows: [][]string{{"claude", "installed", "/x"}},
			want: []string{"claude  installed  /x"},
		},
		{
			name: "no header, three rows",
			rows: [][]string{
				{"claude", "installed", "/x"},
				{"codex", "missing", "/y"},
				{"oh-my-pi", "missing", "/z"},
			},
			want: []string{
				"claude    installed  /x",
				"codex     missing    /y",
				"oh-my-pi  missing    /z",
			},
		},
		{
			name: "no header, two rows, single column",
			rows: [][]string{{"onlyone"}, {"two"}},
			want: []string{"onlyone", "two"},
		},
		{
			name:    "with header, single row",
			headers: []string{"K", "V"},
			rows:    [][]string{{"a", "b"}},
			want:    []string{"K  V", "a  b"},
		},
		{
			name:    "with header, five rows",
			headers: []string{"K", "V"},
			rows: [][]string{
				{"r0", "x"}, {"r1", "x"}, {"r2", "x"}, {"r3", "x"}, {"r4", "x"},
			},
			want: []string{"K   V", "r0  x", "r1  x", "r2  x", "r3  x", "r4  x"},
		},
		{
			name: "no header, no rows",
			want: nil,
		},
		{
			name:    "header only, no rows",
			headers: []string{"A", "B"},
			want:    []string{"A  B"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := alignTable(c.headers, c.rows)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("alignTable(%v, %v) = %#v, want %#v", c.headers, c.rows, got, c.want)
			}
		})
	}
}

// TestAlignTableRightAlign guards the numeric-column alignment contract
// sessions.go's TURNS/token columns depend on: a column named in aligns
// right-justifies within its own computed width, matching what %6d/%10d
// produced by hand before this conversion.
func TestAlignTableRightAlign(t *testing.T) {
	got := alignTable(
		[]string{"AGENT", "ID", "TURNS"},
		[][]string{{"claude", "abc123", "4"}, {"codex", "xy", "120"}},
		lipgloss.Left, lipgloss.Left, lipgloss.Right,
	)
	want := []string{
		"AGENT   ID      TURNS",
		"claude  abc123      4",
		"codex   xy        120",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("alignTable with right-aligned column = %#v, want %#v", got, want)
	}
}

// TestAlignTableNoTrailingWhitespace guards a plain readability property: no
// returned line carries trailing spaces (lipgloss/table pads every cell to
// its column's width, including the last), which would otherwise pollute a
// redirected-to-file capture or a copy-paste.
func TestAlignTableNoTrailingWhitespace(t *testing.T) {
	lines := alignTable(nil, [][]string{
		{"short", "a-much-longer-value"},
		{"a-much-longer-name", "x"},
	})
	for _, l := range lines {
		if l != "" && (l[len(l)-1] == ' ') {
			t.Errorf("line %q has trailing whitespace", l)
		}
	}
}

// TestAlignTableOneLinePerRow pins the guarantee doctor and status index on:
// they walk the returned lines against parallel slices (installedAt[i],
// findings[i].Severity, names[i]), so a cell containing a newline used to
// make lipgloss emit an extra physical line and shift every later row onto
// the wrong entry — then panic with index out of range once the lines
// outnumbered the slice.
func TestAlignTableOneLinePerRow(t *testing.T) {
	rows := [][]string{
		{"claude", "settings.json\nhas two lines", "/x"},
		{"codex", "fine", "/y"},
		{"gemini", "trailing\r\ncrlf", "/z"},
	}
	lines := alignTable(nil, rows)
	if len(lines) != len(rows) {
		t.Fatalf("alignTable returned %d lines for %d rows: %#v", len(lines), len(rows), lines)
	}
	if !strings.Contains(lines[0], "has two lines") {
		t.Errorf("flattening dropped cell content: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "codex") {
		t.Errorf("row 1 is not the second row: %q", lines[1])
	}

	// Headers must stay aligned with flattened rows too.
	withHeader := alignTable([]string{"AGENT", "DETAIL", "PATH"}, rows)
	if len(withHeader) != len(rows)+1 {
		t.Errorf("alignTable returned %d lines for a header plus %d rows: %#v",
			len(withHeader), len(rows), withHeader)
	}
}
