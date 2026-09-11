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
