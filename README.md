# FocusD - Freedom from Distraction

> Breaking free from the slot machines in our pockets.

## Philosophy

Phones and websites are designed like slot machines - engineered to capture attention and create anxiety. They harm our ability to focus deeply and damage intimate relationships. This toolset helps reclaim control.

## Chrome Focus

**Status:** Production-ready | **Version:** 0.1.0

A self-enforcing Chrome extension manager that prevents disabling productivity extensions.

**Key Features:**
- Force-installs Chrome extensions via enterprise policies
- Auto-restores policy if deleted (background daemon)
- Motivational barriers to prevent impulsive disabling
- Temporary disable with auto-re-enable (max 1 hour)
- Process name obfuscation for self-protection

**[📖 Full Documentation →](chrome/README.md)**

**Quick Start:**
```bash
cd chrome
./install.sh
sudo cf on
```

---

## Why These Tools Exist

Modern technology companies use psychological manipulation to maximize engagement:

- **Variable rewards** (like slot machines) keep us checking notifications
- **Infinite scroll** removes natural stopping points
- **FOMO mechanics** create anxiety about missing content
- **Social validation** (likes, views) triggers dopamine loops

**Consequences:**
- Inability to focus on deep work
- Increased anxiety and stress
- Damaged personal relationships
- Loss of autonomy and self-control

These tools are designed to help you **opt out** of that system.

## Project Structure

```
focusd/
├── chrome/              # Chrome Focus tool
│   ├── README.md       # User documentation
│   ├── DESIGN.md       # Architecture & design decisions
│   ├── CHANGELOG.md    # Version history
│   ├── version.yml     # Release metadata
│   ├── install.sh      # One-command installer
│   ├── chrome_focus.py # Main CLI
│   ├── daemon.py       # Background watcher
│   ├── plugins.yml     # Extension configuration
│   └── cf              # Wrapper script
├── requirements/        # Requirements & specifications
│   └── chrome/
│       └── plugins.md
└── CLAUDE.md           # Developer context
```

## Contributing

This is a personal productivity tool. If you find it useful, feel free to fork and adapt it to your needs.

## License

MIT

---

**Remember:** The goal isn't to eliminate technology - it's to use it intentionally, not compulsively.
