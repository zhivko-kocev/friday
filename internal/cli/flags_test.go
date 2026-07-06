package cli

import (
	"flag"
	"reflect"
	"testing"
)

func TestParseInterleavedFlagsAfterPositionals(t *testing.T) {
	// The documented promote invocation puts flags after the path:
	//   friday promote .claude/skills/new-skill --propose -m "add new-skill"
	// Stdlib Parse alone would silently swallow --propose and -m as paths.
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	propose := fs.Bool("propose", false, "")
	msg := fs.String("m", "", "")
	pos, err := parseInterleaved(fs, []string{".claude/skills/new-skill", "--propose", "-m", "add new-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if !*propose || *msg != "add new-skill" {
		t.Errorf("propose=%v m=%q — trailing flags dropped", *propose, *msg)
	}
	if !reflect.DeepEqual(pos, []string{".claude/skills/new-skill"}) {
		t.Errorf("positionals = %v", pos)
	}
}

func TestParseInterleavedDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	force := fs.Bool("force", false, "")
	pos, err := parseInterleaved(fs, []string{"a", "--force", "--", "--not-a-flag", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !*force {
		t.Error("--force before -- not parsed")
	}
	if !reflect.DeepEqual(pos, []string{"a", "--not-a-flag", "b"}) {
		t.Errorf("positionals = %v", pos)
	}
}

func TestParseInterleavedUnknownFlagErrors(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(discard{})
	if _, err := parseInterleaved(fs, []string{"a", "--bogus"}); err == nil {
		t.Error("unknown flag accepted silently")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
