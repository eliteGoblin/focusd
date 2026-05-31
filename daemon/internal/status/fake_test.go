package status

import "time"

// fakeSource is a deterministic, side-effect-free Source for probe and
// aggregation tests. Every field is a programmable seam.
type fakeSource struct {
	euid int

	// launchctl: keyed by domain+"/"+label → loaded; known reports whether
	// the answer is determinable (false ⇒ needs root we don't have).
	loaded     map[string]bool
	launchKnow bool

	files     map[string][]byte // path → content for ReadFile
	exists    map[string]bool   // path → FileExists
	procs     map[string]int    // execPath → running count
	pfEntries int
	pfKnown   bool

	// state.db: keyed by jobID → run + found.
	runs map[string]struct {
		run   JobRunInfo
		found bool
	}
	dbErr error

	now time.Time
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		euid:       501,
		loaded:     map[string]bool{},
		launchKnow: true,
		files:      map[string][]byte{},
		exists:     map[string]bool{},
		procs:      map[string]int{},
		pfKnown:    true,
		runs: map[string]struct {
			run   JobRunInfo
			found bool
		}{},
		now: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
}

func (f *fakeSource) Geteuid() int { return f.euid }

func (f *fakeSource) LaunchctlLoaded(domain, label string) (bool, bool) {
	if !f.launchKnow {
		return false, false
	}
	return f.loaded[domain+"/"+label], true
}

func (f *fakeSource) ReadFile(path string) ([]byte, error) {
	if b, ok := f.files[path]; ok {
		return b, nil
	}
	return nil, errNotExist
}

func (f *fakeSource) FileExists(path string) bool { return f.exists[path] }

func (f *fakeSource) CountProcesses(execPath string) (int, error) {
	return f.procs[execPath], nil
}

func (f *fakeSource) PfTableCount(anchor, table string) (int, bool) {
	return f.pfEntries, f.pfKnown
}

func (f *fakeSource) LastJobRun(dbPath, jobID string) (JobRunInfo, bool, error) {
	if f.dbErr != nil {
		return JobRunInfo{}, false, f.dbErr
	}
	r := f.runs[jobID]
	return r.run, r.found, nil
}

func (f *fakeSource) Now() time.Time { return f.now }

// errNotExist is a stand-in os.ErrNotExist for the fake's ReadFile.
var errNotExist = &fakeNotExist{}

type fakeNotExist struct{}

func (*fakeNotExist) Error() string { return "fake: file does not exist" }
