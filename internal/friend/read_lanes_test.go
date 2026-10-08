package friend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friend's reader row (the owner, 2026-10-05: "Make this part of the friend's lane
// daemon", and "Read slots are different from worker cards"): the daemon beats
// reader-<friend>, begins asked reads up to her read slots, runs each as a one-shot of her
// harness and records the verdict, or returns the read with its reason and usage.

// readSprint is a sprint server that answers the reader's queue and records every verb.
type readSprint struct {
	mu          sync.Mutex
	queue       string
	state       map[string]string // card -> begun | done: what the server's columns say after each verb
	argvs       [][]string
	failVerdict string // before or after the canonical write, once
}

func (s *readSprint) ask(_ context.Context, argv []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.argvs = append(s.argvs, argv)
	switch argv[0] {
	case "card":
		var reads []map[string]any
		for _, a := range s.argvs {
			if len(a) > 4 && a[0] == "read" && (a[3] == "--ok" || a[3] == "--broken") && s.state[a[4]] == "done" {
				reads = append(reads, map[string]any{"ID": a[4], "Col": strings.TrimPrefix(a[3], "--"), "Fields": map[string]string{"verdict": strings.TrimPrefix(a[3], "--"), "head": "aaa", "finding": flagValue(a, "--finding"), "usage": flagValue(a, "--usage")}})
			}
		}
		b, _ := json.Marshal(map[string]any{"read_cards": reads})
		return string(b), nil
	case "queue":
		var q struct {
			Epoch string
			Cards []map[string]any
		}
		_ = json.Unmarshal([]byte(s.queue), &q)
		var cards []map[string]any
		for _, c := range q.Cards {
			switch s.state[c["id"].(string)] {
			case "done":
				continue
			case "begun":
				c["col"] = "reading"
			}
			cards = append(cards, c)
		}
		raw, _ := json.Marshal(map[string]any{"epoch": q.Epoch, "cards": cards})
		return string(raw), nil
	case "read":
		if argv[3] == "--ok" || argv[3] == "--broken" {
			if s.failVerdict == "before" {
				s.failVerdict = ""
				return "", errors.New("rpc unavailable before commit")
			}
		}
		if s.state == nil {
			s.state = map[string]string{}
		}
		id, _, _ := strings.Cut(argv[4], "@")
		s.state[id] = map[string]string{"--begin": "begun"}[argv[3]]
		if argv[3] != "--begin" {
			s.state[id] = "done" // a verdict, or a return the fake does not ask again
		}
		if argv[3] != "--begin" && s.failVerdict == "after" {
			s.failVerdict = ""
			return "", errors.New("response lost after commit")
		}
		return "READ OK\n", nil
	}
	return "", nil
}

func TestReadVerdictRetriesAfterRPCFailureWithoutRunningAgain(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"before", "after"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				dir := cardDirFixture(t, nil, nil, nil)
				h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
				sp := &readSprint{queue: askedQueue, failVerdict: failure}
				r := readRig(t, h, sp, 1)
				r.run(t, 30)
				assert.Len(t, sp.verbs("--begin"), 2, "a and b begin once each")
				assert.Len(t, h.prompts, 2, "a completed read never runs again")
				oks := sp.verbs("--ok")
				if failure == "before" {
					require.Len(t, oks, 2)
					assert.Equal(t, oks[0], oks[1], "finding, epoch and usage survive the failed RPC")
				} else {
					require.Len(t, oks, 1, "a lost response does not duplicate the verdict")
					assert.NoFileExists(t, readSettlementPath(filepath.Join(dir, "reads", "a.w1")), "canonical card confirms the lost response")
				}
			})
		})
	}
}

func TestReadRecoversCompletedResultAfterDaemonCrash(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		readDir := filepath.Join(dir, "reads", "a.w1")
		require.NoError(t, os.MkdirAll(readDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(readDir, "RESULT.md"), []byte(okResult), 0o644))
		require.NoError(t, saveReadActive(readDir, readActive{Read: AskedRead{ID: "a.w1", Epoch: "15", Col: "reading", Packet: ReadPacket{Head: "aaa"}}, PID: 2147483647, Identity: "exited process"}))
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}}
		sp := &readSprint{queue: askedQueue, state: map[string]string{"a.w1": "begun"}}
		r := readRig(t, h, sp, 1)
		r.run(t, 20)
		for _, a := range sp.verbs("--begin") {
			assert.NotEqual(t, "a.w1", a[4], "the reading result is settled without another begin")
		}
		for _, p := range h.prompts {
			assert.NotContains(t, p, "/a.w1/READ.md", "the model is not run again")
		}
		require.Len(t, sp.verbs("--ok"), 1)
		assert.Equal(t, "fine no findings", flagValue(sp.verbs("--ok")[0], "--finding"))
	})
}

func TestReadReplaysDurableSettlementAfterDaemonCrash(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		readDir := filepath.Join(dir, "reads", "a.w1")
		p := readSettlement{Read: AskedRead{ID: "a.w1", Epoch: "15", Col: "reading", Packet: ReadPacket{Head: "aaa"}}, Verdict: "ok", Finding: "exact finding", Usage: "model=m-pro wall=23s harness=fake account=bob"}
		require.NoError(t, saveReadSettlement(readDir, p))
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}}
		sp := &readSprint{queue: askedQueue, state: map[string]string{"a.w1": "begun"}}
		r := readRig(t, h, sp, 1)
		r.run(t, 20)
		for _, prompt := range h.prompts {
			assert.NotContains(t, prompt, "/a.w1/READ.md", "a completed read is never rerun")
		}
		oks := sp.verbs("--ok")
		require.Len(t, oks, 1)
		assert.Equal(t, "exact finding", flagValue(oks[0], "--finding"))
		assert.Equal(t, p.Usage, flagValue(oks[0], "--usage"))
		assert.NoFileExists(t, readSettlementPath(readDir))
	})
}

func TestRestartCannotReturnReadWithoutChildExitProof(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}}
		sp := &readSprint{queue: askedQueue, state: map[string]string{"a.w1": "begun"}}
		r := readRig(t, h, sp, 1)
		r.run(t, 20)
		assert.Empty(t, sp.verbs("--return"), "missing active identity cannot prove an orphaned child exited")
		assert.Empty(t, sp.verbs("--ok"))
	})
}

func TestMissingKeyHoldsReadLaunch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}}
		sp := &readSprint{queue: askedQueue}
		r := readRig(t, h, sp, 1)
		r.d.NeedsEnv = []string{"MISSING_READ_KEY"}
		r.d.Getenv = func(string) string { return "" }
		r.run(t, 20)
		assert.Empty(t, sp.verbs("--begin"))
		assert.Empty(t, h.prompts)
	})
}

func (s *readSprint) verbs(flag string) [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, a := range s.argvs {
		if a[0] == "read" && len(a) > 3 && a[3] == flag {
			out = append(out, a)
		}
	}
	return out
}

func flagValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

const askedQueue = `{"epoch":"15","cards":[
 {"id":"a.w1","col":"asked","packet":{"tier":"pro","head":"aaa","work_branch":"sprint/a","attempt":1,"brief":"REPO: o/r\nBASE: main\n","report":"Verdict: LAND"}},
 {"id":"b.w1","col":"asked","packet":{"tier":"flash","head":"bbb","work_branch":"sprint/b","attempt":1,"brief":"REPO: o/r\nBASE: main\n","report":"Verdict: LAND"}},
 {"id":"z.w1","col":"begun","packet":{"tier":"pro","head":"zzz","work_branch":"sprint/z","attempt":1,"brief":"","report":""}}]}`

var readDirOfPrompt = regexp.MustCompile(`Do the read in (\S+)/READ\.md exactly`)

// readHarness is a lanes harness that also runs reads: a read of a card in verdicts writes
// that RESULT.md, any other writes none; it records the model and the prompt, and waits on
// block while it is set.
type readHarness struct {
	*lanesHarness
	rmu      sync.Mutex
	models   []string
	prompts  []string
	verdicts map[string]string // card dir name -> RESULT.md text
	rblock   chan struct{}
	err      error
}

func (h *readHarness) RunRead(ctx context.Context, model, prompt string) (LaneTurn, error) {
	dir := readDirOfPrompt.FindStringSubmatch(prompt)[1]
	h.rmu.Lock()
	h.models = append(h.models, model)
	h.prompts = append(h.prompts, prompt)
	block, text, err := h.rblock, h.verdicts[filepath.Base(dir)], h.err
	h.rmu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	if err != nil {
		return LaneTurn{Exit: 1}, err
	}
	if text != "" {
		if err := os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte(text), 0o644); err != nil {
			return LaneTurn{}, err
		}
	}
	return LaneTurn{}, nil
}

func readRig(t *testing.T, h *readHarness, sp *readSprint, slots int) *rig {
	r, _ := laneRig(t, h.lanesHarness, 1)
	r.d.Deliver = h
	r.d.Sprint = sp.ask
	r.d.ReadSlots = func() int { return slots }
	r.d.ReadModel = func(tier string) string { return map[string]string{"pro": "m-pro", "flash": "m-flash"}[tier] }
	return r
}

const okResult = "head: aaa\nbranch: sprint/a\nverdict: ok\ngate: go vet\nreport: fine\n## Body\nno findings\n"

// An asked read is begun, run as a one-shot of the friend's harness with the model of its
// tier and the read prompt, and recorded ok with its usage; a run that writes no RESULT.md
// is returned with its reason and usage; a read already begun is not begun again.
func TestALaneDaemonReadsAnAskedReadAndRecordsTheVerdict(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		r := readRig(t, h, sp, 2)
		r.run(t, 30)

		var begun []string
		for _, a := range sp.verbs("--begin") {
			assert.Equal(t, []string{"read", "--as", "reader-bob", "--begin", a[4], "--epoch", "15"}, a)
			begun = append(begun, a[4])
		}
		assert.ElementsMatch(t, []string{"a.w1", "b.w1"}, begun, "each asked read once, the begun one never")
		oks := sp.verbs("--ok")
		require.Len(t, oks, 1)
		assert.Equal(t, "a.w1", oks[0][4])
		assert.Equal(t, "15", flagValue(oks[0], "--epoch"))
		assert.Contains(t, flagValue(oks[0], "--finding"), "fine")
		assert.Contains(t, flagValue(oks[0], "--finding"), "no findings")
		usage := flagValue(oks[0], "--usage")
		assert.Contains(t, usage, "model=m-pro")
		assert.Contains(t, usage, "wall=")
		assert.Contains(t, usage, "account=bob")
		rets := sp.verbs("--return")
		require.Len(t, rets, 1)
		assert.Equal(t, "b.w1", rets[0][4])
		assert.Contains(t, flagValue(rets[0], "--reason"), "no verdict")
		assert.Contains(t, flagValue(rets[0], "--usage"), "model=m-flash")
		assert.Empty(t, sp.verbs("--broken"))
		assert.ElementsMatch(t, []string{"m-pro", "m-flash"}, h.models)
		prompt := strings.Join(h.prompts, "\n")
		assert.Contains(t, prompt, filepath.Join(dir, "reads", "a.w1"))
		raw, err := os.ReadFile(filepath.Join(dir, "reads", "a.w1", "READ.md"))
		require.NoError(t, err)
		assert.Contains(t, string(raw), "/o/r.git")
		assert.Contains(t, string(raw), "BENCH RULE")
		assert.Contains(t, string(raw), "merge-base")
		assert.FileExists(t, filepath.Join(dir, "reads", "a.w1", "BRIEF.md"))
		assert.FileExists(t, filepath.Join(dir, "reads", "a.w1", "WORKER-REPORT.txt"))
	})
}

// Read slots are their own number: with one read slot and the read held, a second asked read
// waits, and a dealt card is started by its card lane at once, never waiting for a read.
func TestAReadSlotIsNeverACardLaneAndNeverWaitsForOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}, finish: map[string]bool{"c1": true}},
			verdicts: map[string]string{"a.w1": okResult, "b.w1": strings.ReplaceAll(okResult, "aaa", "bbb")}, rblock: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		r := readRig(t, h, sp, 1)
		var turns []string
		var begun int
		r.at[20] = func() {
			turns, _, _ = h.got()
			begun = len(sp.verbs("--begin"))
			close(h.rblock)
		}
		r.run(t, 40)
		assert.Equal(t, []string{"ses_1: c1"}, turns, "the card lane worked its card while the read held the read slot")
		assert.Equal(t, 1, begun, "one read slot begins one read; the second waits")
		assert.Len(t, sp.verbs("--begin"), 2, "and begins when the slot frees")
		assert.Len(t, sp.verbs("--ok"), 2)
	})
}

// A rate limit or out of funds met by a read returns it with the provider's reason and
// pauses the lanes, as it does a card's turn.
func TestAReadThatMeetsAUsageLimitIsReturnedAndTheLanesBackOff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, err: OutOfFunds{Reason: "weekly limit reached"}}
		sp := &readSprint{queue: askedQueue}
		r := readRig(t, h, sp, 1)
		r.run(t, 20)
		rets := sp.verbs("--return")
		require.NotEmpty(t, rets)
		assert.Contains(t, flagValue(rets[0], "--reason"), "weekly limit reached")
		assert.Len(t, sp.verbs("--begin"), 1, "held out of funds, no second read begins")
		assert.Contains(t, strings.Join(r.records, "\n"), "out of funds")
	})
}

func TestParseReadSlotsAndVerdict(t *testing.T) {
	t.Parallel()
	n, ok := ParseReadSlots("FRIEND-BEAT OK row_mode=one-shot row_width=2 row_read_slots=3")
	assert.True(t, ok)
	assert.Equal(t, 3, n)
	_, ok = ParseReadSlots("FRIEND-BEAT OK row_mode=one-shot row_width=2")
	assert.False(t, ok, "a beat that says none leaves the default")
	v, f := ReadVerdict(okResult)
	assert.Equal(t, "ok", v)
	assert.Equal(t, "fine no findings", f)
	v, _ = ReadVerdict("verdict: none\nreport: no head\n")
	assert.Empty(t, v, "none is no verdict")
	_, err := ParseReadQueue(`{"cards":[]}`)
	assert.Error(t, err, "a queue with no epoch is not trusted")
}
