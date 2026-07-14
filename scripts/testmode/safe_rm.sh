# safe_rm.sh — belt-and-suspenders guard for test-mode teardown deletions.
#
# HF1 CRITICAL context: a bug in test-mode install let a recomputed scan root
# resolve to the operator's REAL ~/Library, so a constructed/empty path could
# delete something real. This function is the LAST line of defense for any shell
# harness that removes a "sandbox" path: it refuses to delete unless the resolved
# path is unambiguously a throwaway sandbox location.
#
# Usage (SOURCE it, don't exec it):
#   . scripts/testmode/safe_rm.sh
#   safe_rm "$SANDBOX_DIR"
#
# safe_rm deletes PATH only when ALL of these hold; otherwise it prints why and
# returns non-zero WITHOUT deleting:
#   1. non-empty                     (an empty/undefined var must never expand to `rm -rf ''`)
#   2. absolute                      (starts with '/', so no cwd-relative surprises)
#   3. length >= 12                  (rejects short root-ish paths like '/tmp' or '/')
#   4. resolved (symlinks followed) strictly UNDER a sandbox root:
#        /tmp, /private/tmp, or /var/folders  (macOS temp roots)
#   5. NOT $HOME, /, /Users, or any *…/Library path
#   6. exists on disk                (nothing to delete otherwise; a miss is a bug)
#
# It resolves symlinks on the path (and its parent) FIRST, so a symlinked
# "sandbox" that really points at a real dir is judged on its REAL location.

# _safe_rm_canon prints the canonical absolute path of $1 (symlinks resolved),
# or empty on failure. Works when the leaf may not itself be a symlink but a
# parent component is.
_safe_rm_canon() {
    _p="$1"
    [ -n "$_p" ] || { printf ''; return 1; }
    # Resolve the parent (must exist) then re-attach the leaf, so we canonicalize
    # even when the target is a plain dir under a symlinked parent.
    _dir=$(CDPATH= cd -- "$(dirname -- "$_p")" 2>/dev/null && pwd -P) || { printf ''; return 1; }
    _base=$(basename -- "$_p")
    if [ "$_base" = "/" ] || [ "$_base" = "." ]; then
        printf '%s' "$_dir"
    elif [ "$_dir" = "/" ]; then
        printf '/%s' "$_base"   # avoid a '//leaf' double slash when parent is root
    else
        printf '%s/%s' "$_dir" "$_base"
    fi
}

safe_rm() {
    _raw="$1"

    # 1. non-empty
    if [ -z "$_raw" ]; then
        printf 'safe_rm: REFUSED — empty path\n' >&2
        return 1
    fi

    # 2. absolute
    case "$_raw" in
        /*) : ;;
        *)  printf 'safe_rm: REFUSED — not absolute: %s\n' "$_raw" >&2; return 1 ;;
    esac

    # 6a. must exist (before canonicalizing the parent)
    if [ ! -e "$_raw" ]; then
        printf 'safe_rm: REFUSED — path does not exist: %s\n' "$_raw" >&2
        return 1
    fi

    # Resolve symlinks → judge the REAL location.
    _canon=$(_safe_rm_canon "$_raw")
    if [ -z "$_canon" ]; then
        printf 'safe_rm: REFUSED — cannot resolve path: %s\n' "$_raw" >&2
        return 1
    fi

    # 3. length >= 12 (guards against short root-ish paths)
    if [ "${#_canon}" -lt 12 ]; then
        printf 'safe_rm: REFUSED — resolved path too short (<12): %s\n' "$_canon" >&2
        return 1
    fi

    # 5. explicit denylist of dangerous real locations
    case "$_canon" in
        "$HOME"|"$HOME"/) printf 'safe_rm: REFUSED — is $HOME: %s\n' "$_canon" >&2; return 1 ;;
        /|/Users|/Users/) printf 'safe_rm: REFUSED — root/Users: %s\n' "$_canon" >&2; return 1 ;;
        */Library|*/Library/*) printf 'safe_rm: REFUSED — Library path: %s\n' "$_canon" >&2; return 1 ;;
    esac

    # 4. resolved path must be STRICTLY under a known sandbox root.
    _ok=0
    for _root in /tmp /private/tmp /var/folders /private/var/folders; do
        case "$_canon" in
            "$_root"/?*) _ok=1; break ;;
        esac
    done
    if [ "$_ok" -ne 1 ]; then
        printf 'safe_rm: REFUSED — not under a sandbox root (/tmp, /private/tmp, /var/folders): %s\n' "$_canon" >&2
        return 1
    fi

    # 6b. final existence re-check on the resolved path, then delete.
    if [ ! -e "$_canon" ]; then
        printf 'safe_rm: REFUSED — resolved path vanished: %s\n' "$_canon" >&2
        return 1
    fi

    rm -rf -- "$_canon"
}
