package cli

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// pluginLockName is the provenance record for git-fetched plugins. It lives in
// the plugins dir so it versions alongside the presets it pins.
const pluginLockName = "friday.lock"

// pluginLock pins each fetched plugin to the repo and commit it came from, so a
// team gets reproducible renders and `friday plugin list --urls` can show
// provenance. Plugins dropped in by hand simply have no entry.
type pluginLock struct {
	Plugins map[string]pluginPin `yaml:"plugins"`
}

// pluginPin is one plugin's origin: the repo URL, the resolved commit, and the
// preset file installed under plugins/.
type pluginPin struct {
	URL  string `yaml:"url"`
	SHA  string `yaml:"sha"`
	File string `yaml:"file"`
}

func pluginLockPath(storeDir string) string {
	return filepath.Join(storeDir, presets.PluginsDirName, pluginLockName)
}

// loadPluginLock reads the lock, or returns an empty one if absent/unreadable
// (a hand-managed plugins dir has no lock — that is not an error).
func loadPluginLock(storeDir string) pluginLock {
	lock := pluginLock{Plugins: map[string]pluginPin{}}
	data, err := os.ReadFile(pluginLockPath(storeDir))
	if err != nil {
		return lock
	}
	_ = yaml.Unmarshal(data, &lock)
	if lock.Plugins == nil {
		lock.Plugins = map[string]pluginPin{}
	}
	return lock
}

func (l pluginLock) save(storeDir string) error {
	data, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	return atomicio.WriteFile(pluginLockPath(storeDir), data, 0o644)
}
