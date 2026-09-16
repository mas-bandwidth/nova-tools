package pulse

// The manager tier's acceptance replays (SPEC-PULSE.md, "The manager tier"):
// manager-never-expands-policy, manager-quiet-time-makes-no-call,
// manager-dedups-on-contract-line, manager-revalidates-head-before-merge,
// manager-never-merges-draft, manager-shift-ends-with-handoff,
// manager-requeues-once-then-escalates, manager-refuses-fix-pr-without-test.
// Every gh, git, nova-bus and nova-swarm is a fixture on PATH that records its argv.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bench struct {
	queue, roots, bus, bindir, arglog string
}

// setupManager builds a fake queue, two benches and a bin directory on PATH.
func setupManager(t *testing.T) bench {
	t.Helper()
	base := t.TempDir()
	b := bench{
		queue:  filepath.Join(base, "queue"),
		roots:  filepath.Join(base, "swarm-root") + "," + filepath.Join(base, "swarm-root-space"),
		bus:    filepath.Join(base, "bus"),
		bindir: filepath.Join(base, "bin"),
		arglog: filepath.Join(base, "argv.log"),
	}
	for _, d := range []string{b.bindir, b.bus, filepath.Join(b.queue, "templates"),
		filepath.Join(base, "swarm-root"), filepath.Join(base, "swarm-root-space")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ARGLOG", b.arglog)
	t.Setenv("PATH", b.bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b.fakeBus(t, "WAIT TIMEOUT")
	b.fake(t, "nova-swarm", "exit 0")
	b.fake(t, "git", "exit 0")
	b.fake(t, "gh", "exit 0")
	return b
}

func (b bench) fake(t *testing.T, name, body string) {
	t.Helper()
	script := "#!/bin/sh\necho \"" + name + " $*\" >> \"$ARGLOG\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(b.bindir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (b bench) fakeBus(t *testing.T, out string) {
	t.Helper()
	b.fake(t, "nova-bus", "cat <<'EOT'\n"+out+"\nEOT\nexit 0")
}

// fakeGH answers pr view and pr checks with the JSON a test wants and records every call.
func (b bench) fakeGH(t *testing.T, view, checks string) {
	t.Helper()
	b.fake(t, "gh", `case "$2" in
view) cat <<'EOT'
`+view+`
EOT
;;
checks) cat <<'EOT'
`+checks+`
EOT
;;
list) echo '[]' ;;
create) echo 'https://github.com/mas-bandwidth/nova-tools/pull/7' ;;
esac
exit 0`)
}

func (b bench) policy(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(b.queue, "POLICY")
	if err := os.MkdirAll(b.queue, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (b bench) write(t *testing.T, rel, body string) string {
	t.Helper()
	p := filepath.Join(b.queue, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// job writes a finished card: the card file in launched and its RESULT.md on a bench.
func (b bench) job(t *testing.T, root, slot, label, card, result string) {
	t.Helper()
	b.write(t, filepath.Join("launched", label+".md"), card)
	dir := filepath.Join(root, slot, "jobs", label, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "RESULT.md"), []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (b bench) run(t *testing.T, policy string, hours float64) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	stamp := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	code := Manager(ManagerInput{
		Policy: policy, Queue: b.queue, Roots: b.roots, Bus: b.bus, As: "Rowan",
		Hours: hours, Max: 20, Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return stamp },
	})
	return out.String(), errs.String(), code
}

func (b bench) argv(t *testing.T) string {
	t.Helper()
	raw, _ := os.ReadFile(b.arglog)
	return string(raw)
}

func (b bench) read(t *testing.T, rel string) string {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(b.queue, rel))
	return string(raw)
}

func (b bench) pending(t *testing.T) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(b.queue, "pending"))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// manager-never-expands-policy: a key nobody approved is a refusal, and work outside the
// policy's scope is deferred rather than carded.
func TestManagerNeverExpandsPolicy(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "floor=3\nspawn-agents=true\n")
	out, errs, code := b.run(t, p, 0)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q)", code, out)
	}
	if !strings.Contains(errs, "unknown policy key") || !strings.Contains(errs, "spawn-agents") {
		t.Errorf("refusal must name the key and the remedy, got %q", errs)
	}
	if b.argv(t) != "" {
		t.Errorf("a refused policy runs nothing, got %q", b.argv(t))
	}

	src := b.write(t, "sources.tsv", strings.Join([]string{
		"issues\tmas-bandwidth/nova-tools#901\tfix\tfix the slot lock in pit-stop scope\tfix",
		"issues\tmas-bandwidth/nova-tools#902\tfix\tbuild a whole new dashboard\tfix",
	}, "\n")+"\n")
	p = b.policy(t, "floor=5\nscope-regex=pit-|slot-lock\nsources="+src+"\n")
	out, _, code = b.run(t, p, 0)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "SCOPE-DEFERRED") || !strings.Contains(out, "902") {
		t.Errorf("an out-of-scope candidate must be deferred by name, got %q", out)
	}
	if got := b.pending(t); len(got) != 1 {
		t.Errorf("pending = %v, want the one in-scope card only", got)
	}
}

// manager-quiet-time-makes-no-call: nothing on the bus, the floor met, nothing in flight --
// the wait is the only child, and no note goes out.
func TestManagerQuietTimeMakesNoCall(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "wait-timeout=3m\nfloor=0\n")
	out, _, code := b.run(t, p, 0)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimSpace(b.argv(t)), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "nova-bus wait") {
		t.Fatalf("quiet time ran %v; the wait is the only call", lines)
	}
	for _, bad := range []string{"gh ", "git ", "nova-swarm ", "nova-bus send", "nova-bus receipt"} {
		if strings.Contains(b.argv(t), bad) {
			t.Errorf("quiet time called %q", bad)
		}
	}
	if !strings.Contains(out, "quiet=true") {
		t.Errorf("the cycle line must say the cycle was quiet, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(b.queue, "ESCALATE")); err == nil {
		t.Errorf("quiet time wrote an escalation")
	}
}

// manager-dedups-on-contract-line: the same contract sentence under two ids is one card.
func TestManagerDedupsOnContractLine(t *testing.T) {
	b := setupManager(t)
	src := b.write(t, "sources.tsv", strings.Join([]string{
		"issues\tmas-bandwidth/nova-tools#911\tfix\tpit-stop: the slot lock releases twice\tfix",
		"bus\tbo-aaaaaaaaaaaa\tfix\tpit-stop: the slot lock releases twice\tfix",
	}, "\n")+"\n")
	p := b.policy(t, "floor=9\nsources="+src+"\n")
	if _, _, code := b.run(t, p, 0); code != 0 {
		t.Fatalf("exit != 0")
	}
	if got := b.pending(t); len(got) != 1 {
		t.Fatalf("pending = %v, want one card for one contract sentence", got)
	}
	// A second shift re-reads the same source and must not card it again.
	if _, _, code := b.run(t, p, 0); code != 0 {
		t.Fatalf("exit != 0")
	}
	if got := b.pending(t); len(got) != 1 {
		t.Fatalf("pending after the second shift = %v, want one", got)
	}
}

// manager-revalidates-head-before-merge: the head is re-read before any side effect, and a
// head that moved since the read is never merged.
func TestManagerRevalidatesHeadBeforeMerge(t *testing.T) {
	b := setupManager(t)
	b.fakeGH(t, `{"isDraft":false,"state":"OPEN","headRefOid":"bbbbbbbbbbbbbbbb"}`,
		`[{"name":"build","state":"SUCCESS"}]`)
	b.write(t, "APPROVED", "mas-bandwidth/nova-tools 577 aaaaaaaaaaaaaaaa\n")
	p := b.policy(t, "floor=0\n")
	out, _, code := b.run(t, p, 0)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	argv := b.argv(t)
	if !strings.Contains(argv, "gh pr view 577") {
		t.Errorf("the head was not revalidated: %q", argv)
	}
	if strings.Contains(argv, "gh pr merge") {
		t.Errorf("merged on a stale read: %q", argv)
	}
	if !strings.Contains(out, "STALE-READ") || !strings.Contains(b.read(t, "ESCALATE"), "STALE-READ") {
		t.Errorf("a stale read must escalate one line, got %q / %q", out, b.read(t, "ESCALATE"))
	}
	if b.read(t, "APPROVED") != "" {
		t.Errorf("a stale approval must not stay on file")
	}
}

// manager-never-merges-draft: approved, revalidated and green is still not a merge while the
// PR is a draft.
func TestManagerNeverMergesDraft(t *testing.T) {
	b := setupManager(t)
	b.fakeGH(t, `{"isDraft":true,"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaa"}`,
		`[{"name":"build","state":"SUCCESS"}]`)
	b.write(t, "APPROVED", "mas-bandwidth/nova-tools 577 aaaaaaaaaaaaaaaa\n")
	p := b.policy(t, "floor=0\n")
	out, _, _ := b.run(t, p, 0)
	if strings.Contains(b.argv(t), "gh pr merge") {
		t.Fatalf("a draft was merged: %q", b.argv(t))
	}
	if !strings.Contains(out, "DRAFT-READY") {
		t.Errorf("a draft that is otherwise ready must escalate DRAFT-READY, got %q", out)
	}
}

// manager-shift-ends-with-handoff: the shift ends by itself with the handoff line, and the
// same line is on the record.
func TestManagerShiftEndsWithHandoff(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "floor=0\n")
	out, _, code := b.run(t, p, 0)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "SHIFT END cycles=") || !strings.Contains(last, "decisions=") || !strings.Contains(last, "escalations=") {
		t.Fatalf("last line = %q, want SHIFT END cycles= decisions= escalations=", last)
	}
	log := b.read(t, "MANAGER.log")
	if !strings.Contains(log, "MANAGER cycle=1") {
		t.Errorf("MANAGER.log wants one line per cycle, got %q", log)
	}
	if !strings.Contains(log, last) {
		t.Errorf("the handoff line is not on the record: %q", log)
	}
}

// manager-requeues-once-then-escalates: an abstain is requeued once under a new number on the
// other bench; the second abstain is one escalation, never a third send of the same bytes.
func TestManagerRequeuesOnceThenEscalates(t *testing.T) {
	b := setupManager(t)
	rootA := strings.Split(b.roots, ",")[0]
	card := "RESULT: CARD-1 fix the slot lock\nSTEP 1. clone\n"
	b.job(t, rootA, "1", "card-1", card, "ABSTAIN reason=wall the harness refused a read outside the slot\n")
	b.write(t, "NEXT", "7\n")
	p := b.policy(t, "floor=0\nmax-attempts=1\n")
	out, _, code := b.run(t, p, 0)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "MANAGER REQUEUE") || !strings.Contains(out, "reason=wall") {
		t.Fatalf("the first abstain must requeue by reason, got %q", out)
	}
	if !strings.Contains(out, "new=card-7.md") || !strings.Contains(out, "bench=swarm-root-space") {
		t.Fatalf("the requeue wants a new number on the other bench, got %q", out)
	}
	if got := b.pending(t); len(got) != 1 || got[0] != "card-7.md" {
		t.Fatalf("pending = %v, want the requeued card only", got)
	}

	// The requeued card abstains again on the other bench: one escalation, no second requeue.
	rootB := strings.Split(b.roots, ",")[1]
	body, err := os.ReadFile(filepath.Join(b.queue, "pending", "card-7.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(b.queue, "pending", "card-7.md")); err != nil {
		t.Fatal(err)
	}
	b.job(t, rootB, "2", "card-7", string(body), "ABSTAIN reason=wall again\n")
	out, _, _ = b.run(t, p, 0)
	if !strings.Contains(out, "ESCALATE") || !strings.Contains(out, "ABSTAIN") {
		t.Fatalf("the second abstain must escalate, got %q", out)
	}
	if strings.Contains(out, "MANAGER REQUEUE") {
		t.Errorf("the same card was requeued twice: %q", out)
	}
	if got := b.pending(t); len(got) != 0 {
		t.Errorf("pending = %v, want nothing requeued", got)
	}
	if !strings.Contains(b.read(t, "ESCALATE"), "ESCALATE 2026-09-15T12:00:00Z ABSTAIN card-7") {
		t.Errorf("the escalation line is not in the shape the spec names: %q", b.read(t, "ESCALATE"))
	}
}

// manager-refuses-fix-pr-without-test: a fix with no red: line and no test in its diff is
// refused at the harvest, before the push.
func TestManagerRefusesFixPRWithoutTest(t *testing.T) {
	b := setupManager(t)
	b.fake(t, "git", `case "$1" in
merge-base) echo aaaaaaaaaaaa ;;
diff) echo internal/pulse/manager.go ;;
esac
exit 0`)
	rootA := strings.Split(b.roots, ",")[0]
	result := "RESULT: CARD-1 fix the slot lock\nBRANCH: rowan/fix-slot-lock\nREPO: mas-bandwidth/nova-tools\n"
	b.job(t, rootA, "1", "card-1", "RESULT: CARD-1 fix the slot lock\n", result)
	p := b.policy(t, "floor=0\n")
	out, _, _ := b.run(t, p, 0)
	if !strings.Contains(out, "MANAGER REFUSED") || !strings.Contains(out, "reproducing test") {
		t.Fatalf("a fix without its test must be refused with the remedy, got %q", out)
	}
	if strings.Contains(b.argv(t), "git push") || strings.Contains(b.argv(t), "gh pr create") {
		t.Fatalf("a refused fix was pushed anyway: %q", b.argv(t))
	}

	// The same card with a test file in its diff is admitted and gets its read card.
	b2 := setupManager(t)
	b2.fake(t, "git", `case "$1" in
merge-base) echo aaaaaaaaaaaa ;;
diff) echo internal/pulse/manager_test.go ;;
esac
exit 0`)
	b2.fakeGH(t, "{}", "[]")
	rootA2 := strings.Split(b2.roots, ",")[0]
	b2.job(t, rootA2, "1", "card-1", "RESULT: CARD-1 fix the slot lock\n", result)
	p2 := b2.policy(t, "floor=0\n")
	out2, _, _ := b2.run(t, p2, 0)
	if !strings.Contains(out2, "MANAGER PR repo=mas-bandwidth/nova-tools pr=7") {
		t.Fatalf("a fix with its test must open a PR, got %q", out2)
	}
	if !strings.Contains(b2.argv(t), "git push") {
		t.Errorf("the branch was not pushed by explicit refspec: %q", b2.argv(t))
	}
	if !strings.Contains(b2.argv(t), "+rowan/fix-slot-lock:rowan/fix-slot-lock") {
		t.Errorf("the push was not an explicit refspec: %q", b2.argv(t))
	}
	if got := b2.pending(t); len(got) != 1 {
		t.Errorf("a PR wants its read card cut: pending = %v", got)
	}
}

// manager-hands-merge-to-lane: an approved, revalidated, green PR is handed to nova-merge's
// lane with `nova-merge add`, never merged by `gh pr merge` -- the lane is the merge queue.
func TestManagerHandsMergeToLane(t *testing.T) {
	b := setupManager(t)
	b.fake(t, "nova-merge", "exit 0")
	b.fakeGH(t, `{"isDraft":false,"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaa"}`,
		`[{"name":"build","state":"SUCCESS"}]`)
	b.write(t, "APPROVED", "mas-bandwidth/nova-tools 577 aaaaaaaaaaaaaaaa\n")
	lane := t.TempDir()
	p := b.policy(t, "floor=0\nlane="+lane+"\n")
	out, _, code := b.run(t, p, 0)
	if code != 0 {
		t.Fatalf("exit = %d (stdout=%q)", code, out)
	}
	argv := b.argv(t)
	if !strings.Contains(argv, "nova-merge add --lane "+lane+" --pr 577") {
		t.Errorf("the approved PR was not handed to the lane: %q", argv)
	}
	if strings.Contains(argv, "gh pr merge") {
		t.Errorf("the lane, not gh, is the merge queue: %q", argv)
	}
	if !strings.Contains(out, "MANAGER LANE ref=mas-bandwidth/nova-tools#577") {
		t.Errorf("the handoff must name the lane: %q", out)
	}
}

// A policy with every key parses to the values it names; the defaults stand for what it omits.
func TestManagerPolicyKeys(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "# the approved policy\nwait-timeout=90s\nfloor=12\nscope-regex=pit-\nsources=a.tsv,b.tsv\nknown-flakes=TestJoinChildDeath,TestWaitWithoutAdvance\nmax-attempts=2\n")
	pol, err := readPolicy(p)
	if err != nil {
		t.Fatal(err)
	}
	if pol.WaitTimeout != "90s" || pol.Floor != 12 || pol.MaxAttempts != 2 ||
		len(pol.Sources) != 2 || len(pol.KnownFlakes) != 2 || pol.Scope == nil {
		t.Fatalf("policy = %+v", pol)
	}
	if pol2, err := readPolicy(b.policy(t, "floor=1\n")); err != nil || pol2.WaitTimeout != "3m" || pol2.MaxAttempts != 1 {
		t.Fatalf("defaults = %+v, err=%v", pol2, err)
	}
}
