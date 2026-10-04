package sprint

import (
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"maps"
	"strings"
	"time"
)

// PinnedCard binds a receipt to the exact accepted work, not a moving branch
// (docs/SPEC-SPRINT.md section 7; dev-landing contract, StageReceipt).
type PinnedCard struct {
	ID           string `json:"id"`
	Attempt      string `json:"attempt"`
	Head         string `json:"head"`
	ResolvedHead string `json:"resolved_head"`
}

// StageReceipt proves a checked work-branch integration; it never lands work.
type StageReceipt struct {
	Epoch                             uint64
	Repo, Base, Tip, CITip, CIReceipt string
	Entries                           []PinnedCard
}

// DevReceipt is the verified ancestry and exact-tip CI evidence produced by
// the promotion path. The store holds its epoch and pinned identities; git
// ancestry and GitHub review/queue verification occur before submission.
type DevReceipt struct {
	Epoch                                                              uint64
	Repo, Branch, Tip, CandidateTip, CITip, CIReceipt, Review, BatchID string
	VerifiedAt                                                         time.Time
	Entries                                                            []PinnedCard
}

func fullCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

// receiptPins holds every entry to the accepted head and attempt, atomically
// (docs/SPEC-SPRINT.md section 7, pinned development landing).
func receiptPins(s *Snapshot, entries []PinnedCard, batch []*Card) string {
	if len(entries) != len(batch) {
		return "receipt entries do not equal the selected batch; run: nova-sprint promote --dry-run"
	}
	seen := map[string]PinnedCard{}
	for _, e := range entries {
		if _, duplicate := seen[e.ID]; duplicate {
			return "duplicate receipt pin; run: nova-sprint promote --dry-run"
		}
		seen[e.ID] = e
	}
	for _, c := range batch {
		e, ok := seen[c.ID]
		pr := s.Work.Placed(c.ID)
		if !ok || pr == nil || pr.Col != Merging || e.Head != pr.F("head") || e.Attempt != pr.F("attempt") || !validPinnedHead(e) {
			return "receipt does not pin the current merging head and attempt of " + c.ID + "; run: nova-sprint promote --dry-run"
		}
	}
	return ""
}

// stageBatch retains both lifecycle and queue membership. Same receipt replay
// sets the same fields and satisfies no waiting dependency or sentinel.
func stageBatch(s *Snapshot, r MergeReq, batch []*Card) Plan {
	var p Plan
	p.on(s)
	v := r.Stage
	if r.Dev != nil || v.Epoch != s.Epoch || strings.TrimSpace(v.Repo) == "" || v.Base == "" || v.Base == "dev" || !fullCommit(v.Tip) || v.CITip != v.Tip || strings.TrimSpace(v.CIReceipt) == "" {
		p.refuse(r.Stream, "staging needs this epoch, a work branch, exact-tip green CI and full commit; run: nova-sprint land --check <command>")
		return p
	}
	if why := receiptPins(s, v.Entries, batch); why != "" {
		p.refuse(r.Stream, why)
		return p
	}
	for _, c := range batch {
		var e PinnedCard
		for _, pin := range v.Entries {
			if pin.ID == c.ID {
				e = pin
				break
			}
		}
		pr := s.Work.Placed(c.ID)
		at := stamp(s.Now)
		if pr.F("staged_tip") == v.Tip && pr.F("staged_head_pin") == e.Head && pr.F("staged_attempt") == e.Attempt && pr.F("staged_returns") == pr.F("returns") {
			at = pr.F("staged_at")
		}
		set := map[string]string{"staged_repo": v.Repo, "staged_base": v.Base, "staged_tip": v.Tip, "staged_head": resolvedPin(e), "staged_head_pin": e.Head, "staged_attempt": e.Attempt, "staged_at": at, "staged_ci": v.CIReceipt, "staged_ci_tip": v.CITip, "staged_epoch": fmt.Sprint(v.Epoch), "staged_returns": pr.F("returns")}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: r.Stream, Changes: []Change{change(Work, setEntry(pr, set)), change(Merge, setEntry(c, set))}, Moved: c.ID + " merging -> merging (staged on " + v.Base + ")"})
	}
	p.Notes = append(p.Notes, happened(NBatchStaged, r.Stream, s.Now))
	return Lawful(p)
}

// validateDevReceipt refuses bare merge facts: a work-branch push is not a
// development landing (docs/SPEC-SPRINT.md section 7).
func validateDevReceipt(s *Snapshot, r MergeReq, batch []*Card) string {
	v := r.Dev
	if v == nil {
		return "no verified development receipt: a work-branch push is staging; run: nova-sprint promote --dry-run"
	}
	if v.Epoch != s.Epoch || v.Branch != "dev" || v.Repo == "" || !fullCommit(v.Tip) || !fullCommit(v.CandidateTip) || v.CITip != v.CandidateTip || v.CIReceipt == "" || v.Review == "" || v.BatchID == "" || v.VerifiedAt.IsZero() {
		return "development receipt needs this epoch, dev target, full verified tips, exact-tip CI and reviewed batch; run: nova-sprint promote --dry-run"
	}
	queue := s.Merge.Cell(r.Stream, Queued)
	if len(queue) < len(batch) {
		return "development receipt is not an ordered queue prefix; run: nova-sprint promote --dry-run"
	}
	for i, c := range batch {
		if queue[i].ID != c.ID || i >= len(v.Entries) || v.Entries[i].ID != c.ID {
			return "development receipt is not an ordered queue prefix; run: nova-sprint promote --dry-run"
		}
	}
	for _, c := range batch {
		if pr := s.Work.Placed(c.ID); pr != nil && pr.F("staged_repo") != "" && pr.F("staged_repo") != v.Repo {
			return "development receipt repository differs from staged work; run: nova-sprint promote --dry-run"
		}
	}
	return receiptPins(s, v.Entries, batch)
}

func devFields(v *DevReceipt, id string) map[string]string {
	var pin PinnedCard
	for _, e := range v.Entries {
		if e.ID == id {
			pin = e
			break
		}
	}
	return map[string]string{"dev_repo": v.Repo, "dev_branch": v.Branch, "dev_tip": v.Tip, "dev_head": resolvedPin(pin), "dev_head_pin": pin.Head, "dev_attempt": pin.Attempt, "dev_verified_at": stamp(v.VerifiedAt), "dev_review": v.Review, "dev_candidate_tip": v.CandidateTip, "dev_ci_tip": v.CITip, "dev_ci": v.CIReceipt, "dev_batch": v.BatchID, "dev_epoch": fmt.Sprint(v.Epoch)}
}

// Delivered is dependency proof at the current epoch. Sentinel release is a
// coordinator decision; ordinary work needs the exact verified dev receipt.
// A legacy landed column alone never releases dependent work (Sprint §7).
func Delivered(s *Snapshot, id string) bool {
	c := s.Work.Placed(id)
	if c == nil || c.Col != Landed {
		return false
	}
	if IsSentinel(c) {
		return c.F("released") != ""
	}
	if c.F("dev_branch") != "dev" || c.F("dev_repo") == "" || c.F("dev_head_pin") != c.F("head") || c.F("dev_attempt") != c.F("attempt") || c.F("dev_epoch") != fmt.Sprint(s.Epoch) || !validPinnedHead(PinnedCard{Head: c.F("dev_head_pin"), ResolvedHead: c.F("dev_head")}) || !fullCommit(c.F("dev_head")) || !fullCommit(c.F("dev_tip")) || !fullCommit(c.F("dev_candidate_tip")) || c.F("dev_ci_tip") != c.F("dev_candidate_tip") || c.F("dev_ci") == "" || c.F("dev_review") == "" || c.F("dev_batch") == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339, c.F("dev_verified_at"))
	return err == nil
}

// landedEntryVerified holds every ordinary final move to its resulting proof,
// including composed plans that do not call MergeStep (Sprint §7).
func landedEntryVerified(s *Snapshot, e ntable.BatchMemberEntry) bool {
	if s == nil {
		return false
	}
	pr := s.Work.Placed(e.ID)
	if pr == nil {
		return false
	}
	proof := *pr
	proof.Col = Landed
	proof.Fields = maps.Clone(pr.Fields)
	for k, v := range e.Set {
		proof.Fields[k] = v
	}
	shadow := *s
	shadow.Work = NewTable(Work)
	shadow.Work.Put(&proof)
	return Delivered(&shadow, e.ID)
}

// resolvedPin preserves an accepted abbreviated head as a pin while its proof
// carries the unambiguous full Git commit (existing Land abbreviated heads).
func resolvedPin(e PinnedCard) string {
	if e.ResolvedHead != "" {
		return e.ResolvedHead
	}
	return e.Head
}
func validPinnedHead(e PinnedCard) bool {
	full := resolvedPin(e)
	if !fullCommit(full) || len(e.Head) < 7 || len(e.Head) > 40 {
		return false
	}
	return strings.HasPrefix(full, e.Head)
}
