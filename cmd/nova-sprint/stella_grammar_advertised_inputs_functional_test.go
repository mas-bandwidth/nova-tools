//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestStellaGrammarAdvertisedIDInputs is Stella's independent walk of #4399
// (bus stella-ab9422c9b483), preserved under her name: the cold walk with
// the three --ids forms HelpIDs advertises and that failed at 98eec3cdc,
// `card render --ids @-` (stdin p1), `read brief --ids @ids.txt` and
// `read brief --ids @-`. Every line's stdin is "p1\n".
func TestStellaGrammarAdvertisedIDInputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	base, head, mirror := walkMirror(t, dir)
	var tsv strings.Builder
	tsv.WriteString("id\ttitle\tstream\tpaths\tdone-when\tdepends-on\tbody\n") // one invariant a card (#4396)
	for i := 1; i <= 10; i++ {
		dep := "-"
		if i%2 == 0 {
			dep = "none"
		}
		fmt.Fprintf(&tsv, "p%d\tprobe %d\tprobe-a\tp%d.go\tgo test passes\t%s\t%s\n", i, i, i, dep, cutInv)
	}
	writeFile(t, filepath.Join(dir, "cards.tsv"), tsv.String())
	writeFile(t, filepath.Join(dir, "nolabel.md"), "BASE: dev\nBASE-SHA: "+base+"\nPATHS: a.go\nDEPENDS-ON: none\nDONE-WHEN: it holds\n\nbody\n")

	env := []string{grammarAsToolEnv + "=1", "NOVA_SPRINT_REDIS=" + addr, seatEnv + "=rowan", "USER=rowan",
		"HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "PATH=" + os.Getenv("PATH")}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "NOVA_TEST_NO_HOST=") || strings.HasPrefix(kv, "TMPDIR=") {
			env = append(env, kv)
		}
	}
	type result struct {
		code     int
		out, err string
	}
	runLine := func(argv []string) result {
		cmd := exec.Command(self, argv...)
		cmd.Dir, cmd.Env = dir, env
		cmd.Stdin = strings.NewReader("p1\n")
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		_ = cmd.Run()
		return result{cmd.ProcessState.ExitCode(), out.String(), errOut.String()}
	}
	nextRE := regexp.MustCompile(`(?:run|without it): (nova-sprint [^;\n]*)`)
	tailRE := regexp.MustCompile(`run:? nova-sprint help`) // the tail; the help verb's own usage line names "nova-sprint help"
	var lines, helpTail, natural int
	check := func(kind, line string, want ...string) {
		t.Helper()
		lines++
		if kind != "retired" {
			natural++
		}
		r := runLine(splitLine(line))
		all := r.out + r.err
		if tailRE.MatchString(all) {
			helpTail++
		}
		fail := func(why string) {
			t.Errorf("nova-sprint %s: %s; exit %d:\n%s", line, why, r.code, strings.TrimSpace(all))
		}
		if strings.Contains(all, "provided but not defined") {
			fail("Go's flag error")
		}
		for _, w := range want {
			if !strings.Contains(all, w) {
				fail(fmt.Sprintf("lacks %q", w))
			}
		}
		refusedAgain := func(r result) bool { return r.code == 2 || strings.Contains(r.err, "; usage: nova-sprint ") }
		switch kind {
		case "ok":
			if r.code != 0 {
				fail("want exit 0")
			}
		case "help":
			if r.code != 0 || !strings.HasPrefix(r.out, "usage: nova-sprint "+strings.TrimSuffix(line, " -h")) || !strings.Contains(r.out, "example:\n") {
				fail("want its own usage and examples on stdout")
			}
		case "answer": // the store answered: exit 0 or 1, never a usage refusal
			if refusedAgain(r) {
				fail("refused before the store")
			}
		case "shape": // a refusal that says what the input is
			if r.code == 0 {
				fail("want a refusal")
			}
		case "next", "retired", "next-h":
			m := nextRE.FindAllStringSubmatch(all, -1)
			if r.code != 2 || len(m) == 0 {
				fail("want exit 2 and the exact next line")
				return
			}
			next := strings.TrimSpace(m[len(m)-1][1])
			argv := splitLine(strings.TrimPrefix(next, "nova-sprint "))
			if kind == "next-h" { // a line that runs a loop: its flags are held by -h
				argv = append(argv, "-h")
			}
			lines++
			r2 := runLine(argv)
			if tailRE.MatchString(r2.out + r2.err) {
				helpTail++
			}
			switch {
			case kind == "next-h" && (r2.code != 0 || !strings.HasPrefix(r2.out, "usage: nova-sprint ")):
				t.Errorf("%s: the printed line %q with -h: exit %d:\n%s", line, next, r2.code, r2.out+r2.err)
			case kind != "next-h" && refusedAgain(r2):
				t.Errorf("%s: the printed line %q was refused again: exit %d:\n%s", line, next, r2.code, r2.out+r2.err)
			}
		}
	}
	for _, p := range []string{"card", "card cut", "card push", "stream", "stream open", "task", "task ls", "task take",
		"sprint", "scope", "read brief", "ready", "reconcile", "capacity bench"} {
		check("help", p+" -h")
	}
	check("ok", "help", "registered verbs")
	check("shape", "card push --sprint probe", "LABEL: <id>", "KIND: one of")
	check("shape", "card push --sprint probe nolabel.md", "missing label: a card names its id on a first line RESULT: <id> or a LABEL: <id>")
	check("ok", "card cut --from cards.tsv --repo mas-bandwidth/nova-tools --stream probe-a --no-github", "rows=10 cut=10 ")
	writeFile(t, filepath.Join(dir, "ids.txt"), "p1\n")
	check("answer", "card render --ids p1", "CARD RENDER REFUSED id=p1")
	check("answer", "card render --ids @ids.txt", "CARD RENDER REFUSED id=p1")
	check("answer", "card render --ids @-", "CARD RENDER REFUSED id=p1")
	check("ok", "stream ls", `"probe-a" waiting=`)
	check("ok", "task ls", "p10 stream=probe-a where=waiting")
	check("ok", "task ls --stream probe-a", "TASK ls n=11 ")
	check("next", "task ls --state waiting", "--state is spelled --where")
	check("answer", "ready --why p1", "WAITING p1 stream=probe-a met=none")
	check("answer", "ready --why task:p1", "WAITING p1 stream=probe-a met=none")
	check("next-h", "reconcile --sprint probe --once", "without it: nova-sprint reconcile --once")

	// The reconciler's waiting-resolve duty, one pass: every card cut with
	// depends-on - or none is released (item 8).
	lease, err := reconcile.Acquire(ctx, store.New(client), reconcile.AcquireOptions{Host: "walk"})
	if err != nil {
		t.Fatal(err)
	}
	if counts, err := (&reconcile.WaitingResolve{Client: client}).Run(ctx, lease); err != nil || counts.Routed != 10 {
		t.Fatalf("waiting-resolve: routed %d err %v, want the ten", counts.Routed, err)
	}
	if n := client.ZCard(ctx, taskcard.StreamKeyAt(0, "probe-a", "ready")).Val(); n != 10 {
		t.Fatalf("ws:probe-a:ready holds %d, want ten", n)
	}

	check("ok", "task ls --stream probe-a --where ready", "TASK ls n=10 where=ready")
	check("ok", "ready --why p1", "READY probe-a/p1")
	check("ok", "capacity machine --machine studio --slots 32")
	check("ok", "capacity friend --as rowan --machine studio --slots 10")
	check("ok", "capacity bench --as studio --machine studio --slots 2")
	check("ok", "card deal --ids p1,p2,p3,p4,p5,p6,p7,p8,p9,p10 --to friend:rowan", "n=10 ")
	check("ok", "card work --as friend:rowan --n 10", "n=10 ")
	check("ok", "task take --as bench:studio", "TASK take n=0 ")
	check("ok", "pr record --repo nova-tools --n 4399 --head "+head+" --base dev --base-sha "+base+" --stream probe-a --task p1")
	// A code copy's ok with a PR runs the spec gate (#4313, dev #4401) in the
	// child's checkout; with none named the end is refused with the line
	// that runs it. The gate itself (go test at base and head, then nova-ci
	// local) is TestEveryOkDoorRunsTheSpecGate's; the walk stands in for its
	// pass with the one end the verb makes after it.
	check("answer", "card end --ids p1~1 --ok --pr 4399 --head "+head, "wants --checkout <your checkout at --head>")
	if _, err := taskcard.End(ctx, client, taskcard.EndRequest{IDs: []string{"p1~1"}, OK: true, PR: "4399", Repo: "nova-tools", Head: head, By: "rowan"}); err != nil {
		t.Fatalf("the end after the gate's pass: %v", err)
	}
	check("next", "read brief --id p1 --mirror "+mirror, "--id is spelled --ids")
	check("ok", "read brief --ids p1 --mirror "+mirror, "READ BRIEF repo=nova-tools n=4399 ", " out=- diff=- ", "+// walk")

	check("ok", "read brief --ids @ids.txt --mirror "+mirror, "READ BRIEF repo=nova-tools n=4399 ")
	check("ok", "read brief --ids @- --mirror "+mirror, "READ BRIEF repo=nova-tools n=4399 ")

	// The retired spellings, typed on purpose: the corrected whole line.
	check("retired", "card work --actor friend:rowan --n 1", "--actor is spelled --as; run: nova-sprint card work --as friend:rowan --n 1")
	check("retired", "task ls --streams probe-a", "--streams is spelled --stream; run: nova-sprint task ls --stream probe-a")
	check("retired", "stream order --scope probe-a", "--scope is spelled --stream; run: nova-sprint stream order --stream probe-a")
	check("retired", "friend show --friend rowan", "--friend is spelled --as; run: nova-sprint friend show --as rowan")
	check("retired", "pitstop set --sprint probe --scope probe-a --why 'the walk'", "--scope is spelled --stream; run: nova-sprint pitstop set --sprint probe --stream probe-a --why 'the walk'")
	check("retired", "card render --label p2", "--label is spelled --ids; run: nova-sprint card render --ids p2")
	check("retired", "census --store "+addr+" --sprint probe", "--store is spelled --redis; run: nova-sprint census --redis "+addr+" --sprint probe")

	if helpTail != 0 {
		t.Errorf("%d of %d lines printed the help tail; want 0", helpTail, lines)
	}
	t.Logf("walk: %d lines (%d natural), help tail %d", lines, natural, helpTail)
}
