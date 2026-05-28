// Package pfctl is a narrow wrapper around the macOS pfctl(8) binary
// for read/modify access to a single named table inside a single named
// anchor.
//
// Scope is deliberately tiny:
//
//	pfctl -a <anchor> -t <table> -T show
//	pfctl -a <anchor> -t <table> -T add    <ip>
//	pfctl -a <anchor> -t <table> -T delete <ip>
//
// Nothing else. The plugin contract forbids touching /etc/pf.conf or
// the anchor file itself (the user owns those); we only mutate the
// runtime table contents. We invoke pfctl through sudo because table
// writes require root on darwin; sudoers must be pre-configured (see
// the plugin README).
//
// All command execution goes through an exec-seam (Runner.Exec) so
// tests can substitute a fake without ever touching the real binary.
package pfctl

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ExecFunc runs a command and returns its combined stdout+stderr and
// exit error. Matches the real os/exec semantics so tests can mimic
// CombinedOutput accurately.
type ExecFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// realExec is the production ExecFunc — invokes the real binary.
func realExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Runner is the entry point. Use NewRunner for production or build the
// struct directly in tests to swap Exec.
type Runner struct {
	Anchor string // pfctl anchor, e.g. "focusd-block-steam"
	Table  string // table name inside the anchor, e.g. "steam_ips"

	// Binary is the absolute path or name of the pfctl wrapper to run.
	// Defaults to "sudo" with PfctlPath as the first argument.
	Binary    string
	PfctlPath string

	// Exec is the seam; nil means use the real os/exec.
	Exec ExecFunc
}

// NewRunner returns a Runner with production defaults. The binary is
// "sudo" plus the canonical pfctl path "/sbin/pfctl" so the sudoers
// rules can match on an exact argv prefix.
func NewRunner(anchor, table string) *Runner {
	return &Runner{
		Anchor:    anchor,
		Table:     table,
		Binary:    "sudo",
		PfctlPath: "/sbin/pfctl",
	}
}

// argv builds the full argument vector for one pfctl invocation. The
// shape is "sudo /sbin/pfctl -a <anchor> -t <table> -T <op> [ip]". The
// op-then-ip ordering matters: matches the sudoers wildcard pattern.
func (r *Runner) argv(op, ip string) (string, []string) {
	args := []string{
		r.PfctlPath,
		"-a", r.Anchor,
		"-t", r.Table,
		"-T", op,
	}
	if ip != "" {
		args = append(args, ip)
	}
	return r.Binary, args
}

// run dispatches through the seam (or real exec if unset).
func (r *Runner) run(ctx context.Context, op, ip string) ([]byte, error) {
	if r.Anchor == "" || r.Table == "" {
		return nil, errors.New("pfctl: anchor and table are required")
	}
	bin, args := r.argv(op, ip)
	exe := r.Exec
	if exe == nil {
		exe = realExec
	}
	out, err := exe(ctx, bin, args...)
	if err != nil {
		return out, fmt.Errorf("pfctl %s: %w (output: %s)", op, err,
			strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Show reads the current set of IPs in the table. Output looks like:
//
//	1.2.3.4
//	5.6.7.8
//	2001:db8::1
//
// Whitespace and blank lines are tolerated. Non-IPv4 entries are
// returned as-is — caller is responsible for filtering if it wants
// only v4 (we keep this layer dumb).
func (r *Runner) Show(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "show", "")
	if err != nil {
		return nil, err
	}
	return parseShow(out), nil
}

// parseShow extracts one IP per line from pfctl -T show output.
// Splits on any whitespace, drops empties.
func parseShow(out []byte) []string {
	lines := strings.Split(string(out), "\n")
	ips := make([]string, 0, len(lines))
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" {
			continue
		}
		// pfctl can emit "1.2.3.4" or " 1.2.3.4 " or occasionally a
		// header line. Split on whitespace; take the first token.
		fields := strings.Fields(s)
		if len(fields) == 0 {
			continue
		}
		ips = append(ips, fields[0])
	}
	return ips
}

// Add inserts ip into the table. Idempotent on the pfctl side — adding
// an existing entry is a no-op exit-0.
func (r *Runner) Add(ctx context.Context, ip string) error {
	if ip == "" {
		return errors.New("pfctl: add requires a non-empty ip")
	}
	_, err := r.run(ctx, "add", ip)
	return err
}

// Delete removes ip from the table. Idempotent on the pfctl side.
func (r *Runner) Delete(ctx context.Context, ip string) error {
	if ip == "" {
		return errors.New("pfctl: delete requires a non-empty ip")
	}
	_, err := r.run(ctx, "delete", ip)
	return err
}
