package relocate

import "fmt"

// FEATURE 19 — non-clustering mesh names.
//
// The owner spotted the install because the three supervisor entries read as
// three near-identical "com.vendor.X.<hex>" siblings — same shape, same hex
// tail. Decorrelation (FEATURE 10) gave them distinct vendors but kept ONE
// visual form, so they still clustered.
//
// This file gives each of the three roles a STRUCTURALLY DISTINCT naming style
// drawn from its own pools, so the entries never read as a matching set:
//
//   role A (styleReverseDNS) → dotted reverse-DNS bundle id, NO hex tail
//                              e.g. "com.apple.coreservices.spotlight"
//   role B (styleCamelCase)  → CamelCase agent, NO dots
//                              e.g. "MicrosoftUpdateHelper"
//   role ensure (styleDaemon)→ lowercase unix-daemon-ish, trailing 'd'
//                              e.g. "trustlocationd"
//
// Entropy lives in varied PLAUSIBLE segments (not a uniform "<hex>" tail), the
// pools are large for grep-resistance, and ALL pool tokens are filename-safe
// (a label is also the plist filename stem in bootstrap()). The binary basename
// (RandomBinaryName) draws from its OWN disjoint pool so it shares no stem with
// any label.
//
// Casual-grade friction only (a determined/AI reader of a plist still learns
// the names) — the durable commitment weight is the server.

// --- styleReverseDNS (role A): dotted reverse-DNS, no hex tail ----------------

// dnsRoots are two-segment reverse-DNS vendor roots. Apple subsystems dominate
// (as on a real Mac) plus ubiquitous third-party vendors.
var dnsRoots = []string{
	"com.apple", "com.apple.security", "com.apple.coreservices",
	"com.apple.cloudkit", "com.apple.spotlight", "com.apple.identity",
	"com.apple.networking", "com.apple.metadata", "com.apple.accounts",
	"com.apple.commerce", "com.apple.search", "com.apple.assistant",
	"com.google", "com.google.keystone", "com.microsoft", "com.microsoft.office",
	"com.adobe", "com.adobe.acc", "org.mozilla", "com.dropbox",
	"com.docker", "io.tailscale", "com.spotify", "us.zoom",
	"com.1password", "com.bitwarden", "com.slack", "com.jetbrains",
	"com.github", "com.brave", "com.electron", "com.postmanlabs",
}

// dnsLeaves are lowercase component words appended (two independent picks) to a
// reverse-DNS root. All lowercase ASCII → dotted, filename-safe.
var dnsLeaves = []string{
	"helper", "agent", "service", "sync", "cache", "index", "session",
	"account", "identity", "keychain", "cloud", "share", "notify", "session2",
	"telemetry", "diagnostics", "report", "update", "fetch", "scheduler",
	"broker", "relay", "proxy", "gateway", "monitor", "observer", "registry",
	"prefs", "config", "store", "vault", "locker", "metrics", "events",
	"trust", "location", "search", "spotlight", "coreservices", "metadata",
	"signin", "oauth", "push", "background", "maintenance", "snapshot",
	"catalog", "directory", "resolver", "dispatcher",
}

func styleReverseDNS() string {
	return fmt.Sprintf("%s.%s.%s", pick(dnsRoots), pick(dnsLeaves), pick(dnsLeaves))
}

// --- styleCamelCase (role B): CamelCase, no dots ------------------------------

var camelVendors = []string{
	"Adobe", "Microsoft", "Google", "Dropbox", "Zoom", "Slack", "Docker",
	"Spotify", "Notion", "Figma", "Linear", "Brave", "Mozilla", "Tailscale",
	"JetBrains", "Postman", "GitHub", "OnePassword", "Bitwarden", "Box",
	"Citrix", "Logitech", "Webex", "Grammarly",
}

var camelMids = []string{
	"Update", "IPC", "Sync", "Account", "Notification", "Session", "Cloud",
	"Crash", "Login", "Telemetry", "Software", "Auto", "Push", "Identity",
	"Security", "Media", "Network", "Device", "Backup", "License", "Config",
	"Diagnostics", "Metrics", "Activation",
}

var camelSuffixes = []string{
	"Helper", "Agent", "Daemon", "Service", "Broker", "Worker", "Handler",
	"Monitor", "Manager", "Runner",
}

func styleCamelCase() string {
	return pick(camelVendors) + pick(camelMids) + pick(camelSuffixes)
}

// --- styleDaemon (role ensure): lowercase unix-daemon-ish, trailing 'd' -------

// daemonRoots are lowercase single words; two are concatenated and a 'd' is
// appended so the result reads like a BSD/Apple system daemon (mdworkerd,
// nsurlsessiond, …). No dots, no CamelCase → distinct from the other two styles.
var daemonRoots = []string{
	"trust", "location", "session", "account", "search", "sandbox", "notify",
	"cache", "index", "sync", "cloud", "media", "audio", "power", "thermal",
	"network", "route", "symptoms", "analytics", "spotlight", "metadata",
	"identity", "security", "commerce", "assist", "remind", "calendar",
	"contacts", "photos", "backup", "keychain", "preferences", "container",
	"diagnostics", "telemetry", "usage", "biome", "knowledge", "duet", "intent",
}

func styleDaemon() string {
	return pick(daemonRoots) + pick(daemonRoots) + "d"
}

// --- binary basename pool (RandomBinaryName): disjoint from all label pools ----

// binWords is a dedicated pool of generic file/data nouns used ONLY for the
// daemon binary basename — deliberately disjoint from every label pool above so
// the binary (visible as argv[0] to root) shares no stem with any launchd label.
var binWords = []string{
	"bundle", "payload", "manifest", "archive", "blob", "spool", "depot",
	"satchel", "ledger", "shard", "segment", "scratch", "staging", "parcel",
	"crate", "pallet", "stash", "trove", "hoard", "cabinet",
}
