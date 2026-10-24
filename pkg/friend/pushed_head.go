package friend

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The pushed head (nova-sprint #600, FieldProgressHead). A friend pushes her card's branch
// herself, from the staged checkout (JOB.md: git push -u origin <branch>); a push that git
// took writes origin's remote-tracking ref of the branch in the checkout's repository. The
// daemon reads that ref (no network is asked: PushedHead's reading) every PushedHeadEvery,
// and when it names a commit the daemon has not yet said for the job, says it with
// `progress --as friend.<name> <card>@<gen> --epoch <n> [--lane <l>] --head <sha>`
// (ProgressArgv), so an attempt redealt unfinished stages the next attempt from the pushed
// head, never again from BASE. The commit the job was staged at (JOB.md's) is no push of
// this attempt and is never said. It is best effort: a report the server refuses is said on
// the record once and not sent again for that head; one not answered is said and sent again
// after ProgressEvery. Neither touches the card.

// PushedHeadEvery is how often the daemon reads the pushed heads of the working cards on her
// row: a push is minutes of work apart, and the read is a file or two per card.
const PushedHeadEvery = 30 * time.Second

// pushedHeads is the loop's memory of the heads it said: per job, the head the server took
// or refused, and when one not answered was last tried.
type pushedHeads struct {
	at    time.Time
	said  map[string]string
	tried map[string]time.Time
}

// stagedAt is the commit JOB.md says the job was staged at (JobText), "" when it says none.
var stagedAt = regexp.MustCompile(`, ([0-9a-f]{40}), on branch `)

// StagedSha is the commit job's JOB.md under dir says its checkout was staged at, "" when
// there is no JOB.md or it names none.
func StagedSha(dir, job string) string {
	raw, err := os.ReadFile(filepath.Join(JobDir(dir, job), JobFile))
	if err != nil {
		return ""
	}
	if m := stagedAt.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

// jobPushedHead is origin's remote-tracking ref of branch for job under dir: in the staged
// checkout (jobs/<job>/repo) first, else any clone under the job (PushedHead's walk).
func jobPushedHead(dir, job string, c Card, branch string) string {
	if branch == "" {
		head, _ := PushedHead(dir, c)
		return head
	}
	if head := remoteRef(gitDir(filepath.Join(JobDir(dir, job), "repo", ".git")), "refs/remotes/origin/"+branch); head != "" {
		return head
	}
	head, briefBranch := PushedHead(dir, c)
	if briefBranch != branch {
		return ""
	}
	return head
}

// reportPushedHeads says the pushed head of every work card working on her row whose head
// moved since the daemon last said it (the comment above). It runs once each PushedHeadEvery.
func (l *loop) reportPushedHeads(now time.Time) {
	d := l.d
	if d.Progress == nil {
		return
	}
	ph := &l.pushed
	if !ph.at.IsZero() && now.Sub(ph.at) < PushedHeadEvery {
		return
	}
	ph.at = now
	if ph.said == nil {
		ph.said, ph.tried = map[string]string{}, map[string]time.Time{}
	}
	at := now.UTC().Format(time.RFC3339)
	live := map[string]bool{}
	for _, h := range d.heldCards {
		if h.Col != "working" || h.Kind == "read" || h.Job == "" || !validJob(h.Job) {
			continue
		}
		job := h.Job
		live[job] = true
		c := Card{ID: h.Card, Brief: filepath.Join(d.Dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(d.Dir, "outbox", job), Lane: h.Lane}
		if h.Lane > 0 {
			// a lane's card is reported at the epoch and generation the server says it holds,
			// any other by its job's name, as the outbox finishes it (outbox.go)
			c.ServerEpoch, c.ServerGen = strconv.FormatUint(h.Epoch, 10), h.Gen
		}
		head := jobPushedHead(d.Dir, job, c, h.Branch)
		if !fullSha.MatchString(head) || head == ph.said[job] || head == StagedSha(d.Dir, job) {
			continue
		}
		if t, ok := ph.tried[job+" "+head]; ok && now.Sub(t) < ProgressEvery {
			continue
		}
		c.Head = head
		err := d.Progress(l.ctx, []Card{c})
		var refused *Refused
		switch {
		case err == nil:
			ph.said[job] = head
			delete(ph.tried, job+" "+head)
			d.Record(fmt.Sprintf("%s pushed: card %s head %s on %s said to the server", at, h.Card, head, cmp.Or(h.Branch, "its branch")))
		case errors.As(err, &refused):
			ph.said[job] = head // a refusal is not sent again for this head
			d.Record(fmt.Sprintf("%s pushed: card %s head %s refused by the server: %s", at, h.Card, head, oneLine(refused.Why, 300)))
		default:
			ph.tried[job+" "+head] = now
			d.Record(fmt.Sprintf("%s pushed: card %s head %s not said: %s; tried again in %s", at, h.Card, head, oneLine(err.Error(), 300), ProgressEvery))
		}
	}
	for job := range ph.said {
		if !live[job] {
			delete(ph.said, job)
		}
	}
	for key := range ph.tried {
		if job := key[:max(strings.LastIndex(key, " "), 0)]; !live[job] {
			delete(ph.tried, key)
		}
	}
}
