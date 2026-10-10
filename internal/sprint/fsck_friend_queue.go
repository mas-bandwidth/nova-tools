package sprint

import (
	"fmt"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The fsck check friend-queue (docs/SPEC-SPRINT.md, "What is always true",
// fsck-friend-queue-agreement-b.w5): the cards the store has dealt to a friend's row are
// the ids her own inbox/QUEUE.json names, and every REPORT.md in her outbox belongs to a
// dealt or finished card. Her files live on her own machine, so her daemon carries the
// queue ids and the outbox ids on its beat (internal/friend/state.go,
// internal/friend/daemon.go), and the check compares them with the store's cards through
// the comparison friend reconcile uses and never a second one (FriendReconcileOf,
// FriendQueueStrays; internal/sprint/friend_reconcile.go). The tick's reconcile repairs
// ordinary drift every tick (friend-reconcile-every-tick-r.w1), so the check reports only a
// disagreement that is the same on the two reconcile passes it is handed: a violation means
// the repair is broken, and the fix it names is `friend reconcile <friend>`. It writes
// nothing.

// FsckFriendQueue is the check's name.
const FsckFriendQueue = "friend-queue"

// The kinds of a friend-queue finding.
const (
	FriendQueueCardKind   = "card"   // a card of her row her own account disagrees on
	FriendQueueStrayKind  = "stray"  // an id of her account that is no card of her row
	FriendQueueReportKind = "report" // an outbox report that belongs to no dealt or finished card
)

// FriendQueueCard is one card the store has dealt to a friend's row: the work card (its id,
// column and deal stamp) and the job directory she was handed it in.
type FriendQueueCard struct {
	Card *Card
	Job  string
}

// FriendQueueReport is what a friend's daemon carries of her own files on one beat: her
// inbox/QUEUE.json's tasks, each state by id (ReadFriendQueue) with the file's last write,
// and the job directories of the REPORT.md files in her outbox.
type FriendQueueReport struct {
	Account FriendAccount
	Outbox  []string
}

// FriendQueueFinding is the check's finding on one disagreement: the friend, what it names
// (a card id, a stray id, or a report's job), the kind, why the store's cards and her own
// account do not agree, and the line that fixes it.
type FriendQueueFinding struct {
	Check  string `json:"check"`
	Friend string `json:"friend"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Why    string `json:"why,omitempty"`
	Fix    string `json:"fix"`
}

// Line is the finding as fsck prints it: VIOLATION, the check and friend, what it names and
// how they disagree.
func (f FriendQueueFinding) Line() string {
	return fmt.Sprintf("FSCK VIOLATION check=%s friend=%s kind=%s name=%s %s",
		oneline.Field(f.Check), oneline.Field(f.Friend), oneline.Field(f.Kind), oneline.Field(f.Name), oneline.Escape(f.Why))
}

// friendQueuePass is one reconcile pass: every card, stray id and outbox report on which
// the cards the store has dealt to the friend's row and her own account disagree. A card
// she has accounted for (her queue says queued or working, or it was dealt since her
// account was written) agrees, and is not a finding; a card reconcile would collect or
// return, an id of her account that is no card or job of her row, and an outbox report that
// belongs to no dealt or finished card each are one, keyed by kind and name so two passes
// compare.
func friendQueuePass(friend string, cards []FriendQueueCard, rep FriendQueueReport) []FriendQueueFinding {
	held := make([]string, 0, 2*len(cards))
	for _, c := range cards {
		if c.Card != nil {
			held = append(held, c.Card.ID)
		}
		held = append(held, c.Job)
	}
	finished := map[string]bool{}
	for id, state := range rep.Account.Tasks {
		if state == FriendTaskDone {
			finished[id] = true
		}
	}
	var out []FriendQueueFinding
	for _, c := range cards {
		if c.Card == nil {
			continue
		}
		reported := slices.Contains(rep.Outbox, c.Job)
		if action, why := FriendReconcileOf(c.Card, c.Job, reported, rep.Account); action != ReconcileKeep {
			out = append(out, FriendQueueFinding{Check: FsckFriendQueue, Friend: friend,
				Name: c.Card.ID, Kind: FriendQueueCardKind, Why: why, Fix: "friend reconcile " + friend})
		}
	}
	for _, id := range FriendQueueStrays(rep.Account.Tasks, held) {
		out = append(out, FriendQueueFinding{Check: FsckFriendQueue, Friend: friend, Name: id,
			Kind: FriendQueueStrayKind, Why: "her QUEUE.json names " + id + ", which is no card or job of her row", Fix: "friend reconcile " + friend})
	}
	for _, job := range rep.Outbox {
		if slices.Contains(held, job) || finished[job] {
			continue
		}
		out = append(out, FriendQueueFinding{Check: FsckFriendQueue, Friend: friend, Name: job,
			Kind: FriendQueueReportKind, Why: "outbox/" + job + "/REPORT.md belongs to no dealt or finished card", Fix: "friend reconcile " + friend})
	}
	return out
}

// FsckFriendQueue is the check for one friend: the findings that are the same on the two
// reconcile passes before and after, each the account and outbox her daemon carried on a
// beat. A finding the first pass has and the second does not is dropped, so the ordinary
// drift the tick's reconcile repairs is no violation; one on both is one violation per
// card, because the mechanical repair has had two passes and not settled it. Every finding
// names `friend reconcile <friend>` as the fix, and the check runs none.
func FsckFriendQueue(friend string, cards []FriendQueueCard, before, after FriendQueueReport) []FriendQueueFinding {
	first := friendQueuePass(friend, cards, before)
	same := map[string]bool{}
	for _, f := range friendQueuePass(friend, cards, after) {
		same[f.Kind+"\x00"+f.Name] = true
	}
	var out []FriendQueueFinding
	for _, f := range first {
		if same[f.Kind+"\x00"+f.Name] {
			out = append(out, f)
		}
	}
	return out
}
