package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// deliver is the coordinator's hand version of the friend daemon's delivery (the card
// deliver-is-the-daemons-duty-in-order; docs/SPEC-FRIEND.md, the delivery order): every card on
// the friend's row, read as friend cards answers her daemon, delivered by the same
// friend.Delivery her daemon runs, its job staged before its brief is written. It replaces the
// coordinator's stopgap deliver.py. It changes nothing on the table.
func init() {
	verbClasses["deliver"] = classMachine
	verbEffect["deliver"] = "local write: stages each card's job (jobs/<job>/repo and JOB.md) and then writes its inbox/<job>/BRIEF.md under <root>/<friend>-working, one DELIVER <job> staged|brief|skipped [<why>] line each; changes nothing on the table; --dry-run prints the lines it would and writes nothing"
	verbExit["deliver"] = "exit codes: 0 every card delivered or skipped for a reason that is not a failure (with no --once: interrupted), 1 a card whose stage or brief failed (its DELIVER line says why), no such friend, or no working directory for her, 2 usage or a store that did not answer"
}

// deliverEvery is how often deliver with no --once delivers her row again.
const deliverEvery = 15 * time.Second

func (a *app) cmdDeliver(args []string, stdout, stderr io.Writer) int {
	const name = "deliver"
	fs, c := a.verbSetup(name)
	root := fs.String("root", "", "the directory the friends' working directories are under, <root>/<friend>-working (else HOME, else the user's home)")
	once := fs.Bool("once", false, "deliver her row once and exit (default: again every --every until interrupted)")
	every := fs.Duration("every", deliverEvery, "with no --once, how often her row is delivered again")
	stages := fs.String("stages", "daemon", "who stages her jobs: daemon (this verb, as her daemon does, before each brief) or runner (her runner stages its own: briefs are written alone)")
	dry := fs.Bool("dry-run", false, "print the DELIVER line each card would have, and stage and write nothing (one pass)")
	mirrors := fs.String("mirrors", "", "the bare mirrors' directory, <mirrors>/<owner>/<name>.git (default: mirrors under her daemon's state dir)")
	pos, err := parse(fs, args)
	switch {
	case err != nil:
		return refuse(stderr, name, err.Error())
	case len(pos) != 1:
		return refuse(stderr, name, "wants one friend: deliver <friend> [--once]")
	case !sprint.ValidID(pos[0]):
		return refuse(stderr, name, "a friend name wants letters, digits, _ and -: "+oneline.Escape(pos[0]))
	case *stages != "daemon" && *stages != "runner":
		return refuse(stderr, name, "--stages wants daemon or runner, got "+oneline.Escape(*stages))
	case !*once && *every <= 0:
		return refuse(stderr, name, "--every wants a duration above zero, got "+every.String()+"; or say --once")
	case *mirrors != "" && !filepath.IsAbs(*mirrors):
		return refuse(stderr, name, "--mirrors wants an absolute path, got "+oneline.Escape(*mirrors))
	}
	who := pos[0]
	if *root == "" {
		*root = a.getenv("HOME")
	}
	if *root == "" {
		// ignored: no home is the refusal below
		*root, _ = os.UserHomeDir()
	}
	if *root == "" {
		return refuse(stderr, name, "wants --root <dir>, the directory the friends' working directories are under (HOME is not set)")
	}
	dir := filepath.Join(*root, who+"-working")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		fmt.Fprintf(stderr, "%s %s: her working directory %s is not a directory, and nothing is delivered outside it; run: ls -la %s (or name the directory it is under with --root)\n", prog, name, oneline.Escape(dir), oneline.Escape(dir))
		return 1
	}
	dl := friend.Delivery{Dir: dir}
	if *stages == "daemon" {
		m := *mirrors
		if m == "" {
			m = filepath.Join(friend.FindStateDir(a.getenv("HOME"), dir, who), friend.MirrorsDir)
		}
		dl.Stage = (&friend.Stager{Dir: dir, Mirrors: m}).Stage
	}
	ctx := context.Background()
	if *dry {
		dl.DryRun = true
	}
	if *once || *dry {
		code, _ := a.deliverPass(ctx, *c, who, dl, nil, stdout, stderr)
		return code
	}
	if a.notify != nil {
		var cancel context.CancelFunc
		ctx, cancel = a.notify(ctx)
		defer cancel()
	}
	after := a.after
	if after == nil {
		after = time.After
	}
	said := map[string]string{}
	for {
		if ctx.Err() != nil {
			fmt.Fprintln(stdout, "DELIVER STOP interrupted")
			return 0
		}
		if code, why := a.deliverPass(ctx, *c, who, dl, said, stdout, stderr); code == 2 && why != "" {
			fmt.Fprintf(stderr, "%s %s: %s; the next try is in %s\n", prog, name, why, *every)
		}
		select {
		case <-ctx.Done():
		case <-after(*every):
		}
	}
}

// deliverPass delivers her row once and prints each card's DELIVER line (with said, only a
// line that changed since the last pass). It answers the exit code and, for a pass that could
// not read her row, why.
func (a *app) deliverPass(ctx context.Context, c common, who string, dl friend.Delivery, said map[string]string, stdout, stderr io.Writer) (int, string) {
	const name = "deliver"
	st, err := a.store(c)
	if err != nil {
		if said != nil {
			return 2, err.Error()
		}
		return refuse(stderr, name, err.Error()), ""
	}
	names, err := st.FriendNames(ctx)
	if err != nil {
		if said != nil {
			return 2, err.Error()
		}
		return a.readFailed(name, err, stderr), ""
	}
	if !slices.Contains(names, who) {
		fmt.Fprintf(stderr, "%s %s: no friend %s on the friends table (friends: %s); run: nova-sprint friend sync\n", prog, name, who, orDashStr(strings.Join(names, ","), "none"))
		return 1, ""
	}
	held, err := friendCardsOf(ctx, st, who)
	if err != nil {
		if said != nil {
			return 2, err.Error()
		}
		return a.readFailed(name, err, stderr), ""
	}
	code := 0
	for _, o := range dl.DeliverRow(ctx, held) {
		if o.Err != nil && !isStartedElsewhere(o.Err) {
			code = 1
		}
		line := o.Line()
		if said != nil {
			if said[o.Job] == line {
				continue
			}
			said[o.Job] = line
		}
		fmt.Fprintln(stdout, line)
	}
	return code, ""
}

func isStartedElsewhere(err error) bool {
	var away *friend.StartedElsewhere
	return errors.As(err, &away)
}
