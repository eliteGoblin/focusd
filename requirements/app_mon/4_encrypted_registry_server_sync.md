# Encrypted Registry & Server Sync

Note: I also want to switch the registry from the current plain-text file to SQLite and use SQLCipher for the local registry.

**GitHub Issue**: TBD

## Overview

Replace plain-text registry with encrypted SQLite database.

**Implementation Phases:**
- **Phase 1 (MVP)**: Local encrypted registry with file-based key
- **Phase 2**: Server-generated key + config sync

This doc focuses on Phase 1 scope. Phase 2 is documented for solution design context.

## Problem Statement

Current registry is a plain JSON file that users can easily:
1. Read to find PIDs, plist names, backup paths
2. Modify or delete to disrupt protection
3. Use knowledge to craft "fake" binaries that bypass protection

Additionally, without a server:
- No cross-device config management
- No recovery if local data is lost
- No "cooling-off" period for disabling (impulse control)
- No accountability partner features

## Threat Model

**Primary threat**: User trying to bypass protection during moment of urge.

**Assumptions**:
- User won't spend hours reverse-engineering
- Friction = time for urge to pass
- Not defending against sophisticated attackers
- Physical access is assumed (it's user's own machine)

**Goal**: Add enough friction that impulsive bypass attempts fail.

---

## Phase 1: Local Encrypted Registry (MVP) - IMPLEMENTATION SCOPE

### User Stories

1. As a user, I cannot read the registry file to find PIDs/paths
2. As a user, I cannot easily decrypt the registry without significant effort
3. As the app, I can read/write registry transparently with encryption
4. As the app, I work fully offline (no server dependency)

### Folder Structure

```
User mode:
~/.appmon/                          # Hidden app data folder
├── registry.db                     # Encrypted SQLite (SQLCipher)
└── .key                            # 256-bit symmetric key (hidden file)

~/.local/bin/appmon                 # Binary (existing location)

System mode:
/var/lib/appmon/                    # System app data folder
├── registry.db
└── .key

/usr/local/bin/appmon               # Binary (existing location)
```

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Key File (.key)                          │
│  Location: ~/.appmon/.key (hidden)                          │
│  Content: 256-bit random key (base64 encoded)               │
│  Permissions: 0600 (owner read/write only)                  │
│  Generated: On first install                                │
│  Future: Will be replaced by server-generated key           │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ Read key from file
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    appmon Process                           │
│  1. Read key from ~/.appmon/.key                            │
│  2. Open SQLCipher DB with key                              │
│  3. Read/write config transparently                         │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ Encrypted read/write
                              ▼
┌─────────────────────────────────────────────────────────────┐
│              registry.db (SQLCipher encrypted)              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ ████████████████████████████████████████████████████│    │
│  │ ████████ Fully encrypted - opaque bytes ████████████│    │
│  │ ████████████████████████████████████████████████████│    │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

Q: I want you to help decide where I put the key and DB file? 

consider I got user mode and root mode, which appmon will be in different place. should I create a folder for appmon and put key and DB along with binary? 

or I always create a folder in ~/.appmon? but how does root know which user's home will be put in? suggest for me. 

### Why File-Based Key (MVP)

| Approach | Pros | Cons | Decision |
|----------|------|------|----------|
| File-based key | Simple, no dependencies | Less secure than Keychain | ✅ MVP |
| Keychain | More secure | macOS-specific API complexity | Phase 1b |
| Server-generated | Best security + recovery | Requires server | Phase 2 |

File-based key provides enough friction for MVP threat model (impulsive user).
Migration path: file → Keychain → server is additive, not breaking.

I nwant you when implemnt: 

Using TDD and clean arch. Use interface to hide implementation detail: whether it's load from file, or later could be load from key chain. Same idea for other implemnation detail. use interface. 

### Data Model

TBD: 

 review the requirement . and initial schema, I want to keep things simple atm. only imlemnt schema strictly necessaary.

Prob only focus on: 

* All info in current registry: randomly generated process name, launchdaemon plist/launch agent. 
* KISS, only necessaary info atm, don't create for future possible data/schema
* I also want DB schema migration implementation: I think I need one, review for me.  since later I might want to add col, create table. I want to do it by code, and keey in git etc. like prod pbest practice for golang server

Q: what if app crash and get restarted, the PID changed, will registry needs to know? IM wonder at this case, how system can still able to keep track of what's "currently running", so it can continue working for like monitor each other if up, and update version correctly. I think update needs know what current running process? 


### Key Management (Phase 1 - File Based)


**Key properties (Phase 1)**:
- 256-bit random key (AES-256 compatible)
- Stored in hidden file with restricted permissions (0600)
- Generated on first install
- Location not obvious to casual user

**Migration to Phase 2**:
- Server generates key on first login
- Client downloads and overwrites local key file
- Same file location, different source

### Randomized Secrets

On first install, generate and store:

| Secret | Example | Purpose |
|--------|---------|---------|
| `plist_name` | `com.apple.xpc.launchd.helper.8f3a2b` | Looks like system service |
| `process_pattern_watcher` | `kernelmanagerd` | Looks like system process |
| `process_pattern_guardian` | `diskarbitrationd` | Looks like system process |

These are stored in encrypted SQLite, not hardcoded.

### Migration from Current Registry

No need to support migrate, when develop I want you to directly create SQLCipher, using info from the local registry you find. (i.e AI fill the data, )


No need to consider recovery in phase1, I think phase2 server can save backuped SQL db. 

Since these data mostly are cache data can be rebuilt, so even could server implemment the "cleanup request". when daemon talk to server, server will tell them to quit/restart, for these daemon process cannot find the registry. and server will tell daemon to rebuild the local DB and recover. (will leave prob for phase 3 after server is built) 

**Note**: Phase 2 server sync provides better recovery by backing up secrets to server.

### New Components

jsut use as example: below thoughts
```go
// internal/infra/encrypted_registry.go
type EncryptedRegistry struct {
    db      *sql.DB
    keyMgr  *KeychainManager
}

func NewEncryptedRegistry() (*EncryptedRegistry, error)
func (r *EncryptedRegistry) GetDaemonState() (*DaemonEntry, error)
func (r *EncryptedRegistry) SetDaemonState(entry *DaemonEntry) error
func (r *EncryptedRegistry) GetSecret(key string) (string, error)
func (r *EncryptedRegistry) SetSecret(key, value string) error
func (r *EncryptedRegistry) GetConfig(key string) (string, error)
func (r *EncryptedRegistry) SetConfig(key, value string) error
```

### Dependencies

```go
// go.mod additions
require (
    github.com/mutecomm/go-sqlcipher/v4  // SQLCipher bindings
    github.com/keybase/go-keychain       // macOS Keychain bindings
)
```

### Definition of Done (Phase 1 - MVP)

- [ ] Create ~/.appmon/ folder structure on install
- [ ] Generate symmetric key file (.key) on first install
- [ ] SQLCipher encrypted database working
- [ ] Migration from old JSON registry to encrypted SQLite
- [ ] Randomized plist/process names on install
- [ ] All existing functionality works with new registry
- [ ] Registry file unreadable without key
- [ ] Key file has restricted permissions (0600)
- [ ] Unit tests for encrypted registry
- [ ] Works fully offline (no server dependency)
- [ ] User mode and system mode folder paths work correctly

---

## Phase 2: Server Sync & Config Management

### User Stories

1. As a user, I can log in and sync my config across devices
2. As a user, I can configure blocklist/schedule from web dashboard
3. As a user, my device secrets are backed up (recovery if local data lost)
4. As a user, I must wait 24 hours to disable protection (cooling-off)
5. As an admin, I can see all my devices and their status

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         SERVER                              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │  PostgreSQL Database                                │    │
│  │  • users (accounts, auth)                           │    │
│  │  • devices (per-user devices, secrets backup)       │    │
│  │  • configs (blocklist, schedule, preferences)       │    │
│  │  • unlock_requests (cooling-off tracking)           │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
│  API Endpoints:                                             │
│  • POST /auth/login, /auth/register                         │
│  • GET  /config (download blocklist, schedule)              │
│  • POST /devices (register device, upload secrets)          │
│  • GET  /devices/:id/secrets (recover secrets)              │
│  • POST /unlock/request (start 24h cooling-off)             │
│  • GET  /unlock/status (check if cooling-off complete)      │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ TLS (HTTPS)
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      appmon Client                          │
│                                                             │
│  Sync Logic:                                                │
│  • On start: sync config FROM server                        │
│  • On install: register device, upload secrets TO server    │
│  • On config change: push TO server                         │
│  • Offline: use local SQLite cache                          │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │  Local SQLite (encrypted)                           │    │
│  │  • Cache of server config                           │    │
│  │  • Device secrets                                   │    │
│  │  • Daemon state                                     │    │
│  │  ─────────────────────────────────────────────────  │    │
│  │  CLIENT CAN ALWAYS FUNCTION WITH LOCAL CACHE        │    │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

### Data Ownership

| Data | Owner | Sync Direction |
|------|-------|----------------|
| Blocklist apps | Server | Server → Client |
| Block schedule | Server | Server → Client |
| User preferences | Server | Server → Client |
| Device ID | Client | Client → Server (backup) |
| Plist name | Client | Client → Server (backup) |
| Process patterns | Client | Client → Server (backup) |
| Daemon PIDs | Client | Local only (runtime) |
| Backup paths | Client | Client → Server (backup) |

### Offline-First Guarantee

**Critical requirement**: App MUST function when server is unreachable.

```
Server reachable:
1. Sync latest config from server
2. Update local SQLite cache
3. Use synced config

Server unreachable:
1. Log warning: "Server unreachable, using cached config"
2. Use local SQLite cache
3. Retry sync in background (exponential backoff)
4. App continues to block apps normally
```

### First Install Flow

```
With network:
1. User downloads app
2. User runs `appmon start`
3. Prompt: "Log in or create account"
4. User authenticates
5. Generate device ID, plist name, process patterns
6. Upload secrets to server (backup)
7. Download config (blocklist, schedule) from server
8. Store everything in local encrypted SQLite
9. Start daemons with randomized names

Without network:
1. User downloads app
2. User runs `appmon start`
3. Prompt: "Log in or create account"
4. Error: "No network - cannot create account"
5. Option: "Start with default config (sync later)"
6. Generate secrets locally
7. Use hardcoded default blocklist
8. Mark account as "pending setup"
9. Sync when network available
```

### Cooling-Off Period (Anti-Impulse Feature)

```
User: "appmon unlock"

App: "Unlock request submitted.
      Protection will be disabled in 24 hours.

      To cancel: appmon unlock --cancel"

[24 hours later]

App: "Cooling-off period complete.
      Run 'appmon unlock --confirm' to disable protection."

User: "appmon unlock --confirm"

App: "Protection disabled.
      Run 'appmon start' to re-enable."
```

Server tracks:
- Request timestamp
- Expiry timestamp (request + 24h)
- User can cancel anytime before expiry
- Optional: Notify accountability partner

### Conflict Resolution

| Scenario | Resolution |
|----------|------------|
| Server config newer | Server wins (overwrite local) |
| Local secrets differ from server | Client is authoritative (update server) |
| Device re-registered | Generate new secrets, update server |
| Two devices same ID | Error: "Device ID conflict, re-register" |

### API Design

```yaml
# Auth
POST /auth/register
  body: { email, password }
  response: { user_id, token }

POST /auth/login
  body: { email, password }
  response: { user_id, token }

# Config (Server → Client)
GET /config
  headers: { Authorization: Bearer <token> }
  response: {
    blocklist: ["steam", "dota2", ...],
    schedule: { enabled: true, start: "09:00", end: "17:00" },
    preferences: { ... }
  }

# Device (Client → Server)
POST /devices
  headers: { Authorization: Bearer <token> }
  body: {
    device_id: "uuid",
    platform: "macos",
    secrets: {
      plist_name: "com.apple.xpc...",
      process_patterns: ["kernelmanagerd", ...]
    }
  }
  response: { device_id, registered_at }

GET /devices/:id/secrets
  headers: { Authorization: Bearer <token> }
  response: { plist_name, process_patterns, ... }

# Unlock (Cooling-off)
POST /unlock/request
  headers: { Authorization: Bearer <token> }
  body: { device_id }
  response: { request_id, expires_at }

GET /unlock/status
  headers: { Authorization: Bearer <token>, device_id }
  response: {
    pending: true/false,
    expires_at: "...",
    can_unlock: true/false
  }

DELETE /unlock/request
  headers: { Authorization: Bearer <token> }
  response: { cancelled: true }
```

### Definition of Done (Phase 2)

- [ ] User registration and authentication
- [ ] Config download from server
- [ ] Device registration and secrets backup
- [ ] Secrets recovery from server
- [ ] Offline mode with local cache
- [ ] Sync on app start
- [ ] 24-hour cooling-off unlock
- [ ] Web dashboard for config management
- [ ] Cross-device config sync
- [ ] Server deployment (cloud)

---

## Product Review

### Strengths

1. **Offline-first**: App never stops working due to server issues
2. **Friction-based security**: Matches threat model (impulsive user, not attacker)
3. **Progressive enhancement**: Phase 1 adds value without server complexity
4. **Industry-standard patterns**: SQLCipher, Keychain, TLS - proven technologies
5. **Recovery path**: Server backup prevents data loss

### Concerns & Mitigations

| Concern | Mitigation |
|---------|------------|
| Keychain access requires password on boot? | Use `kSecAttrAccessibleAfterFirstUnlock` - available after first login |
| SQLCipher adds binary size | ~2MB increase, acceptable |
| Server costs | Minimal for MVP user base, scale later |
| What if user forgets password? | Standard email password reset |
| What if server is breached? | Secrets are per-device, limited blast radius |

### Competitive Analysis (vs Freedom)

| Feature | Freedom | appmon (Phase 2) |
|---------|---------|------------------|
| Encrypted local storage | Unknown | Yes (SQLCipher) |
| Server sync | Yes | Yes |
| Cooling-off period | Yes (varies) | Yes (24h) |
| Cross-platform | Yes | macOS only (for now) |
| Obfuscated process names | Unknown | Yes |
| Open source | No | Yes |

---

## Technical Review

### Security Considerations

1. **Key in Keychain**: Industry best practice for macOS
2. **SQLCipher**: AES-256, used by Signal, 1Password
3. **TLS for sync**: Standard HTTPS, certificate pinning optional
4. **No secrets in binary**: All secrets generated at runtime

### Performance Considerations

1. **SQLCipher overhead**: ~5-15% slower than plain SQLite (negligible for this use case)
2. **Keychain access**: ~10ms per access (cache key in memory during session)
3. **Sync on start**: Background thread, doesn't block daemon start

### Migration Path

```
v0.3.x (current):   JSON registry
v0.4.0 (Phase 1):   Encrypted SQLite (local only)
v0.5.0 (Phase 2):   Server sync + encrypted SQLite
```

Backward compatibility:
- v0.4.0 migrates JSON → SQLite automatically
- v0.5.0 adds server sync to existing SQLite schema

---

---

## Implementation Roadmap

```
┌─────────────────────────────────────────────────────────────┐
│  Phase 1a: Local Encrypted Registry (MVP)     ◄── CURRENT  │
│  ─────────────────────────────────────────────────────────  │
│  • Folder structure: ~/.appmon/                             │
│  • File-based key: ~/.appmon/.key                           │
│  • Encrypted SQLite: ~/.appmon/registry.db                  │
│  • Random plist/process names                               │
│  • E2E working locally                                      │
│  • NO server dependency                                     │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Phase 1b: Keychain Integration (Optional)                  │
│  ─────────────────────────────────────────────────────────  │
│  • Move key from file to macOS Keychain                     │
│  • Better security (app-only access)                        │
│  • Still no server dependency                               │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Phase 2: Server Sync                                       │
│  ─────────────────────────────────────────────────────────  │
│  • User authentication (login)                              │
│  • Server generates symmetric key                           │
│  • Server stores device secrets (backup)                    │
│  • Server provides config (blocklist, schedule)             │
│  • Client syncs on start, uses local cache offline          │
│  • 24h cooling-off period for unlock                        │
└─────────────────────────────────────────────────────────────┘
```

**Why this order:**
1. Phase 1a gets E2E working with encryption - testable, shippable
2. Phase 1b is optional security hardening
3. Phase 2 adds commercial features (accounts, sync, cooling-off)

---

## Out of Scope (This Requirement)

- iOS/Android clients (future)
- Windows/Linux support (future)
- End-to-end encryption (server can read config - acceptable for this use case)
- Hardware security keys (Yubikey, etc.)
- Biometric unlock (Touch ID) - maybe Phase 3
- Server implementation details (separate requirement doc)

## Dependencies

- Phase 1: None (self-contained)
- Phase 2: Server infrastructure (separate requirement)

## Solution Documents

Detailed implementation located at:
- `artifacts/4_encrypted_registry/solution.md` - Architecture & design
- `artifacts/4_encrypted_registry/implementation_plan.md` - Step-by-step tasks
- `artifacts/4_encrypted_registry/test_plan.md` - Test cases

## Related

- [#4 Version Update Command](./1_features_version_autoupdate_cmd.md) - Works with new registry
- [#6 Freedom Protection](./2_freedom_protection.md) - Uses encrypted secrets
