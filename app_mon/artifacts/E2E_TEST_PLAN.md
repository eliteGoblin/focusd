# appmon E2E Test Plan

This document lists all features that must be verified before release.
Run through each test case and mark as PASS/FAIL.

---

## 1. CLI Commands

### 1.1 Version Command
```bash
./build/appmon version
./build/appmon version --json
```
- [ ] Shows version, commit, build time
- [ ] JSON output is valid JSON with version field

### 1.2 Start Command
```bash
./build/appmon start
```
- [ ] Creates binary backups with version info
- [ ] Installs LaunchAgent plist
- [ ] Starts watcher and guardian daemons
- [ ] Shows "PROTECTED" status

### 1.3 Status Command
```bash
./build/appmon status
```
- [ ] Shows RUNNING when daemons are alive
- [ ] Shows DEGRADED if one daemon is down
- [ ] Shows NOT RUNNING when no daemons

### 1.4 List Command
```bash
./build/appmon list
```
- [ ] Lists Steam policy with paths and processes
- [ ] Lists Dota2 policy with paths and processes

### 1.5 Scan Command
```bash
./build/appmon scan
```
- [ ] Runs enforcement immediately
- [ ] Kills blocked processes
- [ ] Deletes blocked paths
- [ ] Reports results

---

## 2. Steam Blocking

### 2.1 Path Deletion
```bash
brew install --cask steam
./build/appmon scan
ls /Applications/Steam.app  # Should not exist
brew list --cask | grep steam  # Should be empty
```
- [ ] Deletes /Applications/Steam.app
- [ ] Deletes /opt/homebrew/Caskroom/steam
- [ ] Steam removed from brew list

### 2.2 Process Killing
- [ ] Kills Steam process if running (manual test)

---

## 3. Self-Protection

### 3.1 Binary Backups
```bash
./build/appmon start
# Check backups exist
ls ~/.config/.com.apple.helper.*/.helper
ls ~/.local/share/.system.cache.*/.helper
ls /var/tmp/.cf_service_*/.helper
```
- [ ] Backups created in 3 locations
- [ ] SHA256 matches original

### 3.2 Version-Aware Restore (THE BUG FIX)
```bash
# Step 1: Start with v0.1.0
make build VERSION=0.1.0
./build/appmon start

# Step 2: Build new version
make build VERSION=0.2.0

# Step 3: Wait 60+ seconds for daemon check
sleep 70

# Step 4: Verify new binary NOT overwritten
./build/appmon version
# Should show v0.2.0, NOT v0.1.0
```
- [ ] Daemon detects newer version
- [ ] Does NOT restore old backup
- [ ] Updates backups with new version

### 3.3 Restore on Deletion
```bash
# Start appmon
./build/appmon start

# Delete the binary
rm ./build/appmon

# Wait for restore
sleep 70

# Check if restored
ls ./build/appmon  # Should exist again
```
- [ ] Binary restored when deleted

### 3.4 Restore on Corruption
```bash
# Start appmon
make build VERSION=0.1.0
./build/appmon start

# Corrupt the binary (same version, different content)
echo "corrupted" >> ./build/appmon

# Wait for restore
sleep 70

# Check
./build/appmon version  # Should work
```
- [ ] Corrupted binary restored

### 3.5 LaunchAgent Plist Protection
```bash
# Start appmon
./build/appmon start

# Delete plist
rm ~/Library/LaunchAgents/com.focusd.appmon.plist

# Wait for restore
sleep 70

# Check
ls ~/Library/LaunchAgents/com.focusd.appmon.plist
```
- [ ] Plist restored when deleted

---

## 4. Mutual Daemon Protection

### 4.1 Guardian Restarts Watcher
```bash
./build/appmon start
# Find and kill watcher
pkill -f "role watcher"
sleep 10
./build/appmon status
```
- [ ] Watcher restarted by guardian

### 4.2 Watcher Restarts Guardian
```bash
./build/appmon start
# Find and kill guardian
pkill -f "role guardian"
sleep 10
./build/appmon status
```
- [ ] Guardian restarted by watcher

---

## 5. Package Manager Integration

### 5.1 Brew Uninstall Attempt
```bash
brew install --cask steam
./build/appmon scan
# Check logs
tail /var/tmp/appmon.log | grep -i brew
```
- [ ] Attempts brew uninstall (may fail due to sudo)
- [ ] Falls back to path deletion

---

## Quick Verification Script

```bash
#!/bin/bash
# Run this to quickly verify core features

echo "=== Building ==="
make build VERSION=0.2.0

echo "=== Testing version command ==="
./build/appmon version
./build/appmon version --json

echo "=== Starting appmon ==="
./build/appmon start

echo "=== Checking status ==="
./build/appmon status

echo "=== Testing scan ==="
brew install --cask steam 2>/dev/null || true
./build/appmon scan

echo "=== Verifying Steam removed ==="
ls /Applications/Steam.app 2>&1 | grep "No such file" && echo "PASS: Steam deleted"
brew list --cask | grep -q steam && echo "FAIL: Steam still in brew" || echo "PASS: Steam not in brew"

echo "=== Done ==="
```
