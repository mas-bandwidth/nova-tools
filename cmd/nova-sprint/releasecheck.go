package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// release check is a read (docs/SPEC-SPRINT.md, the release check; docs/SPEC-RELEASE.md,
// "release check"): it runs the registry of release checks over the store's log and writes
// nothing. The class is kept here, beside the verb.
func init() {
	verbClasses["release check"] = classRead
	verbEffect["release check"] = "inspection: reads the store's log and the sprint's settings, writes nothing"
	verbExit["release check"] = "exit codes: 0 every check passed (RELEASE OK), 1 a check failed (RELEASE NOT READY; each RELEASE CHECK line names what to look at), 2 usage or a store that did not answer"
}

// releaseCheckID is the sub-verb's word: no card is added with it as its id, so
// `release check` and `release <id>` never name the same thing.
const releaseCheckID = "check"

// reservedCardID is why a card may not be added with one of these ids: "" is may.
func reservedCardID(ids ...string) string {
	for _, id := range ids {
		if id == releaseCheckID {
			return "a card cannot be called check: release check is the release gate's verb, and release <id> would name both; give the card another id"
		}
	}
	return ""
}

// storeRelease is the release check's facts, read from the store once.
type storeRelease struct {
	now         time.Time
	lines       []sprint.Line
	dealtMax    time.Duration
	accept      sprint.Acceptance
	mergeWindow time.Duration
	mergeP90    time.Duration
}

func (s storeRelease) Now() time.Time                { return s.now }
func (s storeRelease) Log() []sprint.Line            { return s.lines }
func (s storeRelease) DealtMax() time.Duration       { return s.dealtMax }
func (s storeRelease) Acceptance() sprint.Acceptance { return s.accept }
func (s storeRelease) MergeWindow() time.Duration    { return s.mergeWindow }
func (s storeRelease) MergeP90() time.Duration       { return s.mergeP90 }

// cmdReleaseCheck runs the release checks and prints one RELEASE CHECK line per check, then
// RELEASE OK checks=<n> or RELEASE NOT READY failed=<n>; --json prints the one report
// object. Exit 0 every check passed, 1 one failed, 2 usage or a store that did not answer.
func (a *app) cmdReleaseCheck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("release check")
	streams := fs.String("streams", "", "only the log of the streams this glob names (path.Match over the stream's name; default every stream)")
	window := fs.Duration("window", sprint.MergeQueueWindowDefault, "how far back a card's merging counts, for merge-queue-p90 (default 24h)")
	mergeP90 := fs.Duration("merge-p90", sprint.MergeQueueP90Default, "the bar on merge-queue-p90: the p90 of the time cards spent merging (default 30m)")
	var names stringList
	fs.Var(&names, "check", "run only this check, by name (repeat for more; default every check): "+strings.Join(sprint.ReleaseCheckNames(), ", "))
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "release check", argErr("takes no words ", err, pos...))
	}
	for _, n := range names {
		if !slicesHas(sprint.ReleaseCheckNames(), n) {
			return refuse(stderr, "release check", fmt.Sprintf("no release check named %s; the checks are %s", oneline.Escape(n), strings.Join(sprint.ReleaseCheckNames(), ", ")))
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "release check", err.Error())
	}
	ctx := context.Background()
	es, err := st.EpochNow(ctx)
	if err != nil {
		return a.readFailed("release check", err, stderr)
	}
	lines, err := st.Log(ctx)
	if err != nil {
		return a.readFailed("release check", err, stderr)
	}
	now := a.now()
	from := now.Add(-sprint.StuckWindow)
	if lines, err = sprint.ReleaseStreamLines(lines, *streams); err != nil {
		return refuse(stderr, "release check", err.Error())
	}
	s, err := st.Load(ctx, store.All, nil)
	if err != nil {
		return a.readFailed("release check", err, stderr)
	}
	rep, err := sprint.RunReleaseChecks(storeRelease{now: now, lines: lines, dealtMax: s.DealtMax(), accept: sprint.AcceptanceOf(s, lines, *streams), mergeWindow: *window, mergeP90: *mergeP90}, names)
	if err != nil {
		return refuse(stderr, "release check", err.Error())
	}
	if es.N > 0 && !es.Cleared.IsZero() && es.Cleared.After(from) {
		for i, r := range rep.Results {
			if r.Name == sprint.CheckNoStuckFriend && r.OK {
				rep.Results[i] = sprint.ReleaseResult{
					Name:     sprint.CheckNoStuckFriend,
					OK:       false,
					Evidence: fmt.Sprintf("coverage incomplete: sprint cleared at %s (%s ago), less than %s window", oneline.Escape(es.Cleared.UTC().Format(time.RFC3339)), now.Sub(es.Cleared).Truncate(time.Second), sprint.StuckWindow),
				}
				rep.Failed++
				rep.Ready = false
				rep.Summary = fmt.Sprintf("RELEASE NOT READY failed=%d", rep.Failed)
			}
		}
	}
	if c.json {
		b, _ := json.Marshal(rep)
		fmt.Fprintln(stdout, string(b))
		return rep.ExitCode()
	}
	for _, r := range rep.Results {
		fmt.Fprintln(stdout, oneline.Escape(r.Line()))
	}
	fmt.Fprintln(stdout, rep.Summary)
	return rep.ExitCode()
}

func slicesHas(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
