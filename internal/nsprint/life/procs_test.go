package life

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

// fakeProcs is a sampler on a fake clock and a canned ps: no process runs
// and no time passes but the test's own.
func fakeProcs(now *time.Time, out *string, err *error, runs *int) *procSampler {
	return &procSampler{
		now: func() time.Time { return *now },
		ps: func(ctx context.Context, argv []string) (string, error) {
			*runs++
			if strings.Join(argv, " ") != "ps -eww -o pid=,ppid=,etimes=,pcpu=,uid=,args=" {
				return "", errors.New("unexpected argv " + strings.Join(argv, " "))
			}
			return *out, *err
		},
		dirs: func() []string { return []string{"/units"} },
		read: func(dir string) []fleet.UnitFile {
			return []fleet.UnitFile{{File: "nova-hand.service", Body: "ExecStart=/x/hand\n"}}
		},
		goos: "linux", uid: 1000, self: 50,
		users: map[int]string{1000: "nova"},
	}
}

// TestProcsNowOneReadPerInterval: the beat reads ps at most once per
// fleet.PSEvery and carries the last sample between; a failed ps is a
// sample with its error, never an empty bench.
func TestProcsNowOneReadPerInterval(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	out := "  7 1 90000 0.0 1000 /x/stray --loop\n 50 1 10 1.0 1000 /x/nova-sprint bench beat\n"
	var psErr error
	runs := 0
	p := fakeProcs(&now, &out, &psErr, &runs)

	first := p.sample(context.Background())
	s, err := fleet.DecodePS(first)
	if err != nil || runs != 1 || s.At != now.Unix() || len(s.Old) != 1 || s.Old[0].Cmd != "/x/stray --loop" || s.Old[0].User != "nova" {
		t.Fatalf("first sample %s (runs %d, err %v)", first, runs, err)
	}
	if len(s.Units) != 1 || s.Units[0] != (fleet.PSUnit{Name: "nova-hand", State: fleet.UnitUndeclared}) {
		t.Fatalf("units = %+v", s.Units)
	}

	now = now.Add(fleet.PSEvery - time.Second)
	out = "garbage"
	if again := p.sample(context.Background()); again != first || runs != 1 {
		t.Fatalf("inside the interval: runs %d, sample changed: %s", runs, again)
	}

	now = now.Add(time.Second)
	psErr = errors.New("exit status 1")
	failed, _ := fleet.DecodePS(p.sample(context.Background()))
	if runs != 2 || failed.Err != "ps: exit status 1" || failed.At != now.Unix() {
		t.Fatalf("a failed ps: runs %d sample %+v", runs, failed)
	}
}
