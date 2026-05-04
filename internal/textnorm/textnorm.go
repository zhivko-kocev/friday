// Package textnorm provides line-ending normalization helpers used
// wherever friday compares or hashes file content. Without this, a
// Windows checkout of an LF-authored file would be flagged as drift on
// every run.
package textnorm

import "bytes"

// Newlines folds CRLF and lone CR into LF. Returns the input unchanged
// when no CR bytes are present.
func Newlines(b []byte) []byte {
	if bytes.IndexByte(b, '\r') < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '\r' {
			out = append(out, '\n')
			if i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

// Equal reports whether a and b are byte-equal after newline normalization.
func Equal(a, b []byte) bool {
	return bytes.Equal(Newlines(a), Newlines(b))
}
