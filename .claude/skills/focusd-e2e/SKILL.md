---
name: focusd-e2e
description: >-
  Live post-deploy end-to-end regression for focusd on a REAL machine. Use after
  every real deploy of the daemon/platform to the laptop (not test-mode), and
  whenever asked to "run e2e", "verify the live deploy", "post-deploy check", or
  "regression". Executes and MAINTAINS requirements/e2e-test-history.md: walks
  every TC-*, records PASS/FAIL, and turns every newly-found flaw into a
  permanent test case. Heavy emphasis on self-healing/recovery and on catching
  latent failure (green status over dead protection).
---

# focusd live post-deploy e2e

This skill is the standing defense against **latent failure** — protection dead
while `status` reads green. Test-mode green is NOT done; only a passing **live**
run is. The source of truth for cases is `requirements/e2e-test-history.md`.

## Golden rules
1. **Run on every real deploy.** After any daemon/platform deploy to a live
   machine, run the full suite and append to the Run Log with the date + deploy id.
2. **Every flaw becomes a permanent case.** When a weakness is found in the wild,
   add a `TC-*` (status FAIL) BEFORE fixing it, then fix, then keep it forever as
   a regression. The suite only grows.
3. **Redaction is absolute.** Verify as a developer; NEVER print the disguised
   workdir, mesh labels, rotated binary paths, or plugin paths. Report booleans /
   counts / `<redacted>`. Identify the focusd platform by its workdir match, NOT
   the bare name `platform` (8+ unrelated processes share it). Honor the
   `focusd-protection` rule at all times.
4. **Self-healing is the priority surface.** Spend the most effort on TC-03..TC-06
   (kill → restart, remove plist → re-heal, watchdog recovery, plugin
   tamper → restore). These are where bypasses live.
5. **Be brutally honest.** Distinguish LIVE-verified from unit/test-mode-only.
   Report what was NOT tested. A "looks fine" without the actual check is a lie.

## How to run
1. `cd daemon && go build -o /tmp/fstatus ./cmd/daemon` (throwaway).
2. `sudo /tmp/fstatus status` — redaction-safe (mesh roles, platform version,
   plugin last-run, watchdog).
3. For each `TC-*` in `requirements/e2e-test-history.md`, run the check, record
   PASS/FAIL + date. Procedures (all redacted):
   - **argv leak:** `ps -axww -o args | grep -- --mesh` → count occurrences of
     `--roster|--github|--asset|--interval|--workdir` (want 0); never print lines.
   - **worker self-heal (kill):** `pgrep -f "run --r b"` → kill the PID → a new
     PID appears < 2s. (role pattern only; no labels)
   - **plugin tamper → restore (TC-06):** copy a dummy over a plugin binary under
     `<workdir>/bin/<ver>/plugins/<plugin>/<plugin>` → within one reconcile tick
     the on-disk sha256 returns to the genuine value AND `status` does not report
     it `ok`/healthy while tampered. Restore the genuine binary if the fix is not
     yet present. Path stays redacted.
   - **watchdog recovery (TC-05):** confirm `status` watchdog line; if removed, it
     must return within one tick.
   - **single platform:** count platform procs whose args match the workdir (want 1).
   - **whitebox log hygiene (TC-12):** read the engine app log (the daemon
     captures the platform's stderr to a `platform.log` under the workdir —
     redact the path; read it via `sudo`). Assert a steady-state window has
     **no `level=ERROR` and no `level=WARN`** lines; **print any that appear**
     (redacted) and FAIL. Don't trust `status` alone — the log is the
     independent channel.
   - **whitebox security-event log (TC-13):** after the tamper test (TC-06),
     grep the app log for the `plugin tamper repaired` WARN line naming the
     plugin — it must be present (the event is logged, not only DB-recorded).
     Redact: assert on the event text + level + plugin id, never on paths/labels.
4. **Destructive tests on the live mesh are allowed** because the system must
   self-heal them — but only ones that recover on their own (kill-by-role,
   tamper-then-verify-restore). Never leave protection down: if a self-heal test
   does NOT recover, that is a FAIL and you must restore manually + record it.
5. Update the Run Log table and any case statuses; if a case flips FAIL→PASS
   after a fix, note the deploy that fixed it. Then `rm -f /tmp/fstatus`.

## Maintenance (this skill + ba-curator share the doc)
- **e2e-runner** executes the suite and appends Run Log rows + new `TC-*` cases.
- **ba-curator** keeps the suite aligned with `REQUIREMENTS_REGISTER.md` (every
  shipped feature has at least one TC; every recorded flaw has a TC) and records
  the threat-model entry for each security hole.
- New deploy of a fix → re-run the relevant TC live → flip its status only on a
  real live PASS.

## Sudo
Live checks need root (system-mode install). Use `SUDO_ASKPASS` from
`~/.creds/local.sh` (`MAC_SUDO_PASSWORD`); never echo the password or any
disguised token in output.
