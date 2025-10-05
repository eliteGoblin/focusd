#!/usr/bin/env python3
import os
import sys
import json
import time
import signal
import hashlib
import subprocess
from pathlib import Path
from typing import List, Dict, Optional
import click
import yaml
import requests
import psutil


# Cross-platform Chrome policy paths
def get_chrome_policy_path() -> Path:
    """Get Chrome managed policy path for current OS"""
    if sys.platform == "darwin":  # macOS
        return Path("/Library/Google/Chrome/NativeMess agingHosts/policies/managed")
    else:  # Linux
        return Path("/etc/opt/chrome/policies/managed")


def get_policy_file_path() -> Path:
    """Get full path to policy JSON file"""
    return get_chrome_policy_path() / "managed_policies.json"


def get_daemon_lock_file() -> Path:
    """Get daemon lock file path (hidden)"""
    return Path("/tmp/.chrome_focus_daemon.lock")


def get_obfuscated_daemon_name() -> str:
    """Generate random obfuscated daemon name that looks like a system process"""
    import random
    import string

    # Common system process prefixes
    prefixes = [
        "systemd", "gvfs", "dbus", "update-notifier",
        "evolution", "tracker", "gnome", "gio"
    ]

    # Common system process suffixes
    suffixes = [
        "monitor", "helper", "daemon", "service",
        "worker", "store", "miner", "agent"
    ]

    # Generate random ID (letters only, looks more system-like)
    random_id = ''.join(random.choices(string.ascii_lowercase, k=6))

    prefix = random.choice(prefixes)
    suffix = random.choice(suffixes)

    return f"{prefix}-{random_id}-{suffix}"


def load_plugins() -> List[Dict]:
    """Load plugins from YAML file"""
    yaml_path = Path(__file__).parent / "plugins.yml"
    with open(yaml_path, 'r') as f:
        data = yaml.safe_load(f)
        return data.get('plugins', [])


def create_chrome_policy() -> None:
    """Create Chrome managed policy JSON file"""
    plugins = load_plugins()
    plugin_ids = [f"{p['id']}" for p in plugins]

    policy = {
        "ExtensionInstallForcelist": plugin_ids
    }

    policy_path = get_policy_file_path()
    policy_path.parent.mkdir(parents=True, exist_ok=True)

    with open(policy_path, 'w') as f:
        json.dump(policy, f, indent=2)

    print(f"✓ Chrome policy created at {policy_path}")


def remove_chrome_policy() -> None:
    """Remove Chrome managed policy"""
    policy_path = get_policy_file_path()
    if policy_path.exists():
        policy_path.unlink()
        print(f"✓ Chrome policy removed from {policy_path}")


def get_motivational_quote() -> str:
    """Fetch motivational quote from API"""
    try:
        response = requests.get("https://api.quotable.io/random?tags=inspirational", timeout=5)
        if response.status_code == 200:
            data = response.json()
            return f"{data['content']} - {data['author']}"
    except Exception:
        pass

    # Fallback quotes
    fallbacks = [
        "The only way to do great work is to love what you do. - Steve Jobs",
        "Success is not final, failure is not fatal: it is the courage to continue that counts. - Winston Churchill",
        "Believe you can and you're halfway there. - Theodore Roosevelt",
        "Your limitation—it's only your imagination. - Unknown"
    ]
    import random
    return random.choice(fallbacks)


def start_daemon() -> None:
    """Start the file watcher daemon with obfuscated name"""
    daemon_script = Path(__file__).parent / "daemon.py"
    daemon_name = get_obfuscated_daemon_name()
    lock_file = get_daemon_lock_file()

    # Check if already running
    if lock_file.exists():
        with open(lock_file, 'r') as f:
            pid = int(f.read().strip())
            if psutil.pid_exists(pid):
                print(f"✓ Daemon already running")
                return

    # Start daemon in background with obfuscated name
    proc = subprocess.Popen(
        [sys.executable, str(daemon_script), daemon_name],
        start_new_session=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL
    )

    # Save PID to lock file
    with open(lock_file, 'w') as f:
        f.write(str(proc.pid))

    print(f"✓ Daemon started")


def stop_daemon() -> None:
    """Stop the daemon"""
    lock_file = get_daemon_lock_file()

    if not lock_file.exists():
        print("✗ Daemon not running")
        return

    with open(lock_file, 'r') as f:
        pid = int(f.read().strip())

    if psutil.pid_exists(pid):
        os.kill(pid, signal.SIGTERM)
        print(f"✓ Daemon stopped")

    lock_file.unlink()


def is_daemon_running() -> bool:
    """Check if daemon is running"""
    lock_file = get_daemon_lock_file()
    if lock_file.exists():
        with open(lock_file, 'r') as f:
            pid = int(f.read().strip())
            return psutil.pid_exists(pid)
    return False


@click.group()
def cli():
    """Chrome Focus - Enforce Chrome extensions to stay focused"""
    pass


@cli.command()
def on():
    """Enable Chrome extension enforcement and start daemon"""
    print("🔒 Enabling Chrome Focus...")
    create_chrome_policy()
    start_daemon()
    print("✓ Chrome Focus is now ON")
    print("\nRestart Chrome to apply policy changes.")


@cli.command()
@click.option('--duration', type=int, help='Disable for N minutes (max 60)')
def off(duration: Optional[int]):
    """Disable Chrome extension enforcement (requires typing motivational quote)"""

    # Validate duration
    if duration is not None:
        if duration < 1 or duration > 60:
            print("✗ Error: Duration must be between 1 and 60 minutes")
            return

    # Get motivational quote
    quote = get_motivational_quote()

    print("\n" + "="*80)
    print("⚠️  You are about to disable Chrome Focus")
    print("="*80)
    print("\nTo continue, please type the following quote EXACTLY:\n")
    print(f'  "{quote}"')
    print("\n" + "-"*80)

    user_input = input("\nType here: ").strip()

    if user_input != quote:
        print("\n✗ Quote doesn't match. Chrome Focus remains enabled.")
        return

    print("\n✓ Quote verified. Disabling Chrome Focus...")

    # Stop daemon and remove policy
    stop_daemon()
    remove_chrome_policy()

    if duration:
        print(f"\n✓ Chrome Focus disabled for {duration} minute(s)")
        print(f"  It will automatically re-enable at {time.strftime('%H:%M:%S', time.localtime(time.time() + duration * 60))}")

        # Schedule re-enable
        time.sleep(duration * 60)
        print("\n⏰ Time's up! Re-enabling Chrome Focus...")
        create_chrome_policy()
        start_daemon()
        print("✓ Chrome Focus is back ON")
    else:
        print("\n✓ Chrome Focus is now OFF")


@cli.command()
def status():
    """Check if Chrome Focus daemon is running"""
    daemon_running = is_daemon_running()
    policy_exists = get_policy_file_path().exists()

    print("\n📊 Chrome Focus Status")
    print("="*40)
    print(f"  Daemon:  {'🟢 Running' if daemon_running else '🔴 Stopped'}")
    print(f"  Policy:  {'🟢 Active' if policy_exists else '🔴 Inactive'}")
    print("="*40 + "\n")


if __name__ == '__main__':
    cli()
