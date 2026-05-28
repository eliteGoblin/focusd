# network-block

A focusd job plugin that reconciles a macOS `pf` table with the live A-record set
for a configured list of domains. Built for the Steam/Akamai/Cloudflare IP-rotation
problem: the user has a static pf anchor + table; the IPs behind the domains rotate
every few hours; this plugin closes that drift gap on a schedule.

## What it is (and isn't)

- It IS a reconciler. On every tick it runs DoH lookups for the configured domains,
  reads the current table contents, computes the diff, and applies adds/deletes.
- It is NOT a crypto barrier. `sudo pfctl -F all` wipes the table; nothing here
  prevents that. The plugin only narrows the window during which rotated IPs are
  reachable, not whether the user can manually tear down the anchor.

It also intentionally complements the sibling `dns-block` plugin, which pins the
same domains to `0.0.0.0` in `/etc/hosts`. The hosts entry blocks resolution via
the system resolver; this plugin blocks the IPs themselves, bypassing /etc/hosts
by going straight to Cloudflare DoH.

## Contract

```
network-block run --config <path-to-job-config.json>
```

The config JSON envelope is `{job_id, plugin_id, config:{...}}`. The `config`
object schema:

```json
{
  "anchor":   "focusd-block-steam",
  "table":    "steam_ips",
  "domains":  ["steampowered.com", "dota2.com", "..."],
  "resolver": "https://cloudflare-dns.com/dns-query"
}
```

Exit codes:

| code | meaning                                                          |
| ---- | ---------------------------------------------------------------- |
| 0    | success                                                          |
| 1    | controlled failure (DoH unreachable, pfctl missing, sudo denied) |
| 2    | plugin error (bad args, bad config)                              |

stdout is always JSON: `{"status","message","details":{"added","removed","current_count"}}`.
stderr carries per-op diagnostics (one line per add/delete/resolve).

## Hard rules

- This plugin MUST NOT modify `/etc/pf.conf` or `/etc/pf.anchors/*`. The user owns
  those files. We only mutate the table's runtime contents.
- pfctl is only invoked with `-T add`, `-T delete`, `-T show`. No other operations.
- The resolver URL must start with `https://`. Plain HTTP is rejected at config
  parse time so a typo can't silently downgrade us to plaintext DNS.
- If every DoH lookup fails the plugin refuses to apply the diff (which would
  otherwise wipe the table). That's a controlled failure (exit 1).

## Prerequisites

The user must have:

1. A pf anchor already loaded in the running ruleset, e.g.

   ```
   # /etc/pf.anchors/focusd-block-steam
   table <steam_ips> persist
   block drop out quick to <steam_ips>
   ```

   referenced from `/etc/pf.conf`:

   ```
   anchor "focusd-block-steam"
   load anchor "focusd-block-steam" from "/etc/pf.anchors/focusd-block-steam"
   ```

2. pf enabled (`sudo pfctl -e`).

3. A sudoers drop-in so this plugin can mutate the table without a password prompt.

## sudoers

Drop the file below at `/etc/sudoers.d/focusd-network-block`. Validate with
`visudo -cf /etc/sudoers.d/focusd-network-block` before saving.

```
# /etc/sudoers.d/focusd-network-block
%admin ALL=(root) NOPASSWD: /sbin/pfctl -a focusd-block-steam -t steam_ips -T add *
%admin ALL=(root) NOPASSWD: /sbin/pfctl -a focusd-block-steam -t steam_ips -T delete *
%admin ALL=(root) NOPASSWD: /sbin/pfctl -a focusd-block-steam -t steam_ips -T show
```

If you change the `anchor` or `table` names in the job config, you MUST update
these sudoers rules to match — they're matched on the exact argv prefix.

## Build & bundle

```
make build           # ./build/network-block
make test            # go test ./... -race -count=1
make bundle          # also copies into platform/internal/bundle/data/network-block/
```
