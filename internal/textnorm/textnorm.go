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

// CRLF is Newlines' inverse: it rewrites every LF that is not already part of
// a CRLF pair. A writer that splices a freshly serialized fragment into a
// file it otherwise preserves byte for byte needs it — encoding/json and most
// encoders only ever emit LF, so patching a CRLF-authored file would leave one
// file carrying both conventions.
func CRLF(b []byte) []byte {
	if bytes.IndexByte(b, '\n') < 0 {
		return b
	}
	out := make([]byte, 0, len(b)+bytes.Count(b, []byte{'\n'}))
	for i := range b {
		if b[i] == '\n' && (i == 0 || b[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, b[i])
	}
	return out
}

// CRLFDominant reports whether b uses CRLF for most of its line breaks, so a
// writer can restore the convention the file arrived with.
func CRLFDominant(b []byte) bool {
	crlf := bytes.Count(b, []byte("\r\n"))
	return crlf > 0 && crlf*2 >= bytes.Count(b, []byte{'\n'})
}

// Equal reports whether a and b are byte-equal after newline normalization.
func Equal(a, b []byte) bool {
	return bytes.Equal(Newlines(a), Newlines(b))
}
