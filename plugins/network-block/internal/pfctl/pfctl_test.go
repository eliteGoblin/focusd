package pfctl

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeExec records every invocation and returns canned output. It's the
// single mechanism for unit-testing the pfctl wrapper without hitting
// the real binary.
type fakeExec struct {
	calls []call
	out   []byte
	err   error
}

type call struct {
	bin  string
	args []string
}

func (f *fakeExec) Exec(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{bin: name, args: append([]string{}, args...)})
	return f.out, f.err
}

func newRunner(t *testing.T, f *fakeExec) *Runner {
	t.Helper()
	r := NewRunner("focusd-block-steam", "steam_ips")
	r.Exec = f.Exec
	return r
}

func TestArgv_Shape(t *testing.T) {
	r := NewRunner("anchor1", "tbl1")
	bin, args := r.argv("show", "")
	if bin != "sudo" {
		t.Errorf("bin = %q, want sudo", bin)
	}
	want := []string{"/sbin/pfctl", "-a", "anchor1", "-t", "tbl1", "-T", "show"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("argv show = %v, want %v", args, want)
	}

	_, args = r.argv("add", "1.2.3.4")
	want = []string{"/sbin/pfctl", "-a", "anchor1", "-t", "tbl1", "-T", "add", "1.2.3.4"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("argv add = %v, want %v", args, want)
	}
}

func TestShow_ParsesOutput(t *testing.T) {
	f := &fakeExec{out: []byte("   1.2.3.4\n5.6.7.8\n\n   9.10.11.12  \n")}
	r := newRunner(t, f)

	ips, err := r.Show(context.Background())
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	want := []string{"1.2.3.4", "5.6.7.8", "9.10.11.12"}
	if !reflect.DeepEqual(ips, want) {
		t.Errorf("got %v, want %v", ips, want)
	}

	if len(f.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(f.calls))
	}
	got := f.calls[0]
	if got.bin != "sudo" {
		t.Errorf("bin %q, want sudo", got.bin)
	}
	if !reflect.DeepEqual(got.args, []string{
		"/sbin/pfctl", "-a", "focusd-block-steam", "-t", "steam_ips", "-T", "show",
	}) {
		t.Errorf("args = %v", got.args)
	}
}

func TestShow_EmptyTable(t *testing.T) {
	f := &fakeExec{out: []byte("\n   \n")}
	r := newRunner(t, f)

	ips, err := r.Show(context.Background())
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(ips) != 0 {
		t.Errorf("want empty, got %v", ips)
	}
}

func TestShow_ExecError(t *testing.T) {
	f := &fakeExec{out: []byte("pfctl: not permitted"), err: errors.New("exit status 1")}
	r := newRunner(t, f)

	if _, err := r.Show(context.Background()); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "pfctl") {
		t.Errorf("error %q should mention pfctl", err)
	}
}

func TestAdd_BuildsCorrectArgv(t *testing.T) {
	f := &fakeExec{}
	r := newRunner(t, f)

	if err := r.Add(context.Background(), "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(f.calls))
	}
	want := []string{"/sbin/pfctl", "-a", "focusd-block-steam", "-t", "steam_ips", "-T", "add", "192.0.2.10"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("argv = %v, want %v", f.calls[0].args, want)
	}
}

func TestDelete_BuildsCorrectArgv(t *testing.T) {
	f := &fakeExec{}
	r := newRunner(t, f)

	if err := r.Delete(context.Background(), "192.0.2.20"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/sbin/pfctl", "-a", "focusd-block-steam", "-t", "steam_ips", "-T", "delete", "192.0.2.20"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("argv = %v, want %v", f.calls[0].args, want)
	}
}

func TestAdd_EmptyIPRejected(t *testing.T) {
	r := newRunner(t, &fakeExec{})
	if err := r.Add(context.Background(), ""); err == nil {
		t.Error("empty IP should be rejected")
	}
}

func TestDelete_EmptyIPRejected(t *testing.T) {
	r := newRunner(t, &fakeExec{})
	if err := r.Delete(context.Background(), ""); err == nil {
		t.Error("empty IP should be rejected")
	}
}

func TestRun_MissingAnchorOrTable(t *testing.T) {
	r := &Runner{Binary: "sudo", PfctlPath: "/sbin/pfctl"}
	if _, err := r.run(context.Background(), "show", ""); err == nil {
		t.Error("missing anchor/table should error")
	}
}

func TestExecError_SurfacesOutput(t *testing.T) {
	f := &fakeExec{out: []byte("pfctl: pf not enabled"), err: errors.New("exit 1")}
	r := newRunner(t, f)

	err := r.Add(context.Background(), "1.2.3.4")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pf not enabled") {
		t.Errorf("error should include pfctl stderr, got %q", err)
	}
}

func TestParseShow_OneIPPerLine(t *testing.T) {
	in := []byte("1.1.1.1\n2.2.2.2\n3.3.3.3\n")
	got := parseShow(in)
	want := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseShow_TolerantWhitespace(t *testing.T) {
	in := []byte("\t  1.1.1.1\t\n  \t2.2.2.2   \n")
	got := parseShow(in)
	want := []string{"1.1.1.1", "2.2.2.2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
