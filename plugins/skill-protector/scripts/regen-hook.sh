#!/bin/sh
# Regenerate internal/reconciler/data/hook.sh from data/rule.md.
# Single source of truth = rule.md. The hook embeds the rule body
# inside a heredoc so the SessionStart hook can re-inject the rule
# without any plugin / daemon dependency.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"
RULE="${HERE}/internal/reconciler/data/rule.md"
HOOK="${HERE}/internal/reconciler/data/hook.sh"

if [ ! -f "${RULE}" ]; then
  echo "rule.md missing at ${RULE}" >&2
  exit 1
fi

TMP="${HOOK}.tmp.$$"
cat > "${TMP}" <<'__FOCUSD_HOOK_HEAD__'
#!/bin/sh
# focusd-protection SessionStart hook — re-injects the always-on rule
# if the user (or anything else) removed it between sessions. Fast and
# self-contained: no network, no plugin spawn, no daemon dependency.
#
# Generated from internal/reconciler/data/rule.md by `make build`.
# Do not edit by hand — the reconciler will overwrite it.

set -eu

HOME_DIR="${HOME:?HOME unset}"
RULE_DIR="${HOME_DIR}/.claude/rules/frank"
RULE="${RULE_DIR}/focusd-protection.md"
SENTINEL_BYTES=512

mkdir -p "${RULE_DIR}"
chmod 700 "${RULE_DIR}" 2>/dev/null || true

# If the rule file exists and is large enough, assume it is intact.
if [ -f "${RULE}" ]; then
  size=$(wc -c < "${RULE}" 2>/dev/null || echo 0)
  if [ "${size}" -ge "${SENTINEL_BYTES}" ]; then
    exit 0
  fi
fi

TMP="${RULE}.tmp.$$"
cat > "${TMP}" <<'__FOCUSD_RULE_EOF__'
__FOCUSD_HOOK_HEAD__

cat "${RULE}" >> "${TMP}"

cat >> "${TMP}" <<'__FOCUSD_HOOK_TAIL__'
__FOCUSD_RULE_EOF__

chmod 600 "${TMP}"
mv "${TMP}" "${RULE}"
__FOCUSD_HOOK_TAIL__

mv "${TMP}" "${HOOK}"
chmod 644 "${HOOK}"
echo "regenerated ${HOOK}"
