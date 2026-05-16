// Package plugin discovers, validates, and inventories external plugin
// binaries. Plugins are separately released executables (never Go .so);
// the platform only knows the executable contract.
package plugin

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// SupportedProtocols is the set of plugin protocol versions this
// platform can execute. Unknown versions are rejected and recorded.
var SupportedProtocols = []string{"1"}

// Plugin type and privilege/run-as enums (mirrors the spec manifest).
const (
	TypeJob     = "job"
	TypeService = "service" // typed-only; not executed yet

	PrivUser   = "user"
	PrivSystem = "system"

	RunAsCurrentUser = "current_user"
	RunAsSystem      = "system"
	RunAsActiveUser  = "active_user"

	// RuntimeNativeBinary is the only supported plugin runtime: the
	// platform execs the entrypoint directly. "" is treated the same.
	RuntimeNativeBinary = "native_binary"
)

// Manifest is the parsed plugin.json.
type Manifest struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	Type              string   `json:"type"`
	ProtocolVersion   string   `json:"protocol_version"`
	// Runtime is optional. "" or "native_binary" => the platform execs
	// the entrypoint directly (the only model supported today). Any
	// other value is rejected so the platform never tries to run a
	// plugin packaged for a runtime it does not understand.
	Runtime           string   `json:"runtime,omitempty"`
	Entrypoint        string   `json:"entrypoint"`
	SupportedOS       []string `json:"supported_os"`
	SupportedArch     []string `json:"supported_arch"`
	RequiredPrivilege string   `json:"required_privilege"`
	RunAs             string   `json:"run_as"`
}

// ParseManifest decodes and structurally validates plugin.json bytes.
// Structural validity is independent of the current host (that is
// checked separately during discovery against OS/arch/mode).
func ParseManifest(raw []byte) (*Manifest, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse plugin.json: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	switch {
	case m.ID == "":
		return fmt.Errorf("manifest: id is required")
	case m.Name == "":
		return fmt.Errorf("manifest %q: name is required", m.ID)
	case m.Version == "":
		return fmt.Errorf("manifest %q: version is required", m.ID)
	case m.Entrypoint == "":
		return fmt.Errorf("manifest %q: entrypoint is required", m.ID)
	}
	if m.Type != TypeJob && m.Type != TypeService {
		return fmt.Errorf("manifest %q: type must be job|service, got %q", m.ID, m.Type)
	}
	if m.Runtime != "" && m.Runtime != RuntimeNativeBinary {
		return fmt.Errorf("manifest %q: unsupported runtime %q (only %q)",
			m.ID, m.Runtime, RuntimeNativeBinary)
	}
	if m.RequiredPrivilege != PrivUser && m.RequiredPrivilege != PrivSystem {
		return fmt.Errorf("manifest %q: required_privilege must be user|system, got %q",
			m.ID, m.RequiredPrivilege)
	}
	switch m.RunAs {
	case RunAsCurrentUser, RunAsSystem, RunAsActiveUser:
	default:
		return fmt.Errorf("manifest %q: run_as must be current_user|system|active_user, got %q",
			m.ID, m.RunAs)
	}
	if len(m.SupportedOS) == 0 {
		return fmt.Errorf("manifest %q: supported_os is required", m.ID)
	}
	if len(m.SupportedArch) == 0 {
		return fmt.Errorf("manifest %q: supported_arch is required", m.ID)
	}
	return nil
}

// ProtocolSupported reports whether the platform can execute this
// manifest's protocol version.
func (m *Manifest) ProtocolSupported() bool {
	return slices.Contains(SupportedProtocols, m.ProtocolVersion)
}

// SupportsHost reports whether the manifest targets the given OS/arch.
func (m *Manifest) SupportsHost(goos, goarch string) bool {
	return slices.Contains(m.SupportedOS, goos) && slices.Contains(m.SupportedArch, goarch)
}
