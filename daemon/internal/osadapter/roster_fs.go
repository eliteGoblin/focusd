package osadapter

import (
	"os"

	"github.com/eliteGoblin/focusd/daemon/internal/core"
)

// workdirRoster is the real rosterIO: it reads/writes the masked
// .roster file under the install's workdir via the core roster helpers
// (FEATURE 10 / ADR-0014). OS-agnostic — the masking + atomic 0600 write
// live in core, so this is just a thin path binding.
type workdirRoster struct{ store *core.Store }

func newWorkdirRoster(workdir string) workdirRoster {
	return workdirRoster{store: &core.Store{Dir: workdir}}
}

func (w workdirRoster) writeRoster(labels []string) error {
	return core.WriteRoster(w.store.RosterPath(), labels)
}

func (w workdirRoster) readRoster() ([]string, error) {
	return core.ReadRoster(w.store.RosterPath())
}

func (w workdirRoster) removeRoster() error {
	if err := os.Remove(w.store.RosterPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
