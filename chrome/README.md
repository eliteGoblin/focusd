# Chrome Focus

A self-enforcing Chrome extension manager that prevents you from disabling productivity extensions.

## Features

- 🔒 **Force-install Chrome extensions** via managed policies
- 🛡️ **Auto-restore policy** if deleted (background daemon)
- 💪 **Motivational barriers** - Type inspirational quotes to disable
- ⏰ **Temporary disable** with auto-re-enable (max 1 hour)
- 🎭 **Obfuscated daemon** name to prevent easy termination
- 🖥️ **Cross-platform** - Ubuntu & macOS support

## Installation

### Quick Install (Recommended)

```bash
cd chrome
./install.sh
```

This will:
- Install Poetry (if needed)
- Set up virtual environment with dependencies
- Install `cf` command to `/usr/local/bin` (requires sudo)

### Manual Install

If you prefer manual setup:

```bash
# 1. Install Poetry
curl -sSL https://install.python-poetry.org | python3 -

# 2. Install dependencies
cd chrome
poetry install --no-root
```

## Configuration

Edit `plugins.yml` to add/remove Chrome extensions:

```yaml
plugins:
  - id: abdkjmofmjelgafcdffaimhgdgpagmop
    name: Freedom
    description: Website blocker
    link: chrome://extensions/?id=abdkjmofmjelgafcdffaimhgdgpagmop
```

## Usage

After installation, use the `cf` command:

### Commands

#### Turn ON (Enable enforcement)

```bash
sudo cf on
```

- Creates Chrome managed policy
- Starts background daemon
- Forces extensions to install
- **Restart Chrome** to apply

#### Turn OFF (Disable enforcement)

```bash
sudo cf off
```

- Prompts you to type a motivational quote
- Stops daemon and removes policy
- **Quote must be typed EXACTLY**

#### Temporary disable (auto re-enable)

```bash
sudo cf off --duration 30
```

- Disables for 30 minutes (max 60)
- Automatically re-enables after timeout
- Still requires typing motivational quote

#### Check status

```bash
cf status
```

Shows:
- Daemon status (running/stopped)
- Policy status (active/inactive)

### Alternative (Without cf wrapper)

If you didn't run `install.sh`, use poetry directly:

```bash
poetry shell
python chrome_focus.py <command>
```

## How It Works

### Chrome Managed Policies

Chrome supports enterprise policies via JSON files:

- **Linux**: `/etc/opt/chrome/policies/managed/managed_policies.json`
- **macOS**: `/Library/Google/Chrome/NativeMess agingHosts/policies/managed/managed_policies.json`

Extensions in `ExtensionInstallForcelist` are:
- Automatically installed
- Cannot be disabled by users
- Enforced until policy is removed

### Background Daemon

- Watches the policy file directory
- Auto-restores policy if deleted or cleared
- Runs with obfuscated process name (MD5-based)
- Stores PID in `~/.chrome_focus_daemon.lock`

### Motivational Barrier

- Fetches random quote from https://api.quotable.io
- Requires exact typing to disable
- Makes impulsive disabling harder
- Falls back to hardcoded quotes if API fails

## File Permissions

The policy file requires **root/sudo** access on Linux:

```bash
sudo python chrome_focus.py on
```

On macOS, you may need to grant Terminal full disk access in System Preferences.

## Troubleshooting

### Extensions not appearing in Chrome

1. Restart Chrome completely
2. Go to `chrome://policy` and verify `ExtensionInstallForcelist` is present
3. Check policy file exists: `cat /etc/opt/chrome/policies/managed/managed_policies.json`

### Daemon not starting

1. Check if already running: `cf status`
2. Check lock file: `cat /tmp/.chrome_focus_daemon.lock`
3. Kill existing daemon: `sudo cf off`, then `sudo cf on`

### Can't disable (forgot to turn on daemon)

If you enabled policy but didn't start the daemon:

```bash
sudo rm /etc/opt/chrome/policies/managed/managed_policies.json
```

Then properly turn it on:

```bash
sudo cf on
```

## Uninstall

```bash
# Turn off enforcement
sudo cf off

# Remove wrapper
sudo rm /usr/local/bin/cf

# Remove virtual environment
cd ~/devel/focusd/chrome
poetry env remove python
```

## Development

```bash
# Install dependencies
./install.sh

# Or manually
poetry install --no-root

# Use cf command
cf status
sudo cf on

# Or use poetry directly
poetry run python chrome_focus.py status
```

## Platform Notes

### Ubuntu 24.04
- Works out of the box
- Requires sudo for policy file

### macOS
- May need System Preferences → Security → Full Disk Access
- Policy path different from Linux (handled automatically)

## License

MIT
