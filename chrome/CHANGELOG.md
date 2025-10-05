# Changelog

All notable changes to Chrome Focus will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2025-10-06

### Added
- Initial release of Chrome Focus
- Chrome extension enforcement via Enterprise Managed Policies
- Background daemon with file watching (auto-restores policy if deleted)
- Motivational quote barriers for disabling (fetched from quotable.io API)
- Temporary disable with auto-re-enable (max 60 minutes)
- CLI commands: `on`, `off`, `status`
- `cf` wrapper script for simplified usage
- Cross-platform support (Ubuntu 24.04 and macOS)
- Process name obfuscation using setproctitle (appears as system daemon)
- Random process names on each start (e.g., `dbus-fkojpy-store`, `systemd-abcdef-monitor`)
- No PID exposure in output to prevent easy termination
- Poetry-based dependency management with local venv
- One-command installation with `install.sh`
- Comprehensive documentation (README.md, DESIGN.md)
- YAML-based plugin configuration

### Security
- Daemon process name obfuscated to look like legitimate system processes
- Lock file in `/tmp` for cross-user compatibility with sudo
- No sensitive information (PID, process name) exposed in CLI output
- Wrapper script uses `getent` for reliable user detection with sudo

### Technical
- Built with Python 3.8+
- Dependencies: pyyaml, watchdog, requests, click, psutil, setproctitle
- Policy location: `/etc/opt/chrome/policies/managed/` (Linux), `/Library/Google/Chrome/` (macOS)
- Lock file: `/tmp/.chrome_focus_daemon.lock`
- Wrapper installed to: `/usr/local/bin/cf`

## [Unreleased]

### Planned
- Windows support
- Browser restart detection and auto-restart
- Scheduled disable windows (non-work hours)
- Desktop notifications for policy tampering attempts
- Firefox and Edge policy support
- Web dashboard for remote monitoring

[0.1.0]: https://github.com/yourusername/focusd/releases/tag/v0.1.0
