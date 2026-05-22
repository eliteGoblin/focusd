package osadapter

import "path/filepath"

// AppName is the runtime directory name used under every OS root.
const AppName = "focusd"

// The fixed sub-layout under a resolved base dir. Identical across OSes;
// only the base dir differs per OS/mode (computed in build-tagged files).
const (
	configFile = "config.yaml"
	pluginsDir = "plugins"
	logsDir    = "logs"
	stateDir   = "state"
)

func configPathIn(base string) string { return filepath.Join(base, configFile) }
func pluginDirIn(base string) string  { return filepath.Join(base, pluginsDir) }
func logDirIn(base string) string     { return filepath.Join(base, logsDir) }
func stateDirIn(base string) string   { return filepath.Join(base, stateDir) }
