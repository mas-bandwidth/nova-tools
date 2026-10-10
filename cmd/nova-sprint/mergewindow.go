package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// THE LANDER'S PAUSE (docs/SPEC-SPRINT.md section 7, the lander's pause): merge-window open
// pauses every landing for its duration with its reason shown, and land pauses a batch while
// the merge queue of the branch it lands onto holds a group (sprint.LandPause). The queue is
// the forge's, asked through the app's mergeQueue: gh for a repository on GitHub, nothing for any
// other (a path, a bare clone, another forge has no such queue), each answer kept for
// mergeQueueKeep so the land loop's rounds do not ask the forge every round; a test gives a
// fake and asks no forge.

// cmdMergeWindowOpen opens the merge window (sprint.MergeWindowOpen): landing pauses from now
// for --for, with --reason shown on every batch it pauses.
func (a *app) cmdMergeWindowOpen(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("merge-window open")
	dur := fs.String("for", "", "how long landing pauses, from now: a duration above zero (10m, 1h); a window opened again replaces the one open")
	reason := fs.String("reason", "", "why landing pauses, shown on every batch the window pauses (required)")
	dry := fs.Bool("dry-run", false, "check --for and --reason, say how long landing would pause, and write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "merge-window open", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "merge-window open", "takes no positional words: --for <duration> --reason <text>")
	}
	if *dry {
		d, err := time.ParseDuration(*dur)
		if err != nil || d <= 0 {
			return refuse(stderr, "merge-window open", "--for wants a duration above zero (10m, 1h); found "+strconv.Quote(*dur))
		}
		if strings.TrimSpace(*reason) == "" {
			return refuse(stderr, "merge-window open", "--reason wants why landing pauses, shown on every paused landing")
		}
		fmt.Fprintf(stdout, "MERGE-WINDOW OPEN DRY-RUN for=%s until=%s; nothing was written\n", d, a.now().Add(d).UTC().Format(time.RFC3339))
		return 0
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "merge-window open", err.Error())
	}
	return a.runStep("merge-window open", *c, st, store.MergeWindowStep(sprint.MergeWindowReq{For: *dur, Reason: *reason, Who: c.actor}), stdout, stderr)
}

// pause is why land does not land a batch onto base of repo now, "" when it lands
// (sprint.LandPause over the window s holds): a window whose end cannot be read pauses,
// naming it; a dry run reads the store only and asks no forge.
func (l *lander) pause(ctx context.Context, s *sprint.Snapshot, repo, base string) string {
	w, err := s.MergeWindow()
	if err != nil {
		return "paused: " + oneline.Err(err) + "; run: nova-sprint merge-window open --for <duration> --reason <text>, which writes it again"
	}
	q := l.a.mergeQueue
	if l.dry {
		q = nil
	}
	return sprint.LandPause(ctx, w, l.a.now(), q, repo, base)
}

// mergeQueueKeep is how long a branch's merge queue answer is kept: the land loop asks
// every round (LandEvery) while cards are queued, and the forge's API is rationed.
const mergeQueueKeep = 20 * time.Second

// keptQueue is a sprint.MergeQueue whose answers, errors included, are kept for
// mergeQueueKeep by repository and branch.
type keptQueue struct {
	ask  sprint.MergeQueue
	now  func() time.Time
	mu   sync.Mutex
	kept map[string]keptAnswer
}

type keptAnswer struct {
	at   time.Time
	held bool
	err  error
}

func (k *keptQueue) HoldsGroup(ctx context.Context, repo, branch string) (bool, error) {
	key := repo + "\x00" + branch
	k.mu.Lock()
	a, ok := k.kept[key]
	k.mu.Unlock()
	if ok && k.now().Sub(a.at) < mergeQueueKeep {
		return a.held, a.err
	}
	held, err := k.ask.HoldsGroup(ctx, repo, branch)
	k.mu.Lock()
	k.kept[key] = keptAnswer{at: k.now(), held: held, err: err}
	k.mu.Unlock()
	return held, err
}

// ghMergeQueue asks the merge queue of a branch of a repository on host, a GitHub forge,
// through gh as the caller's gh is authenticated there: one GraphQL query, its entries
// counted (a queued or checking group is an entry), in the caller's environment. A
// repository on any other host, or a path, has no such queue and is never asked.
type ghMergeQueue struct{ host string }

// githubHost is the forge the server's lander asks: GitHub's own.
const githubHost = "github.com"

// mergeQueueQuery counts the entries of a branch's merge queue; a branch with no merge
// queue answers null, which is none.
const mergeQueueQuery = `query($owner: String!, $name: String!, $branch: String!) { repository(owner: $owner, name: $name) { mergeQueue(branch: $branch) { entries(first: 1) { totalCount } } } }`

func (g ghMergeQueue) HoldsGroup(ctx context.Context, repo, branch string) (bool, error) {
	owner, name, ok := forgeRepo(g.host, repo)
	if !ok {
		return false, nil
	}
	cmd, cancel := subproc.Command(ctx, subproc.GH, "gh", "api", "graphql", "--hostname", g.host, "-f", "query="+mergeQueueQuery,
		"-f", "owner="+owner, "-f", "name="+name, "-f", "branch="+branch, "--jq", ".data.repository.mergeQueue.entries.totalCount // 0")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return false, errors.New("gh api graphql: " + strings.TrimSpace(string(ee.Stderr)))
		}
		return false, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return false, errors.New("gh api graphql answered " + strconv.Quote(strings.TrimSpace(string(out))) + ", not a count of entries")
	}
	return n > 0, nil
}

// forgeRepo is the owner and name of the address of a repository on host (https, ssh, or
// the scp-like git@ form, .git or not); ok false for any other address.
func forgeRepo(host, repo string) (owner, name string, ok bool) {
	u := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(repo), "/"), ".git")
	for _, p := range []string{"https://" + host + "/", "http://" + host + "/", "ssh://git@" + host + "/", "git@" + host + ":"} {
		if rest, found := strings.CutPrefix(u, p); found {
			owner, name, ok = strings.Cut(rest, "/")
			return owner, name, ok && owner != "" && name != "" && !strings.Contains(name, "/")
		}
	}
	return "", "", false
}
