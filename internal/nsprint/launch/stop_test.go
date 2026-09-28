package launch

import (
	"bytes"
	"context"
	"testing"
	"time"
)

type fakeGroups struct {
	ids      map[string][]int
	alive    map[int]bool
	stubborn map[int]bool
	signals  []string
}

func (f *fakeGroups) Find(_ context.Context, id string) ([]int, error) { return f.ids[id], nil }
func (f *fakeGroups) Signal(_ context.Context, p int, s string) error {
	f.signals = append(f.signals, s)
	if s == "TERM" && !f.stubborn[p] {
		f.alive[p] = false
	}
	if s == "KILL" && !f.stubborn[p] {
		f.alive[p] = false
	}
	return nil
}
func (f *fakeGroups) Alive(_ context.Context, p int) (bool, error) { return f.alive[p], nil }
func TestCardStopSignalsOnlyExactAttemptGroups(t *testing.T) {
	t.Parallel()

	f := &fakeGroups{ids: map[string][]int{"nova-card s/a/1": {11}, "nova-card s/c/3": {33}}, alive: map[int]bool{11: true, 33: true}, stubborn: map[int]bool{33: true}}
	var out bytes.Buffer
	sum, err := Stop(context.Background(), bytes.NewBufferString("s a 1\ns b 2\ns c 3\n"), &out, time.Second, f, func(context.Context, time.Duration) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if sum.Stopped != 1 || sum.Gone != 1 || sum.Alive != 1 {
		t.Fatalf("summary %+v", sum)
	}
	want := "STOPPED s/a/1\nGONE s/b/2\nALIVE s/c/3\n"
	if out.String() != want {
		t.Fatalf("out %q want %q", out.String(), want)
	}
	if len(f.signals) != 3 || f.signals[0] != "TERM" || f.signals[1] != "TERM" || f.signals[2] != "KILL" {
		t.Fatalf("signals %v", f.signals)
	}
}

// TestCardStopCommandProtocol pins the bench-side `card stop` contract the
// reset's RemoteStopper parses (#3589 rowan hold 2): stdout holds only the
// per-card lines, the summary goes to stderr, and a completed protocol with an
// ALIVE card is a nil error (the command exits 0; ALIVE is data).
func TestCardStopCommandProtocol(t *testing.T) {
	t.Parallel()

	f := &fakeGroups{ids: map[string][]int{"nova-card s/a/1": {11}, "nova-card s/c/3": {33}}, alive: map[int]bool{11: true, 33: true}, stubborn: map[int]bool{33: true}}
	var out, errOut bytes.Buffer
	err := StopCommand(context.Background(), bytes.NewBufferString("s a 1\ns b 2\ns c 3\n"), &out, &errOut, time.Second, f, func(context.Context, time.Duration) error { return nil })
	if err != nil {
		t.Fatalf("ALIVE must not fail the protocol: %v", err)
	}
	if want := "STOPPED s/a/1\nGONE s/b/2\nALIVE s/c/3\n"; out.String() != want {
		t.Fatalf("stdout %q want only the per-card lines %q", out.String(), want)
	}
	if want := "STOP stopped=1 gone=1 alive=1\n"; errOut.String() != want {
		t.Fatalf("stderr %q want %q", errOut.String(), want)
	}
	out.Reset()
	errOut.Reset()
	if err := StopCommand(context.Background(), bytes.NewBufferString("s a\n"), &out, &errOut, time.Second, f, nil); err == nil {
		t.Fatal("a malformed line must fail the protocol")
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("a failed protocol wrote stdout=%q stderr=%q", out.String(), errOut.String())
	}
}
