package osadapter

import (
	"crypto/rand"
)

// FEATURE 24 (HF4) — platform + plugin process-identity disguise.
//
// WorkdirEnvKey is the environment variable the DAEMON uses to hand the
// platform-workdir to the platform child, instead of putting `--workdir <path>`
// on the command line where `ps` exposes it to root. The platform reads it in
// parseCommon when no explicit --workdir flag is given. MUST match the daemon's
// platformsvc.WorkdirEnvKey (separate modules ⇒ duplicated literal, like the
// mesh-role MeshEnvKey). The key is deliberately opaque — it names neither focusd
// nor 'workdir'.
const WorkdirEnvKey = "APP_STATE_DIR"

// pluginProcTokens is the pool for a plugin child's disguised argv[0]. The runner
// overrides the child's argv[0] with a generic token so a live `ps` shows e.g.
// `worker run` instead of `.../kill-steam run --config /tmp/focusd-job-...`. The
// tokens carry no plugin id, no 'focusd', no path. Generic worker/helper nouns
// that read as any background helper.
var pluginProcTokens = []string{
	"worker", "helper", "agent", "runner", "task", "job",
	"service", "handler", "daemon", "monitor", "broker", "dispatcher",
	"scanner", "reconciler", "sweeper", "probe", "sentinel", "guard",
}

// RandomPluginArgv0 returns a random disguised argv[0] token for a plugin child
// (HF4). Per-exec random (crypto/rand) — a plugin child needs no stable identity
// (nothing greps it back), only the absence of a revealing token. Falls back to
// the first pool entry if the RNG is somehow unavailable (never in practice).
func RandomPluginArgv0() string {
	b := make([]byte, 1)
	if _, err := rand.Read(b); err != nil {
		return pluginProcTokens[0]
	}
	return pluginProcTokens[int(b[0])%len(pluginProcTokens)]
}
