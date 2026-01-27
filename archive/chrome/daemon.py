#!/usr/bin/env python3
"""
Chrome Focus Daemon - Watches and auto-restores Chrome policy file
"""
import sys
import time
import json
from pathlib import Path
from watchdog.observers import Observer
from watchdog.events import FileSystemEventHandler
import yaml

try:
    import setproctitle
    HAS_SETPROCTITLE = True
except ImportError:
    HAS_SETPROCTITLE = False


def get_chrome_policy_path() -> Path:
    """Get Chrome managed policy path for current OS"""
    if sys.platform == "darwin":  # macOS
        return Path("/Library/Google/Chrome/NativeMess agingHosts/policies/managed")
    else:  # Linux
        return Path("/etc/opt/chrome/policies/managed")


def get_policy_file_path() -> Path:
    """Get full path to policy JSON file"""
    return get_chrome_policy_path() / "managed_policies.json"


def load_plugins() -> list:
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


class ChromePolicyWatcher(FileSystemEventHandler):
    """Watch Chrome policy directory and auto-restore deleted policy"""

    def __init__(self, policy_file: Path):
        self.policy_file = policy_file

    def on_deleted(self, event):
        """Called when a file is deleted"""
        if event.src_path == str(self.policy_file):
            print(f"Policy file deleted, restoring...", file=sys.stderr)
            time.sleep(0.5)  # Brief delay to avoid race conditions
            create_chrome_policy()
            print(f"Policy file restored", file=sys.stderr)

    def on_modified(self, event):
        """Called when a file is modified"""
        # Check if policy was cleared/tampered
        if event.src_path == str(self.policy_file):
            try:
                with open(self.policy_file, 'r') as f:
                    content = f.read().strip()
                    if not content or content == "{}":
                        print(f"Policy file cleared, restoring...", file=sys.stderr)
                        create_chrome_policy()
                        print(f"Policy file restored", file=sys.stderr)
            except Exception:
                pass


def main():
    """Main daemon loop"""
    # Set obfuscated process name if provided
    if len(sys.argv) > 1 and HAS_SETPROCTITLE:
        process_name = sys.argv[1]
        setproctitle.setproctitle(process_name)

    policy_file = get_policy_file_path()

    # Ensure policy exists on startup
    if not policy_file.exists():
        create_chrome_policy()

    # Set up file watcher
    event_handler = ChromePolicyWatcher(policy_file)
    observer = Observer()
    observer.schedule(event_handler, str(policy_file.parent), recursive=False)
    observer.start()

    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        observer.stop()

    observer.join()


if __name__ == "__main__":
    main()
