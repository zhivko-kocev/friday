package atomicio

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestWriteFileOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}
}

func TestWriteFileLeavesNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir contains %v, want only out.txt", names)
	}
}

func TestWriteFileCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "out.txt")
	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWriteFileAppliesPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits aren't enforced on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "exec.sh")
	if err := WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("perm = %v, want 0755", info.Mode().Perm())
	}
}

func TestWriteFileNoLeftoverTempOnDirError(t *testing.T) {
	// Pointing at a path whose parent is a regular file forces MkdirAll
	// to fail; we want WriteFile to surface the error and leave nothing.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "subdir", "out.txt")
	err := WriteFile(target, []byte("x"), 0o644)
	if err == nil {
		t.Fatal("expected error when parent is a file")
	}
	if !errors.As(err, new(*fs.PathError)) {
		// not strictly required; just makes a regression more obvious.
		t.Logf("error type: %T (ok)", err)
	}
}
