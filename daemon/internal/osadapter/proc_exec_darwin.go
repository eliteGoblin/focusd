//go:build darwin

package osadapter

import (
	"bytes"
	"errors"
	"unsafe"

	"golang.org/x/sys/unix"
)

// FEATURE 25 (C3) — kernel-authoritative executable resolution via libproc.
//
// The reaper classifies a candidate by its resolved EXECUTABLE PATH (anchored
// under SupportRoot, then signature-verified). Getting that path right is the
// crux: under HF4 disguise the platform child's argv0 is a bare generic token
// (`ps -o comm` reveals nothing) and its workdir is off both argv and env, so the
// ONLY disguise-proof handle on an orphaned platform is its kernel-recorded exec
// path. lsof can supply it too, but as a heavy system-wide subprocess that
// commonly exits non-zero and can miss a process's txt fd — a miss then drops the
// candidate into the argv0 fallback, which a disguised bare-token argv0 cannot
// anchor, so the orphan survives.
//
// libproc's proc_pidpath returns that exec path straight from the kernel for a
// single pid — cheap, no subprocess, and unaffected by any argv/workdir disguise.
// It even reports the (now-dangling) path of a process whose binary was unlinked,
// which sig.VerifyFile then classifies as fs.ErrNotExist (the deleted-binary
// tier). CGO is disabled project-wide (CGO_ENABLED=0), so this is a raw
// SYS_proc_info call rather than a libproc.h binding.
const (
	// sysProcInfo is XNU's proc_info syscall. BSD syscall numbers are shared
	// across darwin/arm64 and darwin/amd64, so a single constant is correct on
	// both supported Apple targets.
	sysProcInfo = 336
	// procInfoCallPIDInfo + procPIDPathInfo are the __proc_info sub-call and
	// flavor libproc's proc_pidpath() issues (PROC_INFO_CALL_PIDINFO,
	// PROC_PIDPATHINFO).
	procInfoCallPIDInfo = 2
	procPIDPathInfo     = 11
	// procPIDPathInfoMaxSize == PROC_PIDPATHINFO_MAXSIZE (4 * PATH_MAX): the
	// buffer proc_pidpath requires.
	procPIDPathInfoMaxSize = 4 * 1024
)

var errProcPidPathEmpty = errors.New("proc_pidpath: no path for pid")

// procPidPath returns pid's resolved executable path via libproc proc_pidpath.
// It returns a non-nil error on ANY failure (bad pid, syscall errno, empty
// result) so callers degrade to the lsof fallback rather than mistake an
// unresolved pid for "no executable" — fail-safe, never fail-open into a wrong
// path the reaper could act on.
func procPidPath(pid int) (string, error) {
	if pid <= 0 {
		return "", errProcPidPathEmpty
	}
	buf := make([]byte, procPIDPathInfoMaxSize)
	// The raw proc_info syscall returns 0 on SUCCESS (writing the NUL-terminated
	// path into buf) and sets errno on failure — libc's proc_pidpath wrapper
	// computes strlen(buf) itself, so we do the same here rather than read the
	// return value as a length. Pass the buffer pointer directly as the Syscall6
	// argument (the canonical pinned-pointer idiom the vet unsafeptr analyzer
	// recognises).
	_, _, errno := unix.Syscall6(
		uintptr(sysProcInfo),
		uintptr(procInfoCallPIDInfo),
		uintptr(pid),
		uintptr(procPIDPathInfo),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if errno != 0 {
		return "", errno
	}
	i := bytes.IndexByte(buf, 0)
	if i <= 0 {
		// i == 0: empty path (no usable result). i < 0: no NUL terminator (never
		// happens for proc_pidpath, which always terminates) — treat as no path.
		return "", errProcPidPathEmpty
	}
	return string(buf[:i]), nil
}
