// Package relocate produces disguised, per-install random names/paths and
// copies the daemon binary into a hidden workdir, so there is no fixed
// "focusd" string to grep/kill (daemon_design.md §6). Ported in spirit
// from app_mon v0.6.1's relocator/obfuscator.
//
// Casual-grade friction only (a determined/AI user reading a plist still
// learns the path) — the durable commitment weight is the server.
//
// Pool sizing: the prefix and suffix pools are intentionally wide
// (60+ × 60+) and mix Apple subsystem prefixes with plausible
// third-party launchd labels commonly seen on a typical Mac developer
// machine (Adobe, Microsoft, Google, Docker, Slack, JetBrains, etc.).
// Combined with the 10-hex-char random tail (40 bits of entropy in the
// tail alone) this gives ~3.6e15 combinations vs the ~2M of the old
// 6×5×16-bit pool. Enumeration via a regex over `launchctl print` is
// no longer practical, and a single disguised label is now
// indistinguishable from any of the dozens of third-party background
// agents already present on a normal developer machine.
package relocate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// prefixes is the disguise pool of launchd-label prefixes. The mix is
// deliberately heterogeneous so a grep for "com.apple." against an
// enumerated label set is not sufficient — any of these entries is
// plausible on a real machine.
//
//   - Apple subsystems: the dominant family of legitimate launchd
//     labels on macOS (Spotlight, security, networking, identity,
//     telemetry, file providers). Common enough that one more "apple
//     metadata helper" agent draws zero attention.
//   - Adobe / Microsoft / Google / Mozilla / Dropbox / Spotify /
//     Zoom: ubiquitous consumer/productivity apps that all install
//     LaunchAgents (autoupdaters, signin helpers, notification
//     daemons).
//   - Developer tools (Docker, JetBrains, GitHub Desktop, VS Code,
//     Tailscale, 1Password, Slack, Notion, Figma, Linear, Brave,
//     Firefox, Arc): the target persona is a developer, so these
//     are the labels most likely already present.
var prefixes = []string{
	// Apple subsystems (heavily over-represented on real machines).
	"com.apple.metadata",
	"com.apple.cfprefsd",
	"com.apple.xpc",
	"com.apple.security",
	"com.apple.coreservices",
	"com.apple.spotlight",
	"com.apple.LaunchServices",
	"com.apple.networkserviceproxy",
	"com.apple.symptomsd",
	"com.apple.akd",
	"com.apple.assistantd",
	"com.apple.bird",
	"com.apple.diagnosticd",
	"com.apple.identityservicesd",
	"com.apple.locationd",
	"com.apple.mdworker",
	"com.apple.nsurlsessiond",
	"com.apple.pkd",
	"com.apple.trustd",
	"com.apple.containermanagerd",
	"com.apple.accountsd",
	"com.apple.routined",
	"com.apple.appleaccountd",
	"com.apple.commerce",
	"com.apple.cloudpaird",
	"com.apple.searchpartyd",
	"com.apple.notificationcenterd",
	"com.apple.usbmuxd",
	"com.apple.WindowServer.helper",
	"com.apple.UserEventAgent",
	// Adobe.
	"com.adobe.acc.installer",
	"com.adobe.AdobeIPCBroker",
	"com.adobe.CCXProcess",
	"com.adobe.GCInvoker",
	// Microsoft.
	"com.microsoft.autoupdate.helper",
	"com.microsoft.OneAuth",
	"com.microsoft.intune.mam",
	"com.microsoft.VSCode.helper",
	"com.microsoft.teams.TeamsUpdaterDaemon",
	// Google.
	"com.google.keystone.daemon",
	"com.google.GoogleUpdater.wake",
	"com.google.Chrome.helper",
	"com.google.drivefs",
	// Mozilla.
	"org.mozilla.updater",
	"org.mozilla.firefox.helper",
	// File-sync / cloud storage.
	"com.dropbox.DropboxMacUpdate",
	"com.box.desktop.helper",
	// Media.
	"com.spotify.webhelper",
	// Conferencing.
	"us.zoom.ZoomDaemon",
	"us.zoom.ZoomAutoUpdater",
	// Containers / virtualization.
	"com.docker.helper",
	"com.docker.vmnetd",
	// Networking.
	"io.tailscale.ipnextension",
	// Security / password managers.
	"com.1password.1password-launcher",
	"com.1password.1password-browser-helper",
	"com.bitwarden.desktop.helper",
	// Collaboration / productivity.
	"com.slack.Update",
	"com.slack.helper",
	"notion.id.helper",
	"com.figma.agent",
	"com.linear.helper",
	// VCS / dev tooling.
	"com.github.GitHubDesktop.helper",
	"com.jetbrains.toolbox.helper",
	"com.jetbrains.AppCode",
	"com.jetbrains.intellij.helper",
	// Browsers.
	"com.brave.Browser.helper",
	"company.thebrowser.Browser.helper",
	// Misc developer tools.
	"com.postmanlabs.mac.helper",
	"com.electron.helper",
}

// suffixes is the launchd-label role suffix pool. All entries are
// lowercase ASCII so the generated base satisfies a strict
// "<prefix>.<role>.<hex>" shape (see RandomBase).
var suffixes = []string{
	"helper",
	"agent",
	"daemon",
	"service",
	"updater",
	"sync",
	"analytics",
	"telemetry",
	"diagnostics",
	"crashreporter",
	"keyhelper",
	"loginitems",
	"bg",
	"mgr",
	"notifier",
	"relay",
	"proxy",
	"gateway",
	"cache",
	"index",
	"monitor",
	"watchdog",
	"installer",
	"uninstaller",
	"configd",
	"prefsd",
	"sessionhelper",
	"webhelper",
	"auth",
	"signin",
	"oauth",
	"pushd",
	"keystored",
	"xpcservice",
	"eventd",
	"metricsd",
	"reportd",
	"crashd",
	"gpud",
	"bird",
	"tcd",
	"mdmd",
	"nlcd",
	"assistd",
	"quicklookd",
	"routined",
	"accountsd",
	"nehelper",
	"sandboxd",
	"coreaudiod",
	"bluetoothd",
	"worker",
	"xpc",
	"broker",
	"dispatcher",
	"scheduler",
	"poller",
	"fetcher",
	"renderer",
	"indexer",
	"compositor",
	"observer",
	"reporter",
	"uploader",
	"downloader",
}

// randomTailBytes is the number of random bytes used for the hex tail
// in RandomBase / RandomBinaryName. 5 bytes → 10 lowercase-hex chars
// → 40 bits of entropy in the tail alone (≈10^12), which together with
// the (≥60)×(≥60) prefix/suffix pool yields ≈3.6e15 combinations.
const randomTailBytes = 5

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func pick(s []string) string {
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	return s[int(b[0])%len(s)]
}

// family returns the org/vendor segment of a launchd-label prefix: the
// first two dot-separated components (e.g. "com.apple", "com.google",
// "org.mozilla", "us.zoom", "io.tailscale", "notion.id"). Entries with
// fewer than two segments are returned whole. Used to assert the prefixes
// pool spans enough distinct vendors (see relocate tests); the FEATURE 19
// roster styles draw from their own pools (roster_styles.go).
func family(prefix string) string {
	i := strings.IndexByte(prefix, '.')
	if i < 0 {
		return prefix
	}
	j := strings.IndexByte(prefix[i+1:], '.')
	if j < 0 {
		return prefix
	}
	return prefix[:i+1+j]
}

// RandomBase is a single disguised, plausible-launchd-label base, e.g.
// "com.apple.metadata.helper.7f3a2c11ab". Format: "<prefix>.<suffix>.
// <10-hex>". The prefix is drawn from a pool that mixes Apple subsystems
// and plausible third-party bundle IDs so a single label is
// indistinguishable from the dozens of third-party background agents
// present on a normal Mac developer machine.
//
// NOTE (FEATURE 10 / ADR-0014): the mesh no longer shares one base
// across roles. The three mesh labels are generated independently by
// [GenerateRoster] (no shared prefix, no role-revealing suffix). This
// primitive remains for single disguised-label needs (e.g. self-update's
// download temp name via [RandomBinaryName]); it is NOT the mesh roster.
func RandomBase() string {
	return fmt.Sprintf("%s.%s.%s", pick(prefixes), pick(suffixes), randHex(randomTailBytes))
}

// GenerateRoster produces the three mesh labels (FEATURE 10 → FEATURE 19). Each
// role gets a STRUCTURALLY DISTINCT naming style from its own pools, so the
// three entries never read as a matching set (the owner's "3 look very similar"
// tell): role A is a dotted reverse-DNS bundle id (no hex tail), role B is a
// CamelCase agent (no dots), the ensurer is a lowercase unix-daemon name. None
// carry a role-revealing token (.a/.b/.ensure) or the "focusd" string. The
// returned slice is positional, aligned with osadapter.AllRoles (index 0 →
// RoleA, 1 → RoleB, 2 → RoleEnsure). All randomness uses crypto/rand (pick).
func GenerateRoster() []string {
	return []string{
		styleReverseDNS(), // RoleA
		styleCamelCase(),  // RoleB
		styleDaemon(),     // RoleEnsure
	}
}

// HiddenWorkdir is a dotted, Apple-metadata-looking directory under the
// given Application Support root (hidden from casual Finder/`ls`). The
// root is mode-specific (user → ~/Library/..., system → /Library/...),
// so a user and a system install never share a directory.
func HiddenWorkdir(supportRoot string) string {
	return filepath.Join(supportRoot, "."+pick(prefixes)+"."+randHex(6))
}

// RandomBinaryName is the disguised basename for the daemon binary inside its
// hidden workdir, e.g. "bundle.payload.archive.7f3a2c4d11" (three generic data
// nouns + 5 random bytes → 10 hex chars). FEATURE 19: it draws from its OWN pool
// (binWords) — deliberately disjoint from every mesh-label pool — so the binary
// (visible as argv[0] to root, the honest limitation) shares no stem with any
// launchd label, defeating a "find one, grep for the rest" pivot. The 10-hex
// tail keeps the path per-call unique so the self-update path can rotate the
// binary path on every update (macOS AMFI caches the CDHash per executable path,
// so re-using a path defeats adhoc-resign + restart; see
// internal/osadapter/selfupdate.go).
func RandomBinaryName() string {
	return pick(binWords) + "." + pick(binWords) + "." + pick(binWords) + "." + randHex(randomTailBytes)
}

// RelocateInto copies src into dir under a random disguised basename,
// 0755, and returns the new path (hard-link first; copy fallback).
func RelocateInto(src, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("relocate: mkdir %s: %w", dir, err)
	}
	dst := filepath.Join(dir, RandomBinaryName())
	if err := os.Link(src, dst); err == nil {
		_ = os.Chmod(dst, 0o755)
		return dst, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}
