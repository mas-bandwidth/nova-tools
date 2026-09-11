package board

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The issue backend, against a RECORDED FIXTURE and never the network (CONTRIBUTING: a
// test that touches the network wants a reason, and this one has none). The fixture is two
// concatenated JSON arrays, which is what `gh api --paginate` hands back, so the
// pagination is exercised rather than assumed.
func TestTheIssueBackendReadsTheWholeThreadAcrossPages(t *testing.T) {
	i, err := NewIssue("mas-bandwidth/schema#876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := os.ReadFile("testdata/issue-876.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	var appended []string
	i.run = func(ctx context.Context, stdin string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "api" {
			return string(recorded) + strings.Join(appended, "\n"), nil
		}
		if stdin == "" {
			t.Error("an event was appended with an empty stdin; the body goes in on stdin, never on the command line")
		}
		for _, a := range args {
			if strings.Contains(a, "card ") || strings.Contains(a, "Windows") {
				t.Errorf("an event's text reached gh's command line: %q", a)
			}
		}
		appended = append(appended, fmt.Sprintf("[{\"body\": %q}]", strings.TrimSpace(stdin)))
		return "", nil
	}
	log, err := i.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Lines) != 5 {
		t.Fatalf("the read is the WHOLE thread: got %d lines across two pages, want 5", len(log.Lines))
	}
	if !strings.Contains(calls[0], "--paginate") {
		t.Errorf("the read is not paginated: %q", calls[0])
	}
	b := Derive(log, mustTime(t, "2026-09-10T11:00:00Z"), 10*time.Minute)
	if len(b.Cards) != 2 {
		t.Fatalf("the fixture folds to %d cards, want 2", len(b.Cards))
	}
	if b.Unparsed != 1 {
		t.Errorf("the hand-written comment is not counted as unparsed: %d", b.Unparsed)
	}
	if b.Cards[0].Owner != "bo" {
		t.Errorf("owner = %q, want the latest take's as=", b.Cards[0].Owner)
	}
	if b.Cards[1].State != "CLOSED" || !b.Cards[1].Probed {
		t.Errorf("the probed row is not closed: %+v", b.Cards[1])
	}
	if c := b.Counts(); c.Owed != 0 || c.Open != 1 {
		t.Errorf("counts = %+v, want the probed row not owed", c)
	}

	// A CACHE MAY AVOID RE-READING; IT MAY NEVER TRUNCATE, AND THIS TOOL'S OWN APPEND
	// INVALIDATES IT. Two reviewers filing nine seconds apart both reading a board from
	// before either wrote is the 25-duplicate failure, mechanized.
	before := len(calls)
	if _, err := i.Events(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != before {
		t.Error("a second read inside one run went back to the backend; the cache exists to avoid that")
	}
	if err := i.Append("card 11111111111111111111111111111111 as=zz at=2026-09-10T11:00:00Z override=false hash=cccccccccccc owner=zz by=2026-09-12T09:00:00Z default=d: a third thing"); err != nil {
		t.Fatal(err)
	}
	log, err = i.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Lines) != 6 {
		t.Fatalf("the read after this tool's own append returned %d lines; the cache was not invalidated", len(log.Lines))
	}
}

// The three parts of --issue become gh's own argv, so a value beginning with a dash is an
// OPTION to gh and not a repository. The cost of being narrow is a refusal a person reads.
func TestAnIssueSpecIsCheckedRatherThanPasted(t *testing.T) {
	for _, bad := range []string{
		"mas-bandwidth/schema", "mas-bandwidth#876", "--repo=evil/x#1", "mas-bandwidth/-x#1",
		"mas-bandwidth/schema#0", "mas-bandwidth/schema#eight", "mas-bandwidth/sch ema#8",
		"mas-bandwidth/$(touch x)#1",
	} {
		if _, _, _, err := ParseIssue(bad); err == nil {
			t.Errorf("%q was accepted as an issue", bad)
		} else if !strings.Contains(err.Error(), "--issue wants") {
			t.Errorf("the refusal for %q does not say what --issue wants: %v", bad, err)
		}
	}
	owner, repo, n, err := ParseIssue("mas-bandwidth/schema#876")
	if err != nil || owner != "mas-bandwidth" || repo != "schema" || n != 876 {
		t.Errorf("ParseIssue = %q %q %d %v", owner, repo, n, err)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	when, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return when.UTC()
}

// CREATION IS EXCLUSIVE UNDER --issue AS WELL, and the re-read that makes it so is never
// answered from this run's own cache. Spec, Creation is exclusive against hand-made files:
// "under `--issue` the board is re-read immediately before the append and the id looked
// for. An id that already exists — which with a random id means a hand-made file, a copied
// one, or a broken random source — is `ADD REFUSED: id <id> exists; nothing written` at
// exit 1". The forge append is the ONLY serial point under this backend, so this is its
// half of the O_EXCL the directory backend gets from the filesystem: a run that read the
// thread, paused, and appended what it drew would post a second card under an id another
// line had already used, and two filings would fold into one card with nothing said.
func TestACardAppendRereadsTheThreadAndRefusesAnExistingId(t *testing.T) {
	i, err := NewIssue("mas-bandwidth/schema#876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	thread := []string{}
	posted := 0
	i.run = func(ctx context.Context, stdin string, args ...string) (string, error) {
		if args[0] == "issue" {
			posted++
			thread = append(thread, strings.TrimSpace(stdin))
			return "", nil
		}
		var body strings.Builder
		body.WriteString("[")
		for n, line := range thread {
			if n > 0 {
				body.WriteString(",")
			}
			fmt.Fprintf(&body, "{\"body\": %q}", line)
		}
		body.WriteString("]")
		return body.String(), nil
	}
	// This run reads the board...
	if _, err := i.Events(); err != nil {
		t.Fatal(err)
	}
	// ...and while it is paused between that read and its append, another line files the
	// id it is about to use: a copied comment, or a broken random source in two clones.
	thread = append(thread, card(idA, "the card the other line filed while this one paused"))

	err = i.Append(card(idA, "the card this run drew the same id for"))
	if err != ErrExists {
		t.Errorf("Append of an existing id returned %v, want ErrExists: the board is re-read immediately before the append and the id looked for", err)
	}
	if posted != 0 {
		t.Errorf("the refused append posted %d comments; nothing is written when the id exists", posted)
	}
	// A later event about that card still appends: only creation is exclusive.
	if err := i.Append(later("taken", idA, "0000000000a1", idA, "bo", "2026-09-10T09:10:00Z", false)); err != nil {
		t.Errorf("a take after a refused create: %v", err)
	}
	if posted != 1 {
		t.Errorf("the take posted %d comments, want 1", posted)
	}
}

// [172] PIN THE DEADLINE FROM BOTH SIDES. --gh-timeout is the one deadline this tool has,
// and until now nothing exercised it: a budget nothing tests is a budget that can quietly
// become no budget at all, and a subprocess that never returns is a tool that has stopped
// saying anything while looking exactly like one that is working.
func TestGhTimeoutIsAFlagAndIsChecked(t *testing.T) {
	// The budget a caller names reaches the subprocess, on the read side and the write
	// side, as a context deadline and not as a hope.
	i, err := NewIssue("mas-bandwidth/schema#876", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var deadlines []time.Duration
	i.run = func(ctx context.Context, stdin string, args ...string) (string, error) {
		when, ok := ctx.Deadline()
		if !ok {
			t.Errorf("gh %s ran with no deadline at all", strings.Join(args, " "))
			return "[]", nil
		}
		deadlines = append(deadlines, time.Until(when).Round(time.Second))
		return "[]", nil
	}
	if _, err := i.Events(); err != nil {
		t.Fatal(err)
	}
	if err := i.Append(later("taken", idA, "0000000000a1", idA, "bo", "2026-09-10T09:10:00Z", false)); err != nil {
		t.Fatal(err)
	}
	if len(deadlines) != 2 {
		t.Fatalf("the read and the append ran %d subprocesses, want one each", len(deadlines))
	}
	for _, d := range deadlines {
		if d != 30*time.Second {
			t.Errorf("a gh call ran under a %s budget, want the 30s the caller named", d)
		}
	}
	// A budget of zero or less is NOT a backend, because there is no default duration to
	// fall back on: rule 8 names durations as well as paths, and a minute nobody chose is a
	// minute a reader never agreed to wait.
	for _, bad := range []time.Duration{0, -time.Second} {
		b, err := NewIssue("mas-bandwidth/schema#876", bad)
		if err == nil {
			t.Errorf("a %s budget gave a backend with a %s budget, want a refusal", bad, b.timeout)
		} else if !strings.Contains(err.Error(), "--gh-timeout") {
			t.Errorf("the refusal for a %s budget does not name the flag: %v", bad, err)
		}
	}
	// And a call that outlives its budget comes back NAMED, with the flag that widens it.
	slow, err := NewIssue("mas-bandwidth/schema#876", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	slow.run = func(ctx context.Context, stdin string, args ...string) (string, error) {
		<-ctx.Done()
		return "", fmt.Errorf("gh %s did not finish in time and was killed; nothing was left half-done by this tool, and a longer budget is --gh-timeout <seconds>", strings.Join(args, " "))
	}
	_, err = slow.Events()
	if err == nil {
		t.Fatal("a gh call past its budget returned no error")
	}
	if !strings.Contains(err.Error(), "--gh-timeout") {
		t.Errorf("the refusal does not name the flag that widens the budget: %v", err)
	}
	// The killGrace is what makes the budget real: gh's children inherit the output pipe
	// and hold it open after gh is gone, so the WaitDelay is not decoration.
	if killGrace <= 0 {
		t.Error("killGrace is not positive; a killed gh whose children hold the pipe blocks forever")
	}
}
