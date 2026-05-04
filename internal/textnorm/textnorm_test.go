package textnorm

import "testing"

func TestNewlinesPassThroughLF(t *testing.T) {
	in := []byte("a\nb\nc\n")
	out := Newlines(in)
	if string(out) != "a\nb\nc\n" {
		t.Errorf("got %q", out)
	}
	// No CR present — Newlines should return the input slice as-is.
	if &in[0] != &out[0] {
		t.Errorf("expected zero-copy passthrough when no CR present")
	}
}

func TestNewlinesFoldsCRLF(t *testing.T) {
	out := Newlines([]byte("a\r\nb\r\nc\r\n"))
	if string(out) != "a\nb\nc\n" {
		t.Errorf("got %q", out)
	}
}

func TestNewlinesFoldsLoneCR(t *testing.T) {
	out := Newlines([]byte("a\rb\rc"))
	if string(out) != "a\nb\nc" {
		t.Errorf("got %q", out)
	}
}

func TestEqualIgnoresLineEndings(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a\nb\n", "a\r\nb\r\n", true},
		{"a\nb\n", "a\rb\r", true},
		{"a\nb", "a\nc", false},
	}
	for _, c := range cases {
		if got := Equal([]byte(c.a), []byte(c.b)); got != c.want {
			t.Errorf("Equal(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
