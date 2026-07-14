package osadapter

import (
	"fmt"
	"os"
	"path/filepath"
)

// DeriveWorkdir resolves the platform-workdir from the running binary's OWN
// location, independent of the (spoofed) argv[0] and without reading any
// environment variable. HF4 (FEATURE 24) disguises the platform child so a live
// `ps`/`ps -E` shows neither the workdir path on argv nor in the environment;
// the child therefore recovers its workdir here instead.
//
// The daemon installs the disguised platform binary at <workdir>/bin/<base>, so
// the workdir is exactly TWO directories up from the executable. os.Executable()
// reads the kernel's exec path (the real file the daemon launched), which the
// argv[0] spoof does not change — so this holds even though the process shows a
// generic token.
//
// NOTE: this 2-levels-up rule matches ONLY the disguised prod layout
// (bin/<base>). The legacy/e2e layout is bin/<v>/platform (3 levels); those
// builds pass the workdir explicitly via --workdir (honored only under the e2e
// build tag) and never reach here.
func DeriveWorkdir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("derive workdir: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return filepath.Dir(filepath.Dir(exe)), nil
}
