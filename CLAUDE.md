# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**FocusD** is a productivity toolkit focused on "Freedom from Distraction" - helping users break free from addictive technology patterns (slot machine apps/websites) that harm focus and relationships.

**Current Tool: Chrome Focus (v0.1.0)**

Chrome Focus is a self-enforcing Chrome extension manager that prevents disabling productivity extensions using:
- Chrome Enterprise Managed Policies (ExtensionInstallForcelist)
- Background file watcher daemon with auto-restore
- Motivational quote barriers for disabling
- Temporary disable with auto-enable (max 1 hour)
- Obfuscated daemon process names (appears as system processes like `dbus-*`, `systemd-*`)

## Key Documents

- **Requirements**: `requirements/chrome/plugins.md` - Original feature requirements and specifications
- **Design**: `chrome/DESIGN.md` - Architecture decisions, data flow, and implementation details
- **User Guide**: `chrome/README.md` - Installation and usage instructions
- **Changelog**: `chrome/CHANGELOG.md` - Version history
- **Version**: `chrome/version.yml` - Release metadata for CI/CD

## Project Structure

```
focusd/
├── chrome/              # Chrome Focus tool
│   ├── chrome_focus.py # Main CLI (on/off/status commands)
│   ├── daemon.py       # Background file watcher
│   ├── cf              # Wrapper script
│   ├── plugins.yml     # Extension configuration (YAML)
│   ├── pyproject.toml  # Poetry dependency management (source of truth)
│   ├── install.sh      # One-command installer
│   ├── README.md       # User documentation
│   ├── DESIGN.md       # Architecture & design decisions
│   ├── CHANGELOG.md    # Version history
│   └── version.yml     # Release metadata
├── requirements/       # Requirements & specifications
│   └── chrome/
│       └── plugins.md  # Chrome Focus requirements
└── CLAUDE.md          # This file
```

## Technology Stack

- **Language**: Python 3.8+
- **Package Manager**: Poetry (local venv, not global)
- **Dependencies**: pyyaml, watchdog, requests, click, psutil, setproctitle
- **Platforms**: Ubuntu 24.04 (tested), macOS (supported)

## Installation & Usage

### Quick Start

```bash
cd chrome
./install.sh        # Installs poetry, deps, and cf wrapper to /usr/local/bin
sudo cf on          # Enable enforcement
cf status           # Check status
sudo cf off         # Disable (requires typing motivational quote)
```

### Development Setup

```bash
cd chrome
poetry install --no-root
poetry shell
python chrome_focus.py <command>
```

### File Locations

**Policy File:**
- Linux: `/etc/opt/chrome/policies/managed/managed_policies.json`
- macOS: `/Library/Google/Chrome/NativeMess agingHosts/policies/managed/managed_policies.json`

**Lock File:** `/tmp/.chrome_focus_daemon.lock`

**Wrapper:** `/usr/local/bin/cf` (in sudo's PATH)

## Key Design Decisions

### 1. Process Obfuscation
- Daemon uses `setproctitle` to appear as legitimate system process
- Random name on each start (e.g., `dbus-fkojpy-store`, `systemd-abcdef-monitor`)
- No PID or process details exposed in CLI output

### 2. Motivational Barriers
- Fetches random inspirational quotes from `api.quotable.io`
- User must type quote exactly to disable
- Creates friction to prevent impulsive disabling

### 3. Sudo Compatibility
- Wrapper script in `/usr/local/bin` (in root's PATH)
- Uses `getent passwd $SUDO_USER` to detect real user's home directory
- Works seamlessly with both `cf status` and `sudo cf on/off`

### 4. YAML Configuration
- Extensions configured in `plugins.yml` (not hardcoded)
- Easier to add/remove extensions without code changes

### 5. Poetry for Dependencies
- `pyproject.toml` is the single source of truth for dependencies
- Local venv (`.venv/`) prevents global pollution
- `poetry.lock` committed for reproducible builds

## Important Notes

- **Daemon obfuscation**: Process name randomized to prevent pattern recognition and easy termination
- **No PID exposure**: CLI output doesn't reveal daemon PID to make it harder to kill
- **Max disable**: 1 hour maximum for temporary disables
- **Cross-platform**: Paths auto-detected based on OS (Linux vs macOS)
- **Wrapper location**: `/usr/local/bin/cf` ensures sudo can find it

## Philosophy

The tools in this repo are designed around the principle that phones and websites are engineered like slot machines - designed to capture attention, create anxiety, and harm our ability to focus deeply and maintain healthy relationships. This toolkit helps users **opt out** of those manipulative patterns.
