# Post-Local MVP Proposal: Server-Controlled Protection

**Date:** 2026-01-27
**Status:** Draft
**Author:** Claude Code + Frank Sun

---

## Executive Summary

The current app_mon implementation provides solid local protection with dual-daemon architecture, binary backup, and process obfuscation. However, it lacks the friction and server control needed for the **"Do Not Trust Yourself"** model.

This proposal outlines features to add after the local MVP is complete, focusing on server-controlled configuration that prevents bypass even with admin access.

---

## Current State Analysis

### What's Already Working

| Feature | Implementation | File(s) |
|---------|---------------|---------|
| Dual daemon (Watcher + Guardian) | Mutual monitoring, auto-restart | `daemon/watcher.go`, `daemon/guardian.go` |
| Process killing | SIGKILL with pattern matching | `infra/process.go` |
| File deletion | Recursive with glob support | `infra/filesystem.go` |
| LaunchAgent persistence | Auto-start, KeepAlive | `infra/launchd.go` |
| Binary backup | 3 hidden locations, SHA256, version-aware | `infra/backup.go` |
| Process obfuscation | `com.apple.cfprefsd.xpc.a1b2c3` style | `infra/obfuscator.go` |
| Plist auto-restore | Recreates if deleted | `daemon/watcher.go` |
| Steam/Dota2 policies | Complete path + process lists | `policy/steam.go`, `policy/dota2.go` |

### Current Bypass Vulnerability

```
Admin user bypass (current state):
1. Find daemon processes (~10 seconds with effort)
2. Kill both daemons simultaneously
3. Unload LaunchAgent plists
4. Total time: ~30 seconds

Goal: Make bypass take 15-20+ minutes (longer than urge duration)
```

---

## Proposed Features

### Phase 1: Local Hardening (MVP)

These features strengthen local protection before adding server dependency.

#### 1.1 LaunchDaemon (Not LaunchAgent)

**Current:** `~/Library/LaunchAgents/` (user level)
**Proposed:** `/Library/LaunchDaemons/` (system level)

| Aspect | LaunchAgent | LaunchDaemon |
|--------|-------------|--------------|
| Runs as | Current user | root |
| Survives logout | No | Yes |
| Unload requires | User permission | sudo |
| Install requires | No sudo | sudo |

**Implementation:**
- Modify `internal/infra/launchd.go`
- Change plist path to `/Library/LaunchDaemons/com.focusd.appmon.plist`
- Add `UserName: root` to plist
- Update install to require sudo

**Effort:** 1 day

---

#### 1.2 Triple Daemon Architecture

**Current:** Watcher ↔ Guardian (bidirectional)
**Proposed:** Watcher → Guardian → Sentinel → Watcher (ring)

```
┌─────────┐     ┌─────────┐     ┌─────────┐
│ Watcher │────►│Guardian │────►│Sentinel │
│ (Alpha) │     │ (Beta)  │     │ (Gamma) │
└────▲────┘     └─────────┘     └────┬────┘
     │                               │
     └───────────────────────────────┘
```

**Each daemon:**
- Watches next daemon's process (is it alive?)
- Watches next daemon's plist (is it present?)
- Restarts next daemon if dead or plist missing

**Bypass difficulty:**
- Must kill 3 processes AND unload 3 plists within ~5 seconds
- Any survivor restarts the others

**Implementation:**
- Add `internal/daemon/sentinel.go`
- Modify `internal/daemon/bootstrap.go` for 3-daemon coordination
- Add 3 plist templates (compiled into binary)

**Effort:** 2 days

---

#### 1.3 Quote Typing Friction

**Current:** No friction to disable
**Proposed:** Must type motivational quote character-by-character

```
To disable protection, type this quote exactly:

"The secret of getting ahead is getting started."
  -- Mark Twain

Type here (copy-paste disabled): _
```

**Anti-paste detection:**
- Read character-by-character in raw terminal mode
- Track timing between keystrokes
- If 4+ characters arrive within 30ms = paste detected = reject

**Implementation:**
- Add `internal/friction/quote.go` (fetch from api.quotable.io)
- Add `internal/friction/antipaste.go` (timing detection)
- Integrate into `cmd/appmon/main.go` disable command

**Effort:** 1 day

---

#### 1.4 Time Delay Enforcement

**Current:** Immediate action
**Proposed:** 15-minute wait before disable takes effect

```
Urges typically fade within 10-20 minutes.
Please wait before disabling protection.

[========================================] 15:00 remaining
```

**Implementation:**
- Add `internal/friction/delay.go`
- Cannot be interrupted (Ctrl+C restarts timer)
- Progress bar in terminal

**Effort:** 0.5 days

---

### Phase 2: Server Infrastructure

#### 2.1 Server API

**Endpoints:**

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/devices/register` | Register new device |
| POST | `/api/v1/heartbeat` | Check-in every 5 min |
| GET | `/api/v1/config` | Get signed config |
| POST | `/api/v1/unlock/request` | Request unlock token |
| GET | `/api/v1/unlock/status` | Check unlock approval |

**Device Registration Flow:**
```
Install app → Generate device_id → Register with server
           → Receive client certificate (mTLS)
           → Receive server public key (for config verification)
```

**Implementation:**
- Server: Go + Echo/Gin framework
- Database: PostgreSQL or SQLite
- Auth: mTLS with client certificates
- Hosting: Azure App Service / AWS Lambda

**Effort:** 1 week

---

#### 2.2 Signed Configuration

**Problem:** Local config can be edited by admin
**Solution:** Server signs config with Ed25519, client verifies

```go
type SignedConfig struct {
    Version           int64
    ProtectionEndUTC  time.Time     // When protection window ends
    BlockedApps       []AppPolicy
    BlockedDomains    []string
    FailMode          string        // "secure" or "open"

    // Cryptographic binding
    ServerSignature   []byte        // Ed25519 signature
}
```

**Verification:**
- Server public key compiled into binary (cannot replace)
- On config load: verify signature
- If signature invalid → lockdown mode (block everything)

**Implementation:**
- Add `internal/crypto/verify.go`
- Add `internal/config/sealed.go`
- Embed server public key via `go:embed` or ldflags

**Effort:** 3 days

---

#### 2.3 Protection Windows

**Problem:** User can disable anytime
**Solution:** Protection window set on server, cannot end early

```
Dashboard UI:
┌─────────────────────────────────────────────┐
│  Protection Window                          │
│                                             │
│  Start: January 27, 2026 9:00 AM           │
│  End:   February 3, 2026 9:00 AM           │
│                                             │
│  Status: ACTIVE (6 days remaining)          │
│                                             │
│  [Request Early Unlock]  ← 15 min wait     │
└─────────────────────────────────────────────┘
```

**Early unlock flow:**
1. User clicks "Request Early Unlock" on dashboard
2. 15-minute timer starts (cannot close browser)
3. Type confirmation phrase
4. Unlock token generated (valid 1 hour)
5. Next heartbeat delivers token to client
6. Client verifies token signature
7. Protection disabled for requested duration
8. Auto-re-enables after duration

**Implementation:**
- Server: unlock request table, token generation
- Client: token verification in heartbeat response

**Effort:** 4 days

---

### Phase 3: Accountability & Dead Man's Switch

#### 3.1 Accountability Partner

**Setup:**
```
Dashboard:
┌─────────────────────────────────────────────┐
│  Accountability Partner                      │
│                                             │
│  Email: spouse@example.com                  │
│  Phone: +1-555-123-4567 (for critical)      │
│                                             │
│  Notify when:                               │
│  [x] Missed heartbeat > 1 hour              │
│  [x] Unlock requested                        │
│  [x] Protection disabled                     │
│  [ ] Daily summary                           │
└─────────────────────────────────────────────┘
```

**Alert triggers:**
- Heartbeat missed > 1 hour → Email warning
- Heartbeat missed > 6 hours → SMS critical
- Unlock requested → Email notification
- Protection disabled → Email + SMS

**Implementation:**
- Server: partner table, alert job (cron)
- Email: SendGrid / AWS SES
- SMS: Twilio

**Effort:** 3 days

---

#### 3.2 Dead Man's Switch

**Problem:** User blocks network to server
**Solution:** If client can't reach server → assume bypass attempt

**Behavior:**

| Offline Duration | Action |
|------------------|--------|
| 0-5 min | Normal (grace period) |
| 5-60 min | Log warning, continue blocking |
| 1-6 hours | Alert accountability partner |
| 6-24 hours | Critical alert (SMS) |
| 24+ hours | Lockdown mode (block more) |

**Fail Secure mode:**
- Cannot disable protection when offline
- Blocks additional categories (stricter)
- Shows notification: "Protection active (offline mode)"

**Implementation:**
- Client: track last successful heartbeat
- Client: escalate blocking when offline
- Server: cron job to check missing heartbeats

**Effort:** 2 days

---

#### 3.3 Offline Config Cache

**Problem:** What if server is legitimately down?
**Solution:** Encrypted config cache with expiration

```go
type SealedCache struct {
    EncryptedConfig []byte    // ChaCha20-Poly1305
    ServerSignature []byte    // Ed25519
    ExpiresAt       time.Time // Config validity
    ProtectionEnd   time.Time // Window end
}
```

**Rules:**
- Cache valid for 7 days from last sync
- If cache expired + no server → lockdown mode
- If cache tampered (signature fail) → lockdown mode

**Implementation:**
- Add `internal/config/cache.go`
- Store in obfuscated location (like backup)

**Effort:** 2 days

---

## Implementation Roadmap

### Phase 1: Local Hardening (MVP) - Week 1-2

| Task | Effort | Priority |
|------|--------|----------|
| LaunchDaemon migration | 1 day | P0 |
| Triple daemon (Sentinel) | 2 days | P0 |
| Quote typing + anti-paste | 1 day | P0 |
| Time delay (15 min) | 0.5 days | P1 |
| Testing + bug fixes | 1.5 days | P0 |

**Deliverable:** Local protection that takes 15+ min to bypass

### Phase 2: Server Infrastructure - Week 3-4

| Task | Effort | Priority |
|------|--------|----------|
| Server API scaffold | 2 days | P0 |
| Device registration + mTLS | 2 days | P0 |
| Heartbeat endpoint | 1 day | P0 |
| Signed config delivery | 2 days | P0 |
| Web dashboard (basic) | 3 days | P1 |

**Deliverable:** Server-controlled config, cannot edit locally

### Phase 3: Protection Windows - Week 5-6

| Task | Effort | Priority |
|------|--------|----------|
| Protection window model | 1 day | P0 |
| Dashboard window UI | 2 days | P0 |
| Unlock request flow | 2 days | P0 |
| Token generation + verify | 2 days | P0 |
| Testing | 1 day | P0 |

**Deliverable:** Time-based protection that cannot end early

### Phase 4: Accountability - Week 7-8

| Task | Effort | Priority |
|------|--------|----------|
| Accountability partner setup | 1 day | P1 |
| Email integration (SendGrid) | 1 day | P1 |
| SMS integration (Twilio) | 1 day | P2 |
| Alert job (cron) | 1 day | P1 |
| Dead man's switch logic | 2 days | P1 |
| Offline cache | 2 days | P1 |

**Deliverable:** Social accountability + fail-secure offline mode

---

## Threat Model Summary

| Attack | Current Bypass Time | After Phase 1 | After All Phases |
|--------|--------------------:|------------:|----------------:|
| Kill daemon | 10 sec | 5+ min | 5+ min |
| Delete plist | 5 sec | 5+ min | 5+ min |
| Delete binary | 30 sec | 2+ min | 2+ min |
| Edit config | 10 sec | N/A | Impossible (signature) |
| Block network | N/A | N/A | Triggers lockdown |
| Disable via CLI | 5 sec | 18+ min | Must wait for window |

**Goal achieved:** Bypass takes longer than urge duration (10-20 min)

---

## Cost Estimates

### Server Infrastructure

| Component | Monthly Cost |
|-----------|-------------:|
| Azure App Service (B1) | $13 |
| PostgreSQL (Basic) | $25 |
| SendGrid (free tier) | $0 |
| Twilio SMS (pay per use) | ~$5 |
| **Total** | **~$43/month** |

### Alternative: Serverless

| Component | Monthly Cost |
|-----------|-------------:|
| AWS Lambda | ~$1 |
| DynamoDB | ~$5 |
| API Gateway | ~$3 |
| SES (email) | ~$1 |
| SNS (SMS) | ~$5 |
| **Total** | **~$15/month** |

---

## Open Questions

1. **What cloud provider?** Azure (existing familiarity) vs AWS (cheaper serverless)?

2. **Web dashboard framework?** React, Vue, or simple server-rendered (Go templates)?

3. **Database choice?** PostgreSQL (relational) vs DynamoDB (serverless)?

4. **Unlock rate limit?** Once per week? Once per month? Configurable?

5. **Multi-device support?** Same account on multiple machines?

---

## Appendix: Files to Create/Modify

### New Files

```
internal/
├── daemon/
│   └── sentinel.go           # Third daemon
├── friction/
│   ├── quote.go              # Quote fetching
│   ├── antipaste.go          # Paste detection
│   └── delay.go              # Time delay
├── server/
│   ├── client.go             # Heartbeat, config sync
│   ├── window.go             # Protection windows
│   └── offline.go            # Fail-secure handling
├── crypto/
│   └── verify.go             # Ed25519 verification
├── config/
│   ├── sealed.go             # Encrypted config
│   └── cache.go              # Offline cache
└── notify/
    └── partner.go            # Accountability alerts
```

### Modified Files

```
internal/
├── daemon/
│   └── bootstrap.go          # 3-daemon coordination
├── infra/
│   └── launchd.go            # LaunchDaemon (not Agent)
└── cmd/appmon/
    └── main.go               # Friction commands
```

---

## Conclusion

This proposal transforms app_mon from a "helpful tool" to a "commitment device" that implements the **"Do Not Trust Yourself"** model. The phased approach allows:

1. **Quick win (Phase 1):** Local hardening in 2 weeks
2. **Core value (Phase 2-3):** Server control in 4 weeks
3. **Full solution (Phase 4):** Accountability in 2 weeks

Total timeline: **8 weeks** for complete server-controlled protection.
