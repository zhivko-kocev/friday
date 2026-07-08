package cli

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
)

// resolveAdapterArg accepts either an adapter name or a path to an adapter's
// target dir and returns the adapter name. Shared by `pull --discover` (import)
// and any other command that takes an adapter-or-dir argument.
func resolveAdapterArg(cfg *config.Config, arg string) (string, error) {
	if _, ok := cfg.Adapters[arg]; ok {
		return arg, nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	for _, name := range cfg.AdapterNames() {
		target, err := cfg.AdapterTargetAbs(name)
		if err != nil {
			continue
		}
		if samePath(target, abs) {
			return name, nil
		}
	}
	return "", fmt.Errorf("%q is neither an adapter name nor a known target dir (defined: %v)", arg, cfg.AdapterNames())
}

// samePath compares absolute paths, case-insensitively on Windows.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
