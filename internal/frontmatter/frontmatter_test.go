package frontmatter

import (
	"strings"
	"testing"
)

func TestParseNoFrontmatter(t *testing.T) {
	fields, body := Parse("# heading\nbody\n")
	if fields != nil {
		t.Errorf("fields = %v, want nil", fields)
	}
	if body != "# heading\nbody\n" {
		t.Errorf("body = %q", body)
	}
}

func TestParseFrontmatter(t *testing.T) {
	src := "---\ntitle: hi\nallowed-tools: [a,b]\n---\nbody\n"
	fields, body := Parse(src)
	if fields["title"] != "hi" {
		t.Errorf("title = %v, want hi", fields["title"])
	}
	if body != "body\n" {
		t.Errorf("body = %q", body)
	}
}

func TestStripNoKeysNoOp(t *testing.T) {
	src := "---\ntitle: hi\n---\nx\n"
	if got := Strip(src, nil); got != src {
		t.Errorf("Strip with no keys mutated content")
	}
}

func TestStripPreservesUntouchedKeys(t *testing.T) {
	src := "---\ntitle: hi\nallowed-tools: [a]\n---\nbody\n"
	out := Strip(src, []string{"allowed-tools"})
	if !strings.Contains(out, "title: hi") {
		t.Errorf("dropped title; got %q", out)
	}
	if strings.Contains(out, "allowed-tools") {
		t.Errorf("kept allowed-tools; got %q", out)
	}
	if !strings.HasSuffix(out, "body\n") {
		t.Errorf("body missing; got %q", out)
	}
}

func TestStripAllKeysReturnsBareBody(t *testing.T) {
	src := "---\ntitle: hi\n---\nbody\n"
	out := Strip(src, []string{"title"})
	if out != "body\n" {
		t.Errorf("got %q, want bare body", out)
	}
}

func TestStripWithoutFrontmatterIsNoOp(t *testing.T) {
	src := "no frontmatter here\n"
	if got := Strip(src, []string{"title"}); got != src {
		t.Errorf("mutated content without frontmatter")
	}
}

func TestParseHandlesCRLF(t *testing.T) {
	src := "---\r\ntitle: hi\r\nallowed-tools: [a,b]\r\n---\r\nbody\r\n"
	fields, body := Parse(src)
	if fields == nil {
		t.Fatal("CRLF frontmatter not detected")
	}
	if fields["title"] != "hi" {
		t.Errorf("title = %v, want hi", fields["title"])
	}
	if !strings.HasPrefix(body, "body") {
		t.Errorf("body = %q, want to start with 'body'", body)
	}
}

func TestStripCRLFFrontmatter(t *testing.T) {
	src := "---\r\ntitle: hi\r\nallowed-tools: [a]\r\n---\r\nbody\r\n"
	out := Strip(src, []string{"allowed-tools"})
	if strings.Contains(out, "allowed-tools") {
		t.Errorf("Strip ignored CRLF frontmatter; got %q", out)
	}
	if !strings.Contains(out, "title: hi") {
		t.Errorf("dropped title; got %q", out)
	}
}
