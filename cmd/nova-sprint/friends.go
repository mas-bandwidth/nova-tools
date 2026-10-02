package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The friends table (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-02: "add a
// friends table, above fleet and below merge. friends | status for now.
// up/down/held"; "friends should be configured in nova-config"; "you should use
// heartbeats from each friend to track their state, and sort them alphabetically,
// and then by status, like with fleet"; and the same day: "please give friends in
// the friends table the same ready, working, width, done, ok%, status that we have
// for machines, but no load, since they don't correspond to a machine (at the
// moment...)"; "you can even use the inbox/outbox standard in friend's working
// dirs"). The roster is nova-config's friend rows alone, copied into the store by
// friend sync, which also reads each friend's working directory and writes her
// job cards: a friend works only in <root>/<friend>-working, the coordinator
// delivers a job as inbox/<job>/ and collects outbox/<job>/REPORT.md, and only the
// coordinator reaches out, so the sync is a read of the directories, never a
// write, and the view reads the store, never the directories. A friend's
// machinery beats with friend beat; the coordinator holds one with friend down
// and releases it with friend up. The status is derived where it is shown
// (store.FriendRows), by the fleet's rule and in the fleet's order.

// friendWidth is every friend's width, the jobs she works at once: one, a
// person-like agent on one job (the friend row of nova-config has no width).
const friendWidth = 1

// friendWords is how a friend's row comes about, in nova-sprint help and
// nova-sprint help friend.
func friendWords() string {
	return strings.TrimSpace(`
The friends: the friends table is nova-config's friend rows, copied into the
store by friend sync (--pg, else NOVA_PG_DSN, as nova-config takes it): a friend
the store lacks is added, one nova-config no longer has is taken off with her
beat and her jobs, and a friend that stays keeps her hold. The same sync reads
each friend's working directory, <root>/<friend>-working (--root, else HOME; a
friend with no directory has no jobs), and writes her job cards: each directory
under inbox/ is a job, ready until outbox/<job>/ exists (the friend makes it
when she starts), working until outbox/<job>/REPORT.md exists, then done; done
failed when the report's first Verdict: or Status: line says HOLD, FAIL, FAILED
or BROKEN, else done ok. The sync reads the directories and never writes them;
run it on the machine that holds them, by the coordinator's loop or by hand
after a job is delivered or collected. where counts the cards: ready, working,
width (`+fmt.Sprint(friendWidth)+`, one job at a time), done (ok and failed), ok% (ok over done, pooled in
the footer) and status. A friend says she is there with
nova-sprint friend beat <friend>, which her own machinery runs every few seconds
beside the friend's harness, for example in the wrapper that starts it
  while :; do nova-sprint friend beat <friend> >/dev/null 2>&1; sleep 5; done &
  trap 'kill $!' EXIT
and her status is the fleet's rule: up until she has missed `+fmt.Sprint(sprint.MissedBeatsDown)+` beat windows of
`+sprint.BeatDeadline.String()+` in a row, down past that or when she has never beaten, held while friend
down holds her whatever she beats (friend up releases the hold). where shows the
friends after merge and before fleet, up first, then held, then down, each by
name, with no load column.`) + "\n"
}

// friendsFn reads nova-config's friend rows' names (friend sync), given the
// address of the config store (its --pg, else NOVA_PG_DSN).
type friendsFn func(ctx context.Context, pg string) ([]string, error)

// readFriends is the real friendsFn: the friend rows of Postgres, by
// config.ResolveDSN, bounded.
func (a *app) readFriends(ctx context.Context, pg string) ([]string, error) {
	var names []string
	err := a.withConfig(ctx, pg, func(ctx context.Context, st config.Store) error {
		rows, err := st.List(ctx, config.KindFriend)
		for _, r := range rows {
			names = append(names, r.Name)
		}
		return err
	})
	return names, err
}

// friendJobs is the job cards of one friend's working directory, the
// inbox/outbox standard: each directory under inbox/ (a name beginning with
// a dot, or a file, is none) is a job; it is ready while outbox/<job> is not
// there, working while it is there without REPORT.md, done once REPORT.md is
// there, ok by reportOK over the report's text. A directory or an inbox that
// is not there is no jobs. It reads and never writes.
func friendJobs(dir string) ([]store.FriendJob, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "inbox"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var jobs []store.FriendJob
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		job := store.FriendJob{ID: e.Name(), State: store.JobReady}
		out := filepath.Join(dir, "outbox", e.Name())
		if _, err := os.Stat(out); err == nil {
			job.State = store.JobWorking
			report, err := os.ReadFile(filepath.Join(out, "REPORT.md"))
			switch {
			case err == nil:
				job.State, job.OK = store.JobDone, reportOK(string(report))
			case !errors.Is(err, fs.ErrNotExist):
				return nil, err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// reportOK is the one rule of a report's verdict: the first line of REPORT.md
// whose key, after any markdown marks (#, *, -, _, spaces), is Verdict or
// Status in any case; its first word, HOLD, FAIL, FAILED or BROKEN in any
// case, is a job done failed, and any other word, or no such line, is a job
// done ok.
func reportOK(report string) bool {
	for _, line := range strings.Split(report, "\n") {
		key, rest, ok := strings.Cut(strings.TrimLeft(line, "#*-_ \t"), ":")
		if !ok {
			continue
		}
		// The key's markdown marks wrap it, not only start the line: key-only
		// bold **Verdict** leaves a trailing mark that a left trim misses.
		switch strings.ToLower(strings.Trim(strings.TrimSpace(key), "#*-_ \t")) {
		case "verdict", "status":
		default:
			continue
		}
		// The first whitespace-delimited token of the value, not the first
		// space-delimited one: a tab-separated value such as HOLD\tblocked is
		// still one verdict word.
		fields := strings.Fields(strings.TrimLeft(rest, "*_ \t"))
		if len(fields) == 0 {
			return true
		}
		switch strings.ToUpper(strings.Trim(fields[0], "*_.,;:!")) {
		case "HOLD", "FAIL", "FAILED", "BROKEN":
			return false
		}
		return true
	}
	return true
}

func (a *app) cmdFriendSync(args []string, stdout, stderr io.Writer) int {
	const name = "friend sync"
	fs, c := a.verbSetup(name)
	pg := fs.String("pg", "", "the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it")
	root := fs.String("root", "", "the directory the friends' working directories are under, <root>/<friend>-working (else HOME); each is read for her jobs and never written")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	names, err := a.friends(ctx, *pg)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was changed\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config friend list"))
		return exitCannotRead
	}
	if len(names) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no friend row, and syncing to none would take every friend off; is this the fleet's config? run: nova-config friend list; nothing was changed\n", prog, name)
		return exitCannotRead
	}
	if *root == "" {
		*root = a.getenv("HOME")
	}
	if *root == "" {
		return refuse(stderr, name, "wants --root <dir>, the directory the friends' working directories are under (HOME is not set)")
	}
	specs := make([]store.FriendSpec, 0, len(names))
	jobs := 0
	for _, n := range names {
		if !sprint.ValidID(n) {
			fmt.Fprintf(stderr, "%s %s: a friend name wants letters, digits, _ and -: %s; fix the friend row in nova-config; nothing was changed\n", prog, name, oneline.Escape(n))
			return 1
		}
		dir := filepath.Join(*root, n+"-working")
		js, err := friendJobs(dir)
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: the working directory of %s cannot be read: %s; nothing was changed; run: ls -la %s\n", prog, name, n, oneline.Escape(err.Error()), oneline.Escape(dir))
			return 1
		}
		jobs += len(js)
		specs = append(specs, store.FriendSpec{Name: n, Width: friendWidth, Jobs: js})
	}
	added, removed, updated, err := st.SyncFriends(ctx, specs)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	line := fmt.Sprintf("FRIEND-SYNC OK added=%s removed=%s updated=%s friends=%d jobs=%d", orDashStr(strings.Join(added, ","), "-"), orDashStr(strings.Join(removed, ","), "-"), orDashStr(strings.Join(updated, ","), "-"), len(names), jobs)
	if len(added)+len(removed)+len(updated) == 0 {
		line += ": nothing to do, the friends table already matches the config and the directories"
	}
	sayOK(stdout, c.json, name, line, map[string]any{"added": orEmpty(added), "removed": orEmpty(removed), "updated": orEmpty(updated), "friends": len(names), "jobs": jobs})
	return 0
}

func (a *app) cmdFriendBeat(args []string, stdout, stderr io.Writer) int {
	const name = "friend beat"
	fs, c := a.verbSetup(name)
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	c.orActor(friend)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	b, err := st.FriendBeat(context.Background(), friend)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, name, "FRIEND-BEAT OK "+friend+" at="+b.At.Format(time.RFC3339), map[string]any{"friend": friend, "at": b.At})
	return 0
}

// cmdFriendHold is friend down (held) and friend up (the hold released).
func (a *app) cmdFriendHold(held bool, args []string, stdout, stderr io.Writer) int {
	name := map[bool]string{true: "friend down", false: "friend up"}[held]
	fs, c := a.verbSetup(name)
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if err := st.SetFriendHeld(context.Background(), friend, held, c.actor); err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, name, token(name)+" OK "+friend+" held="+fmt.Sprint(held), map[string]any{"friend": friend, "held": held})
	return 0
}

// oneFriend is the one friend a verb names, or its refusal.
func oneFriend(verbName string, fs flagSet, args []string, stderr io.Writer) (string, int) {
	pos, err := parse(fs, args)
	if err != nil {
		return "", refuse(stderr, verbName, err.Error())
	}
	if len(pos) != 1 {
		return "", refuse(stderr, verbName, "wants one friend, a friend row of nova-config")
	}
	if !sprint.ValidID(pos[0]) {
		return "", refuse(stderr, verbName, "a friend name wants letters, digits, _ and -: "+pos[0])
	}
	return pos[0], 0
}

// orEmpty is the list, or an empty one in place of nil (JSON [] rather than null).
func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
