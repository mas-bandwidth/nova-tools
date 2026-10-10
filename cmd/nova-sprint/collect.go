package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// collect is the coordinator's hand on the friends' outboxes (the coordinator's stopgap
// finish-loop.py finished 92 cards on the night of 2026-10-05; docs/SPEC-SPRINT.md section
// 1, collect): every outbox/<job>/REPORT.md, in any friend's tree, whose job is a work card
// working on a friend's row finishes that card as her row (sprint.Collect), and with
// --dead-lanes a lane her runner ENDed with no report finishes failed so the card is dealt
// again. A LAND finishes only at origin's tip of the card's branch (friendFinish); a finish
// leaves working, so a report finished once is never finished twice. Her nova-friend daemon
// runs the same collection for her own tree on every sync (pkg/friend outbox.go).

func init() {
	verbClasses["collect"] = classCoordinator
	// it reads the friends' working directories on the machine it is typed on
	notServed = append(notServed, "collect")
	verbExit["collect"] = "exit codes: 0 done (each card on its line, a refused or left one among them, read again by the next collect), 1 refused (a friend not on the roster), 2 usage or a store that did not answer, 3 the config could not be read or holds no friend row"
	verbEffect["collect"] = "store write: finishes each working card of the friends' rows that a report in any friend's outbox (or, with --dead-lanes, her runner's END with no report) finishes, as her row; reads the friends' working directories and writes nothing there, and reads origin's tip (one git ls-remote) for each LAND; --dry-run writes nothing and reads no tip"
}

// runnerLogCap bounds the runner log collect reads: its last runnerLogCap bytes.
const runnerLogCap = 4 << 20

// runnerLog is the log of the runner that runs the friend's cards, its last runnerLogCap
// bytes: runner.log in her working directory dir, else beside it (a bud's runner keeps
// <bud>/runner.log and <bud>/working, which <root>/<friend>-working links to), never root's
// own; "" when there is none.
func runnerLog(root, dir string) string {
	paths := []string{filepath.Join(dir, "runner.log")}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		rootReal, _ := filepath.EvalSymlinks(root) // ignored: a root that cannot be resolved is compared as it is
		if up := filepath.Dir(real); up != cmp.Or(rootReal, root) {
			paths = append(paths, filepath.Join(up, "runner.log"))
		}
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			_ = f.Close() // ignored: only read
			continue
		}
		off := max(fi.Size()-runnerLogCap, 0)
		b := make([]byte, fi.Size()-off)
		n, _ := f.ReadAt(b, off) // ignored: a short read is a shorter log
		_ = f.Close()            // ignored: only read
		return string(b[:n])
	}
	return ""
}

func (a *app) cmdCollect(args []string, stdout, stderr io.Writer) int {
	const name = "collect"
	fs, c := a.verbSetup(name)
	pg := fs.String("pg", "", "the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN), as friend sync reads the roster from it")
	root := fs.String("root", "", "the directory the friends' working directories are under, <root>/<friend>-working (else HOME); collect reads every friend's outbox there and writes nothing in it")
	dead := fs.Bool("dead-lanes", false, "also finish failed each working card with no report whose friend's runner ENDed its job with report=no (runner.log in her working directory or beside it), so the card is dealt again")
	dry := fs.Bool("dry-run", false, "print what would be finished; finish nothing and read no tip")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	rows, err := a.friends(ctx, *pg)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was finished\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config friend list"))
		return exitCannotRead
	}
	if len(rows) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no friend row; run: nova-config friend list; nothing was finished\n", prog, name)
		return exitCannotRead
	}
	var roster []string
	for _, r := range rows {
		if sprint.ValidID(r.Name) {
			roster = append(roster, r.Name)
		}
	}
	only := roster
	if len(pos) > 0 {
		for _, f := range pos {
			if !slices.Contains(roster, f) {
				fmt.Fprintf(stderr, "%s %s: friend %s is not on the roster; run: nova-config friend list; nothing was finished\n", prog, name, oneline.Escape(f))
				return 1
			}
		}
		only = pos
	}
	if *root == "" {
		*root = a.getenv("HOME")
	}
	if *root == "" {
		return refuse(stderr, name, "wants --root <dir>, the directory the friends' working directories are under (HOME is not set)")
	}

	// the work cards working on the named friends' rows, each as its job
	var cards []sprint.CollectCard
	packets := map[string]sprint.Packet{}
	want := map[string]bool{}
	for _, f := range only {
		cs, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(f), sprint.Working)
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		ps, err := st.Packets(ctx, cs)
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		for _, p := range ps {
			if p.Kind == "read" || !sprint.ValidCardID(p.Card) {
				continue
			}
			job := friendJobOf(p)
			cards = append(cards, sprint.CollectCard{Friend: f, Card: p.Card, Job: job})
			packets[f+"/"+p.Card], want[job] = p, true
		}
	}
	// every friend's tree: a report written ahead may sit in another friend's outbox
	trees := make([]sprint.CollectTree, 0, len(roster))
	at := map[string]time.Time{}
	for _, f := range roster {
		dir := filepath.Join(*root, f+"-working")
		t := sprint.CollectTree{Friend: f, Reports: map[string]string{}, Unread: map[string]string{}}
		for job := range want {
			report, why, when, err := friendReadReport(dir, job)
			switch {
			case err != nil:
				t.Unread[job] = oneline.Cap(err.Error(), 300)
			case why != "":
				t.Unread[job] = why
			case report != "":
				t.Reports[job], at[f+"/"+job] = report, when
			}
		}
		if *dead && slices.Contains(only, f) {
			t.Runner = runnerLog(*root, dir)
		}
		trees = append(trees, t)
	}

	var said []string
	say := func(l string) {
		said = append(said, l)
		if !c.json {
			fmt.Fprintln(stdout, l)
		}
	}
	finished, failed, refused, left := 0, 0, 0, 0
	for _, x := range sprint.Collect(cards, trees, *dead) {
		p := packets[x.Friend+"/"+x.Card]
		if x.Left != "" {
			left++
			say(fmt.Sprintf("COLLECT %s LEFT %s; the next collect reads it again", x.Card, oneline.Escape(x.Left)))
			continue
		}
		row := sprint.FriendRow(x.Friend)
		r := sprint.FinishReq{Sel: sprint.Sel{IDs: []string{p.Card}}, As: row, Gens: map[string]int{p.Card: p.Gen}, Branch: p.Branch, Who: row, Failed: true, Report: x.Report}
		if !x.Dead {
			var report string
			for _, t := range trees {
				if t.Friend == x.From {
					report = t.Reports[x.Job]
				}
			}
			if *dry {
				r.Head = x.Head
				r.Failed = x.Failed
			} else {
				fr, err := friendFinish(ctx, x.Friend, p, report, a.tip)
				if err != nil {
					refused++
					say(fmt.Sprintf("COLLECT %s REFUSED %s; the card is not finished, and the next collect reads the report again", x.Card, oneline.Escape(err.Error())))
					continue
				}
				if fr.Failed {
					// the report's first 600 characters, as the daemon's outbox pass carries them
					fr.Report = x.Report
					if globs, ok := member.PathsProposed(report); ok && len(globs) > 0 {
						fr.Report += "; " + member.ProposedKey + " " + strings.Join(globs, ",")
					}
				}
				r = fr
				r.Reported = at[x.From+"/"+x.Job]
			}
		}
		if !*dry {
			// a report her own session wrote in her own tree is her session's evidence
			why, err := a.friendFinishStep(ctx, st, x.Friend, p, r, !x.Dead && x.From == x.Friend, say)
			if err != nil {
				return a.readFailed(name, err, stderr)
			}
			if why != "" {
				refused++
				say(fmt.Sprintf("COLLECT %s REFUSED %s", x.Card, oneline.Escape(why)))
				continue
			}
		}
		dryWord := ""
		if *dry {
			dryWord = " (dry run: not finished)"
		}
		if r.Failed {
			failed++
			say(fmt.Sprintf("COLLECT %s FAILED %s%s", x.Card, oneline.Escape(oneline.Cap(r.Report, 300)), dryWord))
			continue
		}
		finished++
		say(fmt.Sprintf("COLLECT %s LAND %s%s", x.Card, r.Head, dryWord))
	}
	line := fmt.Sprintf("COLLECT OK friends=%d working=%d landed=%d failed=%d refused=%d left=%d", len(only), len(cards), finished, failed, refused, left)
	if *dry {
		line += " (dry run: nothing was finished)"
	}
	sayOK(stdout, c.json, name, line, map[string]any{"friends": len(only), "working": len(cards), "landed": finished, "failed": failed,
		"refused": refused, "left": left, "cards": orEmpty(said), "dry_run": *dry})
	return 0
}

// friendFinishStep runs the finish r of a friend's card p as her row, friendCollect's step:
// refused is the sprint's why when it refused the finish. A finish taken from a report her
// session wrote in her own tree (evidence) is recorded as her session's evidence; a report
// in another's tree, or a lane that ended with none, is no evidence of her.
func (a *app) friendFinishStep(ctx context.Context, st *store.Store, name string, p sprint.Packet, r sprint.FinishReq, evidence bool, say func(string)) (refused string, err error) {
	step := store.FinishStep(r)
	step.Actor, step.Epoch = r.Who, &p.Epoch
	res, err := st.Run(ctx, step)
	if err != nil {
		return "", err
	}
	if len(res.Refused) > 0 {
		return res.Refused[0].Why, nil
	}
	if !evidence {
		return "", nil
	}
	// her row reads up on it for sprint.FriendFinishWindow (docs/SPEC-FRIEND.md, "Presence is
	// her session's evidence"); a record not written costs her that, never the finish
	if err := st.FriendFinished(ctx, name, a.now()); err != nil {
		say(fmt.Sprintf("COLLECT %s NOTE the finish is not recorded as her evidence: %s", p.Card, oneline.Escape(err.Error())))
	}
	return "", nil
}
