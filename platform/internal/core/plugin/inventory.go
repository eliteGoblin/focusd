package plugin

import (
	"strings"

	"github.com/eliteGoblin/focusd/platform/internal/core/state"
)

// SyncInventory persists discovery results to the plugin inventory. Both
// accepted and rejected plugins are recorded so operators can see why a
// plugin is not running (spec AC 7, 14).
func SyncInventory(db *state.DB, discovered []Discovered) error {
	for _, d := range discovered {
		row := toRow(d)
		if err := db.Plugins.Upsert(row); err != nil {
			return err
		}
	}
	return nil
}

func toRow(d Discovered) state.Plugin {
	status := state.ValidationOK
	verr := ""
	if !d.OK {
		status = state.ValidationRejected
		verr = d.Reason
	}

	p := state.Plugin{
		Path:             d.Dir,
		ValidationStatus: status,
		ValidationError:  verr,
	}
	if m := d.Manifest; m != nil {
		p.ID = m.ID
		p.Name = m.Name
		p.Version = m.Version
		p.Type = m.Type
		p.ProtocolVersion = m.ProtocolVersion
		p.Entrypoint = m.Entrypoint
		p.SupportedOS = strings.Join(m.SupportedOS, ",")
		p.SupportedArch = strings.Join(m.SupportedArch, ",")
		p.RequiredPrivilege = m.RequiredPrivilege
		p.RunAs = m.RunAs
	}
	if p.ID == "" {
		// Unparseable manifest: key the row by its directory so the
		// rejection is still visible in the inventory.
		p.ID = "unknown:" + d.Dir
		p.Name = p.ID
	}
	return p
}
