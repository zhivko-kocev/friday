package drift

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetCheckRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	target := filepath.Join(dir, "out.md")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Set("claude", target, []byte("hello"))
	drifted, exists := s.Check("claude", target)
	if !exists || drifted {
		t.Errorf("after Set: drifted=%v exists=%v want false,true", drifted, exists)
	}

	// Mutate target — should drift.
	if err := os.WriteFile(target, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, _ = s.Check("claude", target)
	if !drifted {
		t.Errorf("after edit: want drifted=true")
	}
}

func TestCheckUntrackedExistingFileIsDrift(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(filepath.Join(dir, "state.json"))
	drifted, exists := s.Check("claude", target)
	if !drifted || !exists {
		t.Errorf("untracked existing file: drifted=%v exists=%v want true,true", drifted, exists)
	}
}

func TestCheckMissingFileIsNotDrift(t *testing.T) {
	s, _ := Load(filepath.Join(t.TempDir(), "state.json"))
	drifted, exists := s.Check("claude", filepath.Join(t.TempDir(), "nope.md"))
	if drifted || exists {
		t.Errorf("missing file: drifted=%v exists=%v want false,false", drifted, exists)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	target := filepath.Join(dir, "out.md")
	if err := os.WriteFile(target, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(path)
	s.Set("claude", target, []byte("k"))
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	drifted, exists := s2.Check("claude", target)
	if !exists || drifted {
		t.Errorf("after reload: drifted=%v exists=%v want false,true", drifted, exists)
	}
}

func TestSaveVacuumsMissingTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	live := filepath.Join(dir, "live.md")
	gone := filepath.Join(dir, "gone.md")
	if err := os.WriteFile(live, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gone, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(path)
	s.Set("claude", live, []byte("a"))
	s.Set("claude", gone, []byte("b"))

	// Remove the second target on disk; Save should drop its entry.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, _ := Load(path)
	if _, exists := s2.Check("claude", gone); exists {
		t.Errorf("vacuum: gone target still tracked")
	}
	if _, exists := s2.Check("claude", live); !exists {
		t.Errorf("vacuum: live target dropped (shouldn't have been)")
	}
}

func TestHashIgnoresLineEndings(t *testing.T) {
	// CRLF and LF versions of "a\nb\n" must hash identically so a Windows
	// editor doesn't trigger drift on every push.
	lf := []byte("a\nb\n")
	crlf := []byte("a\r\nb\r\n")
	if hash(lf) != hash(crlf) {
		t.Errorf("hash differs across line endings")
	}
}
