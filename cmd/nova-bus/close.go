// close: every open note dated before an instant receipted at once, to put a backlog down.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdClose is `close --before`, the whole backlog at once: every open note dated before the
// stamp is closed by one receipt note each, batched in one commit. It is the other end of
// the INBOX OPEN large-list remedy line -- the normal answer is reply or receipt per note,
// and `close --before` is the explicit opt-in bulk cutoff: `--advance` draws a line past the
// history and leaves the notes behind it; `close` answers them, each with a Re line that
// removes it from the reader's open list for good.
func cmdClose(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("close")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	beforeFlag := f.fs.String("before", "", "every open note addressed to you and dated before this RFC 3339 instant is closed by a receipt (required)")
	dryRun := f.fs.Bool("dry-run", false, "report what would be closed and write nothing")
	remote := f.fs.String("remote", "", "the git remote to push to (required to write)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required to write)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "before": beforeFlag}) {
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	before, err := time.Parse(time.RFC3339, *beforeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "CLOSE REFUSED: --before %q is not an RFC 3339 instant; refusing to guess; run: nova-bus close -h\n", *beforeFlag)
		return 2
	}
	if !*dryRun {
		if !f.gitArgs(*remote, *branch, stderr) {
			return 2
		}
		if strings.TrimSpace(*remote) == "" || strings.TrimSpace(*branch) == "" {
			fmt.Fprint(stderr, "CLOSE REFUSED: writing onto the bus needs --remote and --branch; refusing to guess (or pass --dry-run); run: nova-bus close -h\n")
			return 2
		}
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "CLOSE REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus close -h"))
		return 2
	}
	t, ok := openBus("close", *busDir, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "CLOSE REFUSED: --as %q names no one on this bus (known: %s); run: nova-bus close -h\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
		return 2
	}
	plan, err := bus.PlanClose(t, me, before, now)
	if err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(me.Name), oneline.Err(err))
		return 1
	}
	// closed= COUNTS NOTES, not receipts. One receipt closes every note one
	// sender left before the stamp, so len(plan.Prepared) is the number of lanes answered
	// and would be a different, smaller and quite surprising number here.
	if *dryRun {
		fmt.Fprintf(stdout, "CLOSE OK closed=%d kept=%d commit=- dry_run=true\n", plan.Closed, plan.Kept)
		return 0
	}
	if len(plan.Prepared) == 0 {
		fmt.Fprintf(stdout, "CLOSE OK closed=0 kept=%d commit=-\n", plan.Kept)
		return 0
	}
	if err := checkoutReady(*busDir, *branch, nil); err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL: %s\n", oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(*busDir, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "CLOSE REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus close -h"))
		return 1
	}
	// A PARTIAL CLOSE COMPLETES OR LEAVES NOTHING BEHIND. The collision this fix
	// removes used to stop the loop at its second Save, with the first receipt written into
	// the working tree, no commit, and the cursor where it started -- so the next run met a
	// file it had not committed and the lane had to be cleaned by hand. Whatever stops the
	// loop now, the files this run wrote go back out of the tree before it returns.
	written := make([]string, 0, len(plan.Prepared))
	undo := func() {
		for i := len(written) - 1; i >= 0; i-- {
			if err := os.Remove(filepath.Join(*busDir, filepath.FromSlash(written[i]))); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(stderr, "CLOSE NOTE %s was written and could not be taken back: %s\n", oneline.Escape(written[i]), oneline.Err(err))
			}
		}
	}
	paths := make([]string, 0, len(plan.Prepared)+1)
	for i := range plan.Prepared {
		p := &plan.Prepared[i]
		if err := p.Save(*busDir); err != nil {
			undo()
			fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(p.Path), oneline.Err(err))
			return 1
		}
		written = append(written, p.Path)
		if err := p.AppendIndex(*busDir); err != nil {
			undo()
			fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(p.Path), oneline.Err(err))
			return 1
		}
		paths = append(paths, p.Path)
	}
	paths = append(paths, bus.IndexPath(me.Lane))
	wroteAttrs, err := bus.EnsureMergeAttributes(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL %s: %s\n", oneline.Escape(bus.AttributesName), oneline.Err(err))
		return 1
	}
	if wroteAttrs {
		paths = append(paths, bus.AttributesName)
	}
	res, err := commit(*busDir, me, paths,
		bus.WithTrailer(plan.Message(me), bus.TrailerClose),
		*remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "CLOSE FAIL: %s\n", oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	sha8 := res.Commit
	if len(sha8) > 8 {
		sha8 = sha8[:8]
	}
	// receipts= is the one number that changed shape: closed= counts
	// notes, as it always did and as the dry run above already did, and receipts= says how
	// many notes it took to close them -- one per sender lane. A reader who wants to know
	// whether a close collapsed 2964 files into a handful reads it here.
	fmt.Fprintf(stdout, "CLOSE OK closed=%d kept=%d receipts=%d commit=%s\n", plan.Closed, plan.Kept, len(plan.Prepared), oneline.Field(sha8))
	return 0
}
