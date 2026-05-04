package engine

import "github.com/zhivko-kocev/friday/internal/textnorm"

// equalNormalized reports whether two byte slices are equal after newline
// normalization. The output is used for compare-only paths; what we
// write to disk is the original content the user authored.
func equalNormalized(a, b []byte) bool {
	return textnorm.Equal(a, b)
}
