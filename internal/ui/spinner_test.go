package ui

import (
	"errors"
	"testing"
)

// Under `go test`, stdout/stdin are not terminals, so Interactive() is false
// and WithSpinner must degrade: run fn synchronously, no TUI, and surface fn's
// error unchanged. (The animated path needs a real terminal and is verified
// manually.)
func TestWithSpinnerDegradesWhenNotInteractive(t *testing.T) {
	if Interactive() {
		t.Skip("attached to a terminal; degrade path not exercised")
	}

	ran := false
	if err := WithSpinner("working", func() error { ran = true; return nil }); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ran {
		t.Fatal("fn did not run")
	}

	sentinel := errors.New("boom")
	if err := WithSpinner("failing", func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the fn's error propagated", err)
	}
}
