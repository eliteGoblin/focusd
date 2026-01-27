# Chrome Focus - Design Document

## Overview

Chrome Focus is a self-enforcing productivity tool that prevents users from disabling Chrome extensions by leveraging Chrome's Enterprise Managed Policies. It includes a background daemon that monitors and auto-restores the policy file if tampered with.

## Architecture

### Components

```
chrome/
├── chrome_focus.py      # Main CLI interface
├── daemon.py            # Background file watcher
├── plugins.yml          # Extension configuration
├── pyproject.toml       # Poetry dependencies
├── README.md            # User documentation
└── DESIGN.md            # This file
```

### Design Decisions

#### 1. **Chrome Managed Policies**

**Why:** Chrome's `ExtensionInstallForcelist` policy is designed for enterprise environments and cannot be bypassed by users through the Chrome UI.

**How it works:**
- Chrome reads JSON policy files from specific system directories
- Extensions in the force-list are automatically installed and cannot be disabled
- Policy files require root/admin privileges to modify

**Platform-specific paths:**
- Linux: `/etc/opt/chrome/policies/managed/`
- macOS: `/Library/Google/Chrome/NativeMess agingHosts/policies/managed/`

#### 2. **Background Daemon with Auto-Restore**

**Problem:** Users can delete the policy JSON file to disable enforcement.

**Solution:** A background daemon using Python's `watchdog` library monitors the policy directory and immediately recreates the file if deleted or cleared.

**Features:**
- Watches for `on_deleted` and `on_modified` events
- Detects policy file deletion or tampering (empty/cleared content)
- Auto-restores within ~0.5 seconds
- Runs with obfuscated process name

#### 3. **Obfuscated Daemon Process**

**Problem:** Users can kill the daemon process if they know its name.

**Solution:** Generate a unique, non-obvious process name based on the username's MD5 hash.

**Implementation:**
```python
hash_obj = hashlib.md5(username.encode())
daemon_name = f"systemd-{hash_obj.hexdigest()[:8]}-monitor"
```

**Example names:**
- User "frank" → `systemd-1a2b3c4d-monitor`
- User "john" → `systemd-5e6f7g8h-monitor`

This makes the daemon look like a legitimate system service.

#### 4. **Motivational Quote Barrier**

**Problem:** Impulsive disabling defeats the purpose of focus tools.

**Solution:** Require users to type a full inspirational quote before disabling.

**Features:**
- Fetches random quotes from `api.quotable.io`
- Requires exact character match (including punctuation)
- Fallback to hardcoded quotes if API fails
- Makes disabling a deliberate, not impulsive, action

**Psychology:** The act of typing a motivational quote:
1. Creates a time delay (reduces impulsivity)
2. Reminds users of their goals
3. Makes disabling effortful enough to reconsider

#### 5. **Temporary Disable with Auto-Enable**

**Use case:** Legitimate breaks where extensions need to be off.

**Features:**
- `--duration` flag (max 60 minutes)
- Blocks current terminal during wait period
- Automatically re-enables after timeout
- Still requires motivational quote to activate

**Implementation:**
```python
time.sleep(duration * 60)  # Block terminal
create_chrome_policy()     # Auto re-enable
start_daemon()
```

#### 6. **Cross-Platform Support**

**Design:** Abstract platform differences behind helper functions.

```python
def get_chrome_policy_path() -> Path:
    if sys.platform == "darwin":  # macOS
        return Path("/Library/Google/Chrome/...")
    else:  # Linux
        return Path("/etc/opt/chrome/policies/managed")
```

**Benefits:**
- Single codebase for Ubuntu and macOS
- Easy to add Windows support later
- Clean separation of platform logic

#### 7. **Poetry & Virtual Environments**

**Requirement:** Don't pollute global Python environment.

**Solution:** Use Poetry for dependency management with local venv.

**Benefits:**
- Isolated dependencies in `.venv/`
- Reproducible builds with lock file
- Easy distribution with `pyproject.toml`
- No system-wide package installation

**Setup:**
```bash
poetry install --no-root  # Creates .venv locally
poetry shell              # Activates venv
```

## Security Considerations

### Permission Requirements

- **Linux:** Requires `sudo` to write to `/etc/opt/chrome/`
- **macOS:** May need Full Disk Access in System Preferences

### Daemon PID Tracking

- PID stored in `~/.chrome_focus_daemon.lock` (hidden file)
- Used to check if daemon is running
- Prevents duplicate daemon instances

### Process Management

- Uses `psutil.pid_exists()` to verify daemon status
- Graceful shutdown with `SIGTERM` signal
- Cleanup of lock file on stop

## Data Flow

### Enable Flow (`on` command)

```
1. Load plugins.yml
2. Generate policy JSON with extension IDs
3. Write to /etc/opt/chrome/policies/managed/managed_policies.json
4. Start daemon.py in background
5. Save daemon PID to lock file
```

### Disable Flow (`off` command)

```
1. Fetch motivational quote from API
2. Display quote to user
3. Validate user input (exact match)
4. Stop daemon (kill PID)
5. Remove policy JSON file
6. (Optional) Sleep for duration, then re-enable
```

### Daemon Watch Loop

```
1. Load plugins.yml
2. Ensure policy file exists
3. Start watchdog observer
4. On file deletion:
   - Sleep 0.5s (avoid race condition)
   - Recreate policy file
5. On file modification:
   - Check if content is empty/cleared
   - Recreate if tampered
```

## Extension Configuration

### plugins.yml Structure

```yaml
plugins:
  - id: <32-char-extension-id>
    name: <Human-readable name>
    description: <What it does>
    link: chrome://extensions/?id=<id>
```

### Chrome Extension ID Format

- 32 lowercase letters (a-p)
- Example: `abdkjmofmjelgafcdffaimhgdgpagmop`
- Uniquely identifies extension in Chrome Web Store

### Policy JSON Format

```json
{
  "ExtensionInstallForcelist": [
    "abdkjmofmjelgafcdffaimhgdgpagmop",
    "cfhdojbkjhnklbpkdaibdccddilifddb"
  ]
}
```

## Future Enhancements

### Potential Features

1. **Windows Support**
   - Policy path: `HKEY_LOCAL_MACHINE\SOFTWARE\Policies\Google\Chrome`
   - Registry-based policy management

2. **Whitelist Hours**
   - Allow automatic disable during non-work hours
   - Cron-based scheduling

3. **Browser Restart Detection**
   - Auto-restart Chrome when policy changes
   - Use AppleScript (macOS) or xdotool (Linux)

4. **Tamper Notifications**
   - Send desktop notification when policy is restored
   - Log tampering attempts

5. **Multi-Browser Support**
   - Firefox policy enforcement
   - Edge policy management

6. **Web Dashboard**
   - View enforcement status remotely
   - Schedule disable windows

## Testing Strategy

### Manual Testing

1. **Policy Creation**
   ```bash
   python chrome_focus.py on
   cat /etc/opt/chrome/policies/managed/managed_policies.json
   ```

2. **Extension Enforcement**
   - Restart Chrome
   - Go to `chrome://extensions`
   - Verify extensions installed and greyed out

3. **Daemon Auto-Restore**
   ```bash
   sudo rm /etc/opt/chrome/policies/managed/managed_policies.json
   # Wait 1 second
   cat /etc/opt/chrome/policies/managed/managed_policies.json
   # Should be restored
   ```

4. **Motivational Quote**
   ```bash
   python chrome_focus.py off
   # Type quote incorrectly → should fail
   # Type quote correctly → should disable
   ```

5. **Temporary Disable**
   ```bash
   python chrome_focus.py off --duration 1
   # Wait 1 minute
   # Should auto re-enable
   ```

### Edge Cases

- Policy directory doesn't exist → Should create
- Daemon already running → Should not duplicate
- API quota exceeded → Should use fallback quotes
- Duration > 60 minutes → Should reject
- Chrome not installed → Policy created anyway (ready for install)

## Dependencies

| Package | Purpose | Version |
|---------|---------|---------|
| pyyaml | Parse plugins.yml | ^6.0 |
| watchdog | File system monitoring | ^3.0.0 |
| requests | Fetch quotes from API | ^2.31.0 |
| click | CLI framework | ^8.1.0 |
| psutil | Process management | ^5.9.0 |

## References

- [Chrome Enterprise Policies](https://chromeenterprise.google/policies/)
- [ExtensionInstallForcelist Policy](https://chromeenterprise.google/policies/#ExtensionInstallForcelist)
- [Watchdog Documentation](https://python-watchdog.readthedocs.io/)
- [Quotable API](https://github.com/lukePeavey/quotable)
