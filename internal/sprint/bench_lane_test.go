package sprint

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBenchShell is the bench a test runs a lane on: it records each line in order and
// answers each by the first rule whose substring the line holds. No socket and no ssh.
type fakeBenchShell struct {
	lines []string
	rules []fakeBenchRule
}

type fakeBenchRule struct {
	has  string
	out  string
	code int
	err  error
}

func (f *fakeBenchShell) Shell(ctx context.Context, host, line string, stdout, _ io.Writer) (int, error) {
	f.lines = append(f.lines, host+": "+line)
	if ctx.Err() != nil {
		return NoBenchAnswer, ctx.Err()
	}
	for _, r := range f.rules {
		if strings.Contains(line, r.has) {
			if _, err := io.WriteString(stdout, r.out); err != nil {
				return 0, err
			}
			return r.code, r.err
		}
	}
	return 0, nil
}

// A lane the machinery starts on a bench (a worker, a reader, a lander, a bench gate) runs
// in its own job directory, with TMPDIR and GOTMPDIR inside it and GOCACHE the bench's one
// shared cache, and removes that directory at its end whatever the verdict: a command that
// passes, one that fails, one whose bench drops it, and one whose context is cancelled
// (docs/SPEC-SPRINT.md section 18, bench lanes). Before this, a lane ran with the bench's
// shared temporary directory and left its files there.
func TestALaneRunsInItsOwnTmpAndRemovesIt(t *testing.T) {
	t.Parallel()
	lane := BenchLane{Kind: BenchLaneWorker, Job: "bench-lanes-clean-their-own-tmp.w1~15"}
	dir := "nova-bench/lanes/worker/bench-lanes-clean-their-own-tmp.w1~15"
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name  string
		ctx   context.Context
		rules []fakeBenchRule
		code  int
		err   bool
		ran   bool // the command's line was sent
	}{
		{name: "a command that passes", ctx: context.Background(), ran: true},
		{name: "a command that fails", ctx: context.Background(), rules: []fakeBenchRule{{has: "nice -n 19", code: 1}}, code: 1, ran: true},
		{name: "a command the bench drops", ctx: context.Background(), rules: []fakeBenchRule{{has: "nice -n 19", code: NoBenchAnswer, err: errors.New("broken pipe")}}, code: NoBenchAnswer, err: true, ran: true},
		{name: "a make the bench refuses", ctx: context.Background(), rules: []fakeBenchRule{{has: "mkdir -p", code: 1}}, code: 1, err: true},
		{name: "a lane whose context is cancelled", ctx: cancelled, code: NoBenchAnswer, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sh := &fakeBenchShell{rules: tc.rules}
			res, err := RunBenchLane(tc.ctx, sh, "bench-a", lane, BenchLaneOptions{}, []string{"go", "test", "./internal/sprint"})
			if tc.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.code, res.Code, "the lane's code is its command's")
			assert.Equal(t, dir, res.Dir)
			assert.True(t, res.Removed, "the lane's directory is removed whatever the verdict: %v", res.RemoveErr)
			require.NotEmpty(t, sh.lines)
			assert.Equal(t, "bench-a: rm -rf -- '"+dir+"'", sh.lines[len(sh.lines)-1], "the last line is the remove, of the lane's own directory and nothing else")
			var exec string
			for _, l := range sh.lines {
				if strings.Contains(l, "nice -n 19") {
					exec = l
				}
			}
			if !tc.ran {
				assert.Empty(t, exec, "a lane whose directory was not made runs nothing")
				return
			}
			assert.Contains(t, exec, "cd '"+dir+"/repo'")
			assert.Contains(t, exec, "TMPDIR=\"$HOME\"/'"+dir+"/tmp'", "TMPDIR is inside the job directory, under the home")
			assert.Contains(t, exec, "GOTMPDIR=\"$HOME\"/'"+dir+"/tmp'", "GOTMPDIR is inside the job directory, under the home")
			assert.Contains(t, exec, "GOCACHE=\"$HOME\"/'"+BenchCacheDefault+"'", "GOCACHE is the bench's one shared cache, under the home")
			assert.NotContains(t, exec, "/tmp/", "nothing is put in the bench's shared temporary directory")
			assert.Contains(t, exec, "nice -n 19 'go' 'test' './internal/sprint'")
		})
	}

	t.Run("each kind runs in its own directory", func(t *testing.T) {
		t.Parallel()
		for _, kind := range []string{BenchLaneWorker, BenchLaneReader, BenchLaneLander, BenchLaneGate} {
			sh := &fakeBenchShell{}
			job := "job-" + kind
			res, err := RunBenchLane(context.Background(), sh, "bench-a", BenchLane{Kind: kind, Job: job}, BenchLaneOptions{}, []string{"true"})
			require.NoError(t, err, kind)
			want := BenchLaneRoot + "/" + kind + "/" + job
			assert.Equal(t, want, res.Dir, kind)
			assert.True(t, res.Removed, kind)
			joined := strings.Join(sh.lines, "\n")
			assert.Contains(t, joined, "TMPDIR=\"$HOME\"/'"+want+"/tmp'", kind)
			assert.Contains(t, joined, "GOTMPDIR=\"$HOME\"/'"+want+"/tmp'", kind)
		}
	})

	t.Run("the shared cache over its cap is cleaned before the command, under it is kept", func(t *testing.T) {
		t.Parallel()
		over := &fakeBenchShell{rules: []fakeBenchRule{{has: "du -sk", out: "31457281\t" + BenchCacheDefault + "\n"}}}
		res, err := RunBenchLane(context.Background(), over, "bench-a", lane, BenchLaneOptions{CacheCapGiB: 30}, []string{"go", "vet", "./..."})
		require.NoError(t, err)
		assert.True(t, res.CacheCleaned)
		assert.Contains(t, strings.Join(over.lines, "\n"), "GOCACHE='"+BenchCacheDefault+"' go clean -cache")
		under := &fakeBenchShell{rules: []fakeBenchRule{{has: "du -sk", out: "1024\t" + BenchCacheDefault + "\n"}}}
		res, err = RunBenchLane(context.Background(), under, "bench-a", lane, BenchLaneOptions{}, []string{"go", "vet", "./..."})
		require.NoError(t, err)
		assert.False(t, res.CacheCleaned)
		assert.NotContains(t, strings.Join(under.lines, "\n"), "go clean")
		// The default cap is BenchCacheCapGiBDefault when the sprint sets none: one KiB over it is cleaned.
		overDefault := &fakeBenchShell{rules: []fakeBenchRule{{has: "du -sk", out: "20971521\t" + BenchCacheDefault + "\n"}}}
		res, err = RunBenchLane(context.Background(), overDefault, "bench-a", lane, BenchLaneOptions{}, []string{"go", "vet", "./..."})
		require.NoError(t, err)
		assert.True(t, res.CacheCleaned, "the default cap is %d GiB", BenchCacheCapGiBDefault)
		work := &Table{}
		work.SetProps(map[string]string{PropBenchCacheGiB: "12"})
		assert.Equal(t, 12, (&Snapshot{Work: work}).BenchCacheCapGiB())
		assert.Equal(t, BenchCacheCapGiBDefault, (&Snapshot{}).BenchCacheCapGiB())
	})

	t.Run("a bad lane is refused before anything is sent", func(t *testing.T) {
		t.Parallel()
		for _, l := range []BenchLane{{Kind: "shell", Job: "x"}, {Kind: BenchLaneReader, Job: ".."}, {Kind: BenchLaneLander, Job: "a/b"}, {Kind: BenchLaneGate, Job: "a'b"}} {
			sh := &fakeBenchShell{}
			_, err := RunBenchLane(context.Background(), sh, "bench-a", l, BenchLaneOptions{}, []string{"true"})
			assert.Error(t, err, "%+v", l)
			assert.Empty(t, sh.lines, "%+v", l)
		}
	})

	t.Run("the next tick sweeps the directories of lanes that are gone, and only those", func(t *testing.T) {
		t.Parallel()
		sh := &fakeBenchShell{rules: []fakeBenchRule{{has: "ls -d", out: strings.Join([]string{
			BenchLaneRoot + "/worker/a~15",
			BenchLaneRoot + "/worker/b~15",
			BenchLaneRoot + "/reader/a~15",
			BenchLaneRoot + "/gate/../x",
			"/tmp/elsewhere",
		}, "\n") + "\n"}}}
		live := []BenchLane{{Kind: BenchLaneWorker, Job: "a~15"}}
		swept, err := SweepBenchLanes(context.Background(), sh, "bench-a", live)
		require.NoError(t, err)
		assert.Equal(t, []string{BenchLaneRoot + "/reader/a~15", BenchLaneRoot + "/worker/b~15"}, swept)
		all := strings.Join(sh.lines, "\n")
		assert.Contains(t, all, "rm -rf -- '"+BenchLaneRoot+"/worker/b~15'")
		assert.Contains(t, all, "rm -rf -- '"+BenchLaneRoot+"/reader/a~15'")
		assert.NotContains(t, all, "rm -rf -- '"+BenchLaneRoot+"/worker/a~15'", "a live lane's directory is kept")
		assert.NotContains(t, all, "elsewhere", "nothing outside the lanes' root is removed")
		assert.NotContains(t, all, "..", "a name that climbs is never removed")
	})

	t.Run("a bench whose /tmp is over 80% raises one judgment naming it and its largest directories", func(t *testing.T) {
		t.Parallel()
		out := "Filesystem 1024-blocks Used Available Capacity Mounted on\ntmpfs 61000000 55000000 6000000 91% /tmp\n" +
			"9437184\t/tmp/gocache\n2097152\t/tmp/job-a\n1024\t/tmp/x\n"
		tmp, err := BenchTmpFrom("bench-a", out)
		require.NoError(t, err)
		assert.Equal(t, 91, tmp.UsedPct)
		s := &Snapshot{Now: p0, Coordinator: "coordinator"}
		n, ok := BenchTmpJudgment(s, "tick", tmp)
		require.True(t, ok)
		assert.Equal(t, Judgment, n.Kind)
		assert.Equal(t, NBenchTmp, n.Type)
		assert.Contains(t, n.What, "bench bench-a:")
		assert.Contains(t, n.What, "/tmp is 91% full")
		assert.Contains(t, n.What, "/tmp/gocache")
		assert.Contains(t, n.What, "/tmp/job-a")
		s.Open = []Open{{Note: n}}
		_, ok = BenchTmpJudgment(s, "tick", tmp)
		assert.False(t, ok, "one judgment a bench while it stands")
		other := tmp
		other.Bench = "bench-b"
		_, ok = BenchTmpJudgment(s, "tick", other)
		assert.True(t, ok, "another bench is its own judgment")
		tmp.UsedPct = BenchTmpOverPct
		_, ok = BenchTmpJudgment(&Snapshot{Now: p0}, "tick", tmp)
		assert.False(t, ok, "at the threshold or under there is none")
	})
}
