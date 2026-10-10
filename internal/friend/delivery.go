package friend

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// Delivery is the daemon's duty, in order (the card deliver-is-the-daemons-duty-in-order; what
// the coordinator's stopgap deliver.py learned on the night of 2026-10-05). Every card on her
// row is delivered, whatever its WHO line prefers: the pin is a preference the deal weighed,
// and the deal is the decision, so a card placed on her row is hers to run. A card whose job
// the daemon stages is staged before its brief is written, because a runner starts a lane
// within seconds of BRIEF.md and found no checkout ("the staged checkout is missing", a dozen
// HOLDs). The checkout is cloned from a full local mirror (a blob-less partial one breaks the
// clone: "pack has unresolved deltas"). A friend whose runner stages its own jobs is handed the
// brief alone (Stage nil), and a job directory another hand made is never staged over
// (StartedElsewhere). The daemon runs it each loop (inboxStep, stageStep), and the
// coordinator's `nova-sprint deliver <friend>` runs the same One by hand. The order is modelled
// in internal/friend/tla/DeliverOrder.tla (MCDeliverOrder*: no lane meets a brief with no
// checkout, no job staged by two hands, every card delivered; two reversed witnesses).

// The ends of one card's delivery, as the DELIVER lines say them.
const (
	DeliverStaged  = "staged"  // its job staged now, then its brief written
	DeliverBrief   = "brief"   // its brief written, nothing staged now (a read, no REPO, staged before, or a runner that stages its own)
	DeliverSkipped = "skipped" // nothing written; Why says why
)

// Delivered is one card's delivery: what was done (DeliverStaged, DeliverBrief or
// DeliverSkipped), why when skipped, the commit staged when one was, and Err, the stage's
// failure (NotStageable, StartedElsewhere or another) when that is why it was skipped.
type Delivered struct {
	Card, Job, What, Why, Sha string
	Err                       error
}

// Line is the DELIVER line: DELIVER <job> staged|brief|skipped [<why>].
func (o Delivered) Line() string {
	line := "DELIVER " + cmp.Or(o.Job, "-") + " " + o.What
	if o.Why != "" {
		line += " " + oneLine(o.Why, 400)
	}
	return line
}

// Delivery delivers the cards on one friend's row into her working directory Dir. Stage stages
// a card's job (a Stager's Stage); nil is a friend whose runner stages its own jobs, handed the
// brief alone. A DryRun delivery answers what each card would have and writes nothing.
type Delivery struct {
	Dir    string
	Stage  func(ctx context.Context, p Packet) (string, error)
	DryRun bool // say what each card would have, and stage and write nothing
}

// Owed says h's job is staged here before its brief is written: a stage is set, h is a work
// card naming its repository, and its JOB.md is not there.
func (dl Delivery) Owed(h HeldCard) bool {
	if dl.Stage == nil {
		return false
	}
	_, ok := PacketOf(h)
	return ok && !Staged(dl.Dir, h.Job)
}

// One delivers h in order: its job staged when it is owed (Owed), then its BRIEF.md written
// whole, never over a file there. A stage that fails writes no brief, so no runner meets a
// brief with no checkout.
func (dl Delivery) One(ctx context.Context, h HeldCard) Delivered {
	h.Brief = deliveryBrief(h)
	o := Delivered{Card: h.Card, Job: h.Job, What: DeliverSkipped}
	in, why := inboxDir(dl.Dir, h.Job)
	if why != "" {
		o.Why = why
		return o
	}
	brief := filepath.Join(in, "BRIEF.md")
	there := exists(brief)
	if there && !dl.Owed(h) {
		o.Why = "delivered already: inbox/" + h.Job + "/BRIEF.md is there"
		return o
	}
	if !there && h.Brief == "" {
		o.Why = cmp.Or(h.Why, "the server sent no brief")
		return o
	}
	if dl.DryRun {
		o.What, o.Why = DeliverBrief, "dry run: nothing written"
		if dl.Owed(h) {
			o.What = DeliverStaged
		} else if there {
			o.What, o.Why = DeliverSkipped, "delivered already: inbox/"+h.Job+"/BRIEF.md is there"
		}
		return o
	}
	staged := false
	if dl.Owed(h) {
		p, _ := PacketOf(h) // Owed read it
		sha, err := dl.Stage(ctx, p)
		if err != nil {
			o.Why, o.Err = "not staged: "+err.Error(), err
			return o
		}
		o.Sha, staged = sha, sha != ""
	}
	if there {
		o.What, o.Why = DeliverStaged, "its brief was there before its job was staged"
		if !staged {
			o.What, o.Why = DeliverSkipped, "delivered already: inbox/"+h.Job+"/BRIEF.md is there"
		}
		return o
	}
	wrote, err := writeBrief(in, deliveryBrief(h))
	switch {
	case err != nil:
		o.Why, o.Err = "BRIEF.md not written: "+err.Error(), err
		return o
	case !wrote:
		o.Why = "delivered already: inbox/" + h.Job + "/BRIEF.md was written by another hand"
		return o
	case staged:
		o.What = DeliverStaged
	default:
		o.What = DeliverBrief
	}
	return o
}

// inboxDir is inbox/<job> under dir, or why a card's job may not be delivered there: a name
// that is no inbox directory, or an inbox/<job> that is a symlink or a file.
func inboxDir(dir, job string) (string, string) {
	if !validJob(job) || filepath.Base(job) != job {
		return "", fmt.Sprintf("its job %q is no inbox directory", job)
	}
	in := filepath.Join(dir, "inbox", job)
	if fi, err := os.Lstat(in); err == nil && !fi.IsDir() {
		return "", "inbox/" + job + " is a symlink or a file, not a directory"
	}
	return in, ""
}

// writeBrief writes in/BRIEF.md whole, never over a file there; wrote is false when one was
// there first.
func writeBrief(in, brief string) (wrote bool, err error) {
	if err := os.MkdirAll(in, 0o755); err != nil {
		return false, err
	}
	err = atomicfile.WriteFile(filepath.Join(in, "BRIEF.md"), []byte(brief), 0o644, atomicfile.NoReplace())
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	return err == nil, err
}

// DeliverRow delivers every card on a row, in the row's order, and answers each delivery.
func (dl Delivery) DeliverRow(ctx context.Context, cards []HeldCard) []Delivered {
	out := make([]Delivered, 0, len(cards))
	for _, h := range cards {
		out = append(out, dl.One(ctx, h))
	}
	return out
}

// deliveryBrief carries the row's current tier on the RESULT contract. A historical
// embedded tier cannot override the packet; briefs with no known packet tier stay intact.
func deliveryBrief(h HeldCard) string {
	brief := ReworkedBrief(h.Brief)
	if !cardhdr.IsRoute(h.Tier) || strings.TrimSpace(brief) == "" {
		return brief
	}
	lines := strings.Split(brief, "\n")
	for i, line := range lines {
		key, value, ok := cardhdr.KeyValue(line)
		if !ok || key != "RESULT" {
			continue
		}
		words := strings.Fields(value)
		for j, word := range words {
			if word != "tier:" {
				continue
			}
			words = append(words[:j], words[min(j+2, len(words)):]...)
			break
		}
		lines[i] = "RESULT: " + strings.Join(words, " ") + " tier: " + h.Tier
		return strings.Join(lines, "\n")
	}
	result := "RESULT: " + h.Card + " sha= tier: " + h.Tier + "\n"
	if strings.HasPrefix(brief, "STATUS:") {
		if i := strings.Index(brief, "\n\n"); i >= 0 {
			return brief[:i+1] + result + brief[i+1:]
		}
	}
	return result + brief
}
