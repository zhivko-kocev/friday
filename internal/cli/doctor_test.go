package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDoctorReportsMissingStore checks that doctor refuses to claim health
// when there's no store at all. Other paths exercise integration territory
// the engine tests already cover end-to-end.
func TestDoctorReportsMissingStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if code := cmdDoctor(nil); code != 1 {
		t.Errorf("cmdDoctor with no store returned %d, want 1", code)
	}
}

func TestDoctorOKWhenStoreExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}

	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if code := cmdDoctor(nil); code != 0 {
		t.Errorf("cmdDoctor on empty store returned %d, want 0", code)
	}
}
