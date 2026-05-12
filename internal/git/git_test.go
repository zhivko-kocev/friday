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

