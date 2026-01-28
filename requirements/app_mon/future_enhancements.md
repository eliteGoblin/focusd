# Future Enhancements

## Security

- **Checksum verification for GitHub downloads** - Verify SHA256/signature of downloaded binaries before installation to ensure integrity beyond TLS

## Features

- **Auto-update command** - Check GitHub for newer version, download, replace binary, restart daemons, rollback if new version fails
- **Freedom app protection** - Monitor and restart Freedom.app if killed, restore Login Items if removed
- **Per-URL proxy blocking** - Block specific URLs/paths (e.g., google.com/search) while allowing subdomains (e.g., cloud.google.com)
- **LLM content analysis** - Detect distracting content (videos, news) on unknown sites using local or managed LLM

## Operations

- **Structured release notes** - Categorized changelog (Features, Bug Fixes, Improvements) like k8s releases
