package fold_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

// addReads puts three read cards on route flash: two typed read (one 8+, one
// not) and one cold read nobody priced. Read cards open no PR by design, so
// they must never sit on a code row (fold 2026-09-22 finding 4: 802 read cards
// made flash look useful).
func addReads(t *testing.T, client *redis.Client, sprint string) {
	t.Helper()
	ctx := context.Background()
	s := "s:" + sprint
	for label, fields := range map[string]map[string]string{
		"r1": {"kind": "model", "type": "read", "route": "flash", "state": "ended", "outcome": "DONE", "repo": "nova-tools", "pr": "103", "head": "aaaa3333", "usd": "0.01", "score": "9"},
		"r2": {"kind": "model", "type": "read", "route": "flash", "state": "ended", "outcome": "DONE", "repo": "nova-tools", "pr": "106", "head": "bbbb6666", "usd": "0.02", "score": "5"},
		"r3": {"kind": "model", "type": "cold", "route": "flash", "state": "ended", "outcome": "DONE"},
	} {
		if err := client.HSet(ctx, s+":card:"+label, fields).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.SAdd(ctx, s+":idx:card:ended", label).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// Control 55 (#2756 v6 4.1.1), the unknown half and the split: a card with no
// end record makes the fold print `unknown 1` in red and exit 1, with no
// commit and the sprint still closed; a PR with no state record is unknown the
// same way; read and code cards are separate rows per class, type and route;
// the approved-not-landed PRs print with their why line.
func TestControl55Unknown(t *testing.T) {
	t.Parallel()

	t.Run("card-with-no-end-record", func(t *testing.T) {
		mr, client, fx := seed(t)
		work := workRepo(t)
		ctx := context.Background()
		s := "s:" + fx.Sprint
		addReads(t, client, fx.Sprint)
		// c10 was dealt and never ended: no outcome, so nobody knows if it was OK.
		client.HSet(ctx, s+":card:c10", "kind", "model", "type", "fix", "route", "fable", "state", "dealt")
		client.SAdd(ctx, s+":idx:card:dealt", "c10")

		var out bytes.Buffer
		_, err := fold.Run(ctx, client, opts(fx, work), &out)
		var ue *fold.UnknownError
		if !errors.As(err, &ue) || ue.N() != 1 {
			t.Fatalf("fold with one unended card: err %v, want an UnknownError of 1\n%s", err, out.String())
		}
		red := "\x1b[31mFOLD UNKNOWN sprint=" + fx.Sprint + " unknown 1 cards=1 prs=0 first=c10\x1b[0m\n"
		if !strings.Contains(out.String(), red) {
			t.Fatalf("no red unknown line %q in\n%s", red, out.String())
		}
		if n := len(strings.Fields(git(t, work, "log", "--format=%H"))); n != 1 {
			t.Fatalf("a fold with unknown outcomes made a commit (%d commits)", n)
		}
		if st := client.HGet(ctx, s, "status").Val(); st != "closed" {
			t.Fatalf("status %q after a refused fold, want closed", st)
		}

		// Read and code cards are separate rows; flash has only read rows.
		o := out.String()
		for _, want := range []string{
			"FOLD SPLIT sprint=" + fx.Sprint + " class=code type=fix route=fable cards=4 done=2 useful=2 prs=2 landed=2 closed=0 open=0 usd=1.3 usd_per_useful=- usd_per_landed=- unpriced=1\n",
			"FOLD SPLIT sprint=" + fx.Sprint + " class=code type=nx route=sonnet cards=2 done=2 useful=0 prs=2 landed=0 closed=0 open=2 usd=0.4 usd_per_useful=- usd_per_landed=- unpriced=0\n",
			"FOLD SPLIT sprint=" + fx.Sprint + " class=code type=nx route=kimi cards=1 done=1 useful=0 prs=1 landed=0 closed=1 open=0 usd=0.05 usd_per_useful=- usd_per_landed=- unpriced=0\n",
			"FOLD SPLIT sprint=" + fx.Sprint + " class=read type=cold route=flash cards=1 done=1 useful=0 prs=0 landed=0 closed=0 open=0 usd=- usd_per_useful=- usd_per_landed=- unpriced=1\n",
			"FOLD SPLIT sprint=" + fx.Sprint + " class=read type=read route=flash cards=2 done=2 useful=1 prs=0 landed=0 closed=0 open=0 usd=0.03 usd_per_useful=0.03 usd_per_landed=- unpriced=0\n",
			"FOLD CLASS sprint=" + fx.Sprint + " class=code cards=10 done=7 useful=4 prs=7 landed=3 closed=1 open=3 usd=2.45 usd_per_useful=- usd_per_landed=- unpriced=2\n",
			"FOLD CLASS sprint=" + fx.Sprint + " class=read cards=3 done=3 useful=1 prs=0 landed=0 closed=0 open=0 usd=0.03 usd_per_useful=- usd_per_landed=- unpriced=1\n",
			"FOLD APPROVED-NOT-LANDED sprint=" + fx.Sprint + " pr=nova-tools#103 route=fable type=nx head=aaaa3333 state=reading why=\"mergeable CONFLICTING\"\n",
		} {
			if !strings.Contains(o, want) {
				t.Errorf("missing line %q in\n%s", want, o)
			}
		}
		for _, line := range strings.Split(o, "\n") {
			if strings.HasPrefix(line, "FOLD SPLIT") && strings.Contains(line, "class=code") && strings.Contains(line, "route=flash") {
				t.Errorf("a read card is on a code row: %s", line)
			}
		}
		// #107's APPROVE is at an old head and #106 is held: neither is approved at head.
		if n := strings.Count(o, "FOLD APPROVED-NOT-LANDED"); n != 1 {
			t.Errorf("%d approved-not-landed lines, want 1 (#103)\n%s", n, o)
		}
		// Every code row is in the class line: the split is complete.
		if !(strings.Index(o, "class=code type=fix") < strings.Index(o, "class=read type=cold") &&
			strings.Index(o, "FOLD CLASS sprint="+fx.Sprint+" class=code") < strings.Index(o, "FOLD CLASS sprint="+fx.Sprint+" class=read")) {
			t.Errorf("code rows print before read rows:\n%s", o)
		}

		// The verb exits 1 on unknown outcomes (2 stays refusal).
		var stdout, stderr bytes.Buffer
		code := fold.Main(ctx, []string{fx.Sprint, "--store", mr.Addr(), "--work", work}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stdout.String(), "unknown 1") || !strings.Contains(stderr.String(), "unknown 1") {
			t.Fatalf("Main on unknown outcomes: exit %d\nstdout %s\nstderr %s", code, stdout.String(), stderr.String())
		}

		// Once c10 ends, the fold finishes, and the record carries the split.
		client.HSet(ctx, s+":card:c10", "state", "unresolved", "outcome", "FAILED")
		out.Reset()
		if _, err := fold.Run(ctx, client, opts(fx, work), &out); err != nil {
			t.Fatalf("fold after c10 ended: %v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "FOLD OUTCOMES sprint="+fx.Sprint+" unknown 0 cards=14 prs=7\n") {
			t.Errorf("no unknown 0 line:\n%s", out.String())
		}
		body, err := os.ReadFile(filepath.Join(work, "docs", "roadmaps", "folds", fx.Sprint+".sexp"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`(class "code" :cards 10 :done 7 :useful 4 :prs 7 :landed 3 :closed 1 :open 3 :usd "2.45"`,
			`(class "read" :cards 3 :done 3 :useful 1 :prs 0 :landed 0 :closed 0 :open 0 :usd "0.03"`,
			`(split "read" "read" "flash" :cards 2`,
			`(approved-not-landed "nova-tools#103" :route "fable" :type "nx" :head "aaaa3333" :state "reading" :why "mergeable CONFLICTING")`,
		} {
			if !bytes.Contains(body, []byte(want)) {
				t.Errorf("fold file lacks %q:\n%s", want, body)
			}
		}
	})

	t.Run("pr-with-no-state", func(t *testing.T) {
		_, client, fx := seed(t)
		work := workRepo(t)
		ctx := context.Background()
		client.Del(ctx, "s:"+fx.Sprint+":pr:nova-tools:107")
		var out bytes.Buffer
		_, err := fold.Run(ctx, client, opts(fx, work), &out)
		var ue *fold.UnknownError
		if !errors.As(err, &ue) || ue.N() != 1 {
			t.Fatalf("fold with a stateless PR: err %v, want an UnknownError of 1\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "\x1b[31mFOLD UNKNOWN sprint="+fx.Sprint+" unknown 1 cards=0 prs=1 first=nova-tools#107\x1b[0m\n") {
			t.Fatalf("no red unknown line for #107:\n%s", out.String())
		}
		if n := len(strings.Fields(git(t, work, "log", "--format=%H"))); n != 1 {
			t.Fatalf("a fold with an unknown PR made a commit")
		}
	})

	t.Run("why-names-each-gate", func(t *testing.T) {
		_, client, fx := seed(t)
		work := workRepo(t)
		ctx := context.Background()
		client.HSet(ctx, "s:"+fx.Sprint+":pr:nova-tools:103", "mergeable", "MERGEABLE", "holds_open", "2",
			"stack_parent", "nova-tools#99", "drop_key", "gate-red", "lane", "tools-1", "lane_result", "GATE-RED")
		var out bytes.Buffer
		if _, err := fold.Run(ctx, client, opts(fx, work), &out); err != nil {
			t.Fatalf("fold: %v\n%s", err, out.String())
		}
		want := `why="holds 2 open; stack parent nova-tools#99; dropped gate-red; lane tools-1 GATE-RED"`
		if !strings.Contains(out.String(), want) {
			t.Fatalf("no %s in\n%s", want, out.String())
		}
	})
}
