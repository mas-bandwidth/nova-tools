package release

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAnsible answers each play with the output the real one prints for it.
type fakeAnsible struct {
	runs    [][]string
	answers []string
	fail    error
}

func (a *fakeAnsible) Play(_ context.Context, argv []string) (string, error) {
	a.runs = append(a.runs, argv)
	answer := a.answers[len(a.runs)-1]
	return answer, a.fail
}

// playOutput is the tools play's receipts and recap, the way ansible prints them.
func playOutput(state string, failed string, hosts ...string) string {
	var b strings.Builder
	for _, h := range hosts {
		tail := state
		if state == "INSTALLED" {
			tail = "INSTALLED RELEASE INSTALLED version=v1.1.0-dev.c2 tools=1 skipped=17 retired=0 bin=/home/nova/.local/bin platform=linux-amd64 pruned=1 prune-failed=0"
		}
		fmt.Fprintf(&b, "ok: [%s] => {\n    \"msg\": \"TOOLS host=%s platform=linux-amd64 version=v1.1.0-dev.c2 was=v1.1.0-dev.c1 removed=0 %s\"\n}\n", h, h, tail)
	}
	b.WriteString("ok: [localhost] => {\n    \"msg\": [\n        \"RELEASE BUILD DOGFOOD REPORTED open=4 reason=the\\\\x20member\\\\x20fix\",\n        \"RELEASE BUILD INCREMENTAL version=v1.1.0-dev.c2 platform=linux-amd64 base=v1.1.0-dev.c1 changed=3 rebuilt=nova-swarm reused=17\"\n    ]\n}\n")
	b.WriteString("PLAY RECAP *********\n")
	for _, h := range append(hosts, "localhost") {
		f := "0"
		if h == failed {
			f = "1"
		}
		fmt.Fprintf(&b, "%-26s : ok=12   changed=4    unreachable=0    failed=%s    skipped=2    rescued=0    ignored=0\n", h, f)
	}
	return b.String()
}

// cycleRig is a checkout with a play, an artifact root and a receipts
// directory, and a clock that moves a minute a reading.
func cycleRig(t *testing.T, plays ...string) (args []string, deps Deps, play *fakeAnsible) {
	t.Helper()
	source := sourceTree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(source, "fleet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "fleet", "tools.yml"), []byte("- hosts: all\n"), 0o644))
	now := time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC)
	play = &fakeAnsible{answers: plays}
	deps = Deps{Ansible: play, Now: func() time.Time { now = now.Add(time.Minute); return now }}
	args = []string{"cycle", "--version", "v1.1.0-dev.c2", "--source", source, "--out", t.TempDir(),
		"--inventory", "/fleet/nova-inventory", "--benches", "batman,vision", "--reason", "the member fix",
		"--receipts", t.TempDir(), "--ansible", "/usr/bin/ansible-playbook"}
	return args, deps, play
}

func TestCycleDryRunChecksAndInstallsNothing(t *testing.T) {
	t.Parallel()
	args, deps, play := cycleRig(t, playOutput("WOULD-INSTALL", "", "batman", "vision"))
	var o, e bytes.Buffer
	code := Run("nova-update", append(args, "--dry-run"), &o, &e, deps)
	require.Equal(t, 0, code, e.String())

	require.Len(t, play.runs, 1, "a dry run is the check alone")
	argv := play.runs[0]
	assert.Equal(t, "--check", argv[len(argv)-1])
	assert.Equal(t, "batman,vision,localhost", argv[slices.Index(argv, "--limit")+1])
	assert.Contains(t, argv, "nova_version=v1.1.0-dev.c2")
	var build map[string][]string
	for _, a := range argv {
		if strings.HasPrefix(a, "{") {
			require.NoError(t, json.Unmarshal([]byte(a), &build))
		}
	}
	assert.Equal(t, []string{"--incremental", "--gate", "report", "--reason", "the member fix"}, build["nova_release_build_args"])
	assert.Contains(t, o.String(), "CYCLE WOULD host=batman platform=linux-amd64 version=v1.1.0-dev.c2 was=v1.1.0-dev.c1 state=WOULD-INSTALL\n")
	assert.Contains(t, o.String(), "CYCLE WOULD host=vision ")
	assert.Contains(t, o.String(), "CYCLE DRY-RUN version=v1.1.0-dev.c2 benches=2 check=1m0s\n")
	out := args[slices.Index(args, "--out")+1]
	_, err := os.Stat(filepath.Join(out, "v1.1.0-dev.c2", "cycle-check.log"))
	assert.NoError(t, err, "the check's output is kept")
}

func TestCycleChecksThenAppliesAndSaysWhatEachBenchRuns(t *testing.T) {
	t.Parallel()
	args, deps, play := cycleRig(t, playOutput("WOULD-INSTALL", "", "batman", "vision"), playOutput("INSTALLED", "", "batman", "vision"))
	var o, e bytes.Buffer
	code := Run("nova-update", args, &o, &e, deps)
	require.Equal(t, 0, code, e.String())
	require.Len(t, play.runs, 2)
	assert.NotContains(t, play.runs[1], "--check")
	assert.Contains(t, o.String(), "RELEASE BUILD DOGFOOD REPORTED open=4 reason=the\\x20member\\x20fix\n", "the play's JSON quoting is undone")
	assert.Contains(t, o.String(), "RELEASE BUILD INCREMENTAL version=v1.1.0-dev.c2 platform=linux-amd64 base=v1.1.0-dev.c1 changed=3 rebuilt=nova-swarm reused=17\n")
	assert.Contains(t, o.String(), "CYCLE BENCH host=batman platform=linux-amd64 version=v1.1.0-dev.c2 was=v1.1.0-dev.c1 state=INSTALLED installed=1 skipped=17\n")
	assert.Contains(t, o.String(), "CYCLE OK version=v1.1.0-dev.c2 benches=2 changed=2 check=1m0s apply=1m0s total=2m0s ")
}

func TestCycleStopsOnAFailedBench(t *testing.T) {
	t.Parallel()
	// The check fails on vision: nothing is applied.
	args, deps, play := cycleRig(t, playOutput("WOULD-INSTALL", "vision", "batman", "vision")+"fatal: [vision]: UNREACHABLE! => {}\n")
	play.fail = fmt.Errorf("exit status 2")
	var o, e bytes.Buffer
	code := Run("nova-update", args, &o, &e, deps)
	assert.Equal(t, 1, code)
	assert.Len(t, play.runs, 1)
	assert.Contains(t, e.String(), "fatal: [vision]: UNREACHABLE!")
	assert.Contains(t, e.String(), "CYCLE FAIL step=check version=v1.1.0-dev.c2 reason=exit\\x20status\\x202 ")

	// A bench the apply has no receipt for is a failure too.
	args, deps, _ = cycleRig(t, playOutput("WOULD-INSTALL", "", "batman", "vision"), playOutput("INSTALLED", "", "batman"))
	o.Reset()
	e.Reset()
	code = Run("nova-update", args, &o, &e, deps)
	assert.Equal(t, 1, code)
	assert.Contains(t, e.String(), "CYCLE FAIL step=apply ")
	assert.Contains(t, o.String(), "CYCLE BENCH host=vision platform=- version=- was=- state=- ")
}

func TestCycleRefusesBeforeAnyPlay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ flag, value, want string }{
		{"--benches", "batman,all:!vision", "not a machine name"},
		{"--benches", " , ", "--benches is empty"},
		{"--source", "/nonexistent", "has no fleet/tools.yml"},
	} {
		args, deps, play := cycleRig(t)
		args[slices.Index(args, tc.flag)+1] = tc.value
		var o, e bytes.Buffer
		assert.Equal(t, 2, Run("nova-update", args, &o, &e, deps), tc.flag)
		assert.Contains(t, e.String(), tc.want)
		assert.Empty(t, play.runs)
	}
}
