# NextDNS Setup Guide

**Account**: `frankbluemind.focusd@outlook.com`

## How It Works

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         NEXTDNS IP BINDING                                  │
│                                                                             │
│   All devices behind your router share ONE public IP (NAT):                │
│                                                                             │
│   Mac ─────┐                                                                │
│   iPhone ──┼──► Eero Router ──► Public IP: 159.196.169.242 ──► Internet    │
│   iPad ────┘         │                                                      │
│                      │                                                      │
│   NextDNS "Linked IP" binds your public IP to your profile:                │
│                                                                             │
│   "159.196.169.242 = Profile 395ea7 → Apply denylist"                      │
│                                                                             │
│   If public IP changes (router restart), NextDNS agent on Mac              │
│   auto-updates the binding. All devices stay protected.                    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Two components:**
1. **Router DNS** → All devices use NextDNS
2. **Mac Agent** → Keeps Linked IP updated if it changes

---

## Setup

### Step 1: Link IP in NextDNS Dashboard

1. Go to https://my.nextdns.io → **Setup** tab
2. Scroll to **Linked IP** → Click **Link IP**
3. Note your DNS servers (shown after linking):
   - Primary: `45.90.28.102`
   - Secondary: `45.90.30.102`

### Step 2: Configure Router (Eero Pro 7)

1. Eero app → **Settings** → **Network Settings** → **DNS**
2. Select **Custom DNS**
3. Enter: `45.90.28.102` / `45.90.30.102`
4. Save (network reboots)

### Step 3: Install NextDNS Agent on Mac

```bash
# Install via Homebrew
brew install nextdns

# Install as system service
sudo nextdns install

# Configure with your profile
sudo nextdns config set -profile 395ea7

# Restart to apply
sudo nextdns restart

# Verify running
sudo nextdns status
# Output: running
```

---

## Verify Setup

### Test 1: Browser Test Page

Open: **https://test.nextdns.io**

Expected:
```
✓ Using NextDNS
  Protocol: DOH
  Profile: 395ea7
```

### Test 2: Terminal

```bash
# Check status
sudo nextdns status

# Test blocked domain (after adding to denylist)
nslookup steampowered.com
# Should return: 0.0.0.0
```

### Test 3: NextDNS Dashboard

Go to https://my.nextdns.io → **Logs** → See your queries appearing

---

## Add Blocks (Denylist)

Go to https://my.nextdns.io → **Denylist** → Add domains:

### Steam (Core)
```
steampowered.com
steamcommunity.com
steamstatic.com
steamusercontent.com
steamcontent.com
valvesoftware.com
```

---

## iPhone Setup & Verification

### Verify iPhone Using NextDNS

1. Connect to WiFi (not cellular)
2. Open **Safari** → https://test.nextdns.io
3. Should show: **Using NextDNS** with profile `395ea7`

**Note:** May take 1-2 minutes for DNS to propagate after router config change.

### If "Unconfigured" on iPhone:

**1. Force DNS Refresh (most common fix):**
- Airplane Mode ON → wait 10 sec → OFF
- Or restart iPhone

**2. Verify DNS is Automatic:**
- Settings → Wi-Fi → tap ⓘ → **Configure DNS** → **Automatic**
- Should show NextDNS IPs: `45.90.28.102`, `45.90.30.102`

**3. Disable Private Relay (if enabled):**
- Settings → [Your Name] → iCloud → **Private Relay** → OFF

**4. Disable Limit IP Tracking:**
- Settings → Wi-Fi → tap ⓘ → **Limit IP Address Tracking** → OFF

---

## Propagation / Latency

**Blocks not working immediately?** DNS caching causes delay:

| Device | How to Force Update |
|--------|---------------------|
| **Mac** | `sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder` |
| **iPhone/iPad** | Airplane Mode ON → OFF, or restart device |
| **Browser** | Close all tabs, restart browser. Disable Secure DNS. |

---

## Troubleshooting

**"Not using NextDNS" on test page:**
```bash
# Restart agent
sudo nextdns restart

# Flush DNS cache
sudo dscacheutil -flushcache
sudo killall -HUP mDNSResponder
```

**Browser bypassing DNS:**
- Chrome: Settings → Privacy → Security → "Use secure DNS" → OFF
- Disable iCloud Private Relay if enabled

---

## Quick Reference

| Item | Value |
|------|-------|
| Profile ID | `395ea7` |
| DNS Servers | `45.90.28.102` / `45.90.30.102` |
| Dashboard | https://my.nextdns.io |
| Test Page | https://test.nextdns.io |
| Agent Status | `sudo nextdns status` |
| Agent Restart | `sudo nextdns restart` |
