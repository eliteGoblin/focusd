// Command gen regenerates the mac-browser-guard Python util's BLOCKLIST from
// the Go source of truth (guard.DefaultBlocklist). Invoked via `go generate
// ./...` in plugins/browser-monitor. Keeping the Python list generated is what
// stops the two implementations from drifting.
package main

import (
	"fmt"
	"os"

	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/blocklistsync"
	"github.com/eliteGoblin/focusd/plugins/browser-monitor/internal/guard"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	py, err := blocklistsync.RepoPythonPath(cwd)
	if err != nil {
		fail(err)
	}
	src, err := os.ReadFile(py)
	if err != nil {
		fail(err)
	}
	out, err := blocklistsync.Splice(src, blocklistsync.RenderPython(guard.DefaultBlocklist))
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(py, out, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("regenerated %s (%d blocklist entries)\n", py, len(guard.DefaultBlocklist))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genblocklist:", err)
	os.Exit(1)
}
