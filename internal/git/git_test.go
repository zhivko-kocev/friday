package git

import "testing"

func TestValidateURL(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", true},
		{"-x", true}, // would be parsed as a flag by git
		{"--upload-pack=evil", true},
		{"https://github.com/foo/bar.git", false},
		{"git@github.com:foo/bar.git", false},
		{"./local/path", false},
		{"/abs/path", false},
	}
	for _, c := range cases {
		err := ValidateURL(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateURL(%q): err=%v wantErr=%v", c.in, err, c.wantErr)
		}
	}
}

func TestIsURL(t *testing.T) {
	cases := map[string]bool{
		"https://example.com":    true,
		"git@github.com:foo/bar": true,
		"foo/bar.git":            true,
		"claude":                 false,
		"./local":                false,
	}
	for in, want := range cases {
		if got := IsURL(in); got != want {
			t.Errorf("IsURL(%q) = %v, want %v", in, got, want)
		}
	}
}
