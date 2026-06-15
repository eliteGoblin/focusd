#!/bin/bash
# Real-launchd self-heal verification for the FEATURE 10 mesh (ADR-0014).
#
# Installs the THROWAWAY test-mode mesh (fixed `com.focusd.daemon.e2e.*`
# labels, isolated workdir — NEVER touches a real/disguised install), then
# empirically measures the two manual-bypass vectors:
#   TEST 1  remove a worker's plist (bootout + rm)  → must re-heal  < 2s
#   TEST 2  kill a worker's process                 → must restart  < 2s
#
# This exists because fake-seam unit tests CANNOT catch real launchd timing
# (the interval/ThrottleInterval baked into the plists). Run it on macOS after
# any change to the mesh install / plist / worker loop:
#   bash daemon/scripts/e2e_selfheal.sh
set -u
U=$(id -u)
BIN=${BIN:-/tmp/focusd-e2e-daemon}
WD=${WD:-/tmp/focusd-e2e-wd}
LA="$HOME/Library/LaunchAgents"
A=com.focusd.daemon.e2e.a; B=com.focusd.daemon.e2e.b; E=com.focusd.daemon.e2e.ensure
HERE="$(cd "$(dirname "$0")/.." && pwd)"   # daemon module root
dom(){ echo "gui/$U/$1"; }
loaded(){ launchctl print "$(dom "$1")" >/dev/null 2>&1; }
cleanup(){ "$BIN" uninstall --test-mode >/dev/null 2>&1
  for L in "$A" "$B" "$E"; do launchctl bootout "$(dom "$L")" >/dev/null 2>&1; rm -f "$LA/$L.plist"; done
  rm -rf "$WD"; }
fail=0

echo "=== build e2e daemon (-tags e2e) ==="
( cd "$HERE" && go build -tags e2e -o "$BIN" ./cmd/daemon ) || { echo BUILD_FAIL; exit 1; }
cleanup; mkdir -p "$WD"
"$BIN" install --test-mode -v v0.0.0 --workdir "$WD" >/dev/null 2>&1
d=$(( $(date +%s) + 8 )); while :; do loaded "$A" && loaded "$B" && loaded "$E" && break; [ $(date +%s) -ge $d ] && break; done
echo "mesh up: A=$(loaded $A&&echo y||echo n) B=$(loaded $B&&echo y||echo n) ensure=$(loaded $E&&echo y||echo n)"

echo "=== TEST 1: remove worker B plist (8 samples across the tick cycle) ==="
max=0
for i in $(seq 1 8); do
  off=$(awk -v i="$i" 'BEGIN{printf "%.3f",(i*0.27)%2.0}')
  e=$(awk -v t="$(date +%s.%N)" -v o="$off" 'BEGIN{printf "%.3f",t+o}')
  while awk -v n="$(date +%s.%N)" -v e="$e" 'BEGIN{exit !(n<e)}'; do :; done
  t0=$(date +%s.%N); launchctl bootout "$(dom "$B")" >/dev/null 2>&1; rm -f "$LA/$B.plist"
  dl=$(( $(date +%s) + 12 )); ok=""
  while :; do [ -f "$LA/$B.plist" ] && loaded "$B" && { ok=1; break; }; [ $(date +%s) -ge $dl ] && break; done
  t1=$(date +%s.%N); ht=$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.2f",b-a}')
  [ -n "$ok" ] && echo "  sample $i: ${ht}s" || { echo "  sample $i: NOT HEALED"; fail=1; }
  max=$(awk -v m="$max" -v h="$ht" 'BEGIN{print (h>m)?h:m}')
done
echo "  worst-case: ${max}s"
awk -v m="$max" 'BEGIN{exit !(m<2.0)}' || { echo "  FAIL: worst-case >= 2s"; fail=1; }

echo "=== TEST 2: kill worker B process (must restart < 2s) ==="
p0=$(pgrep -f "run --r b" | head -1); t0=$(date +%s.%N); [ -n "$p0" ] && kill -9 "$p0" 2>/dev/null
dl=$(( $(date +%s) + 12 )); newp=""
while :; do p=$(pgrep -f "run --r b" | head -1); [ -n "$p" ] && [ "$p" != "$p0" ] && { newp="$p"; break; }; [ $(date +%s) -ge $dl ] && break; done
t1=$(date +%s.%N)
if [ -n "$newp" ]; then rt=$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.2f",b-a}'); echo "  restart: ${rt}s"
  awk -v r="$rt" 'BEGIN{exit !(r<2.0)}' || { echo "  FAIL: restart >= 2s"; fail=1; }
else echo "  FAIL: not restarted"; fail=1; fi

cleanup
[ "$fail" = 0 ] && echo "=== PASS: both bypass vectors heal < 2s ===" || echo "=== FAIL ==="
exit $fail
