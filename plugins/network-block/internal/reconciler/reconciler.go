// Package reconciler is the pure (testable) core of the network-block
// plugin: given a desired IP set (from DoH) and the current pf table
// contents (from pfctl show), it computes the diff and applies it via
// pfctl add/delete.
//
// The package intentionally knows nothing about the network — both the
// DNS resolver and the pfctl runner are passed in as interfaces so the
// unit tests can drive every code path with fakes.
//
// Failure semantics:
//   - If DoH lookups all fail, we surface a controlled failure: the
//     plugin exits 1. We refuse to apply *only* the deletions in this
//     case, because an empty desired set would wipe the table — that
//     would be data loss, not reconciliation.
//   - If pfctl show or pfctl add/delete fails, we surface controlled
//     failure (exit 1) too. The platform retries on its own schedule.
package reconciler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Resolver is the DoH interface the reconciler depends on. The real
// implementation lives in plugins/network-block/internal/dns.
type Resolver interface {
	ResolveA(ctx context.Context, name string) ([]string, error)
}

// PfTable is the pfctl-side interface: read the current set, apply
// adds, apply deletes.
type PfTable interface {
	Show(ctx context.Context) ([]string, error)
	Add(ctx context.Context, ip string) error
	Delete(ctx context.Context, ip string) error
}

// Reconciler holds the inputs. Logger is where per-op diagnostics are
// written (stderr in production, a *bytes.Buffer in tests).
type Reconciler struct {
	Resolver Resolver
	Pf       PfTable
	Domains  []string
	Logger   io.Writer
}

// Outcome is the structured result the CLI emits.
type Outcome struct {
	Added        []string `json:"added"`
	Removed      []string `json:"removed"`
	CurrentCount int      `json:"current_count"`
}

// Reconcile performs one resolve / diff / apply pass. Returns the
// outcome plus a boolean indicating "controlled failure" (true = exit
// 1, false + err=nil = ok). A non-nil err means the reconciler itself
// failed in a way the caller should treat as exit 1.
func (r *Reconciler) Reconcile(ctx context.Context) (Outcome, error) {
	if r.Resolver == nil || r.Pf == nil {
		return Outcome{}, errors.New("reconciler: Resolver and Pf are required")
	}
	if len(r.Domains) == 0 {
		return Outcome{}, errors.New("reconciler: no domains to resolve")
	}

	// Phase 1: gather the desired IP set from DoH.
	desired, resolvedDomains, err := r.resolveAll(ctx)
	if err != nil {
		return Outcome{}, err
	}
	// Safety belt: if NO domain resolved successfully we MUST NOT apply
	// the diff. desired would be empty and we'd delete every IP in the
	// table. That's data loss, not reconciliation.
	if resolvedDomains == 0 {
		return Outcome{}, errors.New("reconciler: no domain resolved; refusing to wipe table")
	}

	// Phase 2: read current table contents from pfctl.
	current, err := r.Pf.Show(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("reconciler: read table: %w", err)
	}
	currentSet := toSet(current)

	// Phase 3: compute the diff.
	added, removed := diff(desired, currentSet)

	// Phase 4: apply, logging each mutation. We continue on individual
	// failures so a single flaky entry doesn't block the rest, but we
	// remember the first error and surface it.
	var firstErr error
	for _, ip := range added {
		fmt.Fprintf(r.log(), "add %s\n", ip)
		if err := r.Pf.Add(ctx, ip); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("add %s: %w", ip, err)
		}
	}
	for _, ip := range removed {
		fmt.Fprintf(r.log(), "delete %s\n", ip)
		if err := r.Pf.Delete(ctx, ip); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("delete %s: %w", ip, err)
		}
	}

	out := Outcome{
		Added:        added,
		Removed:      removed,
		CurrentCount: len(currentSet) + len(added) - len(removed),
	}
	if firstErr != nil {
		return out, firstErr
	}
	return out, nil
}

// resolveAll queries DoH for every domain and unions the results.
// Returns (desired-set, count-of-domains-that-resolved-cleanly, err).
// err is non-nil only for catastrophic failures; a domain returning
// zero answers is treated as "resolved cleanly, zero IPs".
func (r *Reconciler) resolveAll(ctx context.Context) (map[string]struct{}, int, error) {
	desired := map[string]struct{}{}
	ok := 0
	for _, d := range r.Domains {
		ips, err := r.Resolver.ResolveA(ctx, d)
		if err != nil {
			fmt.Fprintf(r.log(), "resolve %s: %v\n", d, err)
			continue
		}
		ok++
		for _, ip := range ips {
			desired[ip] = struct{}{}
		}
	}
	return desired, ok, nil
}

// diff returns (toAdd, toRemove) given the desired set and the current
// set. Both result slices are sorted for deterministic apply order and
// stable test assertions.
func diff(desired, current map[string]struct{}) (add, remove []string) {
	for ip := range desired {
		if _, present := current[ip]; !present {
			add = append(add, ip)
		}
	}
	for ip := range current {
		if _, want := desired[ip]; !want {
			remove = append(remove, ip)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	return add, remove
}

func toSet(s []string) map[string]struct{} {
	m := make(map[string]struct{}, len(s))
	for _, x := range s {
		m[x] = struct{}{}
	}
	return m
}

func (r *Reconciler) log() io.Writer {
	if r.Logger == nil {
		return io.Discard
	}
	return r.Logger
}
