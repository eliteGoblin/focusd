package defaultconfig

import (
	"reflect"
	"testing"

	"github.com/eliteGoblin/focusd/platform/internal/core/config"
)

// The embedded default must always parse — it is the sole policy source on
// the daemon-managed run path.
func TestLoadParsesEmbeddedDefault(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Jobs) == 0 {
		t.Fatal("embedded default has no jobs — that's the wrong baseline")
	}
}

// Load is path-free by design: there is no override to merge, so two calls
// return the identical enforced policy regardless of anything on disk.
func TestLoadIsDeterministicAndOverrideFree(t *testing.T) {
	a, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Compare the WHOLE config, not just the job count: two loads must be the
	// identical enforced policy — same platform fields, same jobs in the same
	// order with the same fields, same services — so non-determinism in job
	// ordering/fields, services, or platform fields (e.g. map iteration order
	// or mutation of a shared map) can't slip through a count-only check.
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Load must return an identical policy on every call;\n a=%+v\n b=%+v", a, b)
	}
}

// Requirement (c): network-block ships ENABLED in the embedded default,
// with the anchor/table/resolver/domains the plugin needs. It used to be
// enabled only via the now-removed workdir override, so if it regressed to
// disabled here net-block would silently stop being enforced.
func TestNetworkBlockShipsEnabled(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	j := findJob(cfg.Jobs, "network-block-reconcile")
	if j == nil {
		t.Fatal("network-block-reconcile job missing from embedded default")
	}
	if !j.Enabled {
		t.Fatal("network-block-reconcile must be enabled in the embedded default")
	}
	// The plugin (plugins/network-block/cmd/main.go loadConfig) requires all
	// four keys; assert they are present and well-formed so a config edit
	// can't ship net-block "enabled" but inert.
	if s, _ := j.Config["anchor"].(string); s == "" {
		t.Error("network-block config.anchor missing/empty")
	}
	if s, _ := j.Config["table"].(string); s == "" {
		t.Error("network-block config.table missing/empty")
	}
	resolver, _ := j.Config["resolver"].(string)
	if len(resolver) < 8 || resolver[:8] != "https://" {
		t.Errorf("network-block config.resolver must be an https:// DoH URL, got %q", resolver)
	}
	domains, ok := j.Config["domains"].([]any)
	if !ok || len(domains) == 0 {
		t.Fatal("network-block config.domains missing/empty")
	}
	for i, d := range domains {
		if s, ok := d.(string); !ok || s == "" {
			t.Errorf("network-block config.domains[%d] must be a non-empty string", i)
		}
	}
}

func findJob(jobs []config.Job, id string) *config.Job {
	for i := range jobs {
		if jobs[i].ID == id {
			return &jobs[i]
		}
	}
	return nil
}
