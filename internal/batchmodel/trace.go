// Package batchmodel holds the pure evidence and rendering half of a batch
// receipt replay. A store adapter supplies independent before/after snapshots;
// TLC, rather than this package, decides whether Apply(request) permits them.
package batchmodel

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// Member is an independently read member record. A missing map entry in Fields
// means absent; a present entry containing "" is a present empty value.
type Member struct {
	Exists   bool
	Revision string
	Place    *Place
	Fields   map[string]string
}

// Place is an owned cell and its Redis score. Score is the exact finite score
// spelling read from Redis, not a floating-point approximation.
type Place struct{ Row, Column, Score string }

// Snapshot is a complete captured image plus the projection needed to check a
// batch receipt. Image must include every key in the disposable store, encoded
// by the adapter without lossy value conversion. None of these values come
// from the receipt being checked.
type Snapshot struct {
	Image         map[string][]byte
	TableRevision string
	Epoch         string
	Members       map[string]Member
	Operations    uint64
	Receipts      uint64
	Recorded      *OperationRecord // this request key, read independently
	LastReceipt   *Receipt         // change-stream tail, read independently
}

// OperationRecord is the durable lookup for table+epoch+operation ID.
type OperationRecord struct {
	Table, Epoch, OperationID, Digest, ReceiptID string
	Canonical                                    []byte
}

// Delta is a receipt's complete before/after state for one selected member,
// including guard-only and no-op entries.
type Delta struct {
	ID     string
	Before Member
	After  Member
}

// Receipt is the typed batch receipt after strict wire decoding by an adapter.
// Its identity includes the stream ID. Canonical request bytes are checked
// separately against the durable operation record by the adapter.
type Receipt struct {
	StreamID, OperationID, Digest, Actor, Table, Epoch string
	BeforeRevision, AfterRevision                      string
	Kind                                               string // changed or noop
	SelectedCount, GuardCount, ChangedCount            uint64
	Members                                            []Delta
}

// Request is the exact request sent to one FCALL. Canonical is its actual byte
// sequence, retained even if a digest collision is deliberately injected.
type Request struct {
	Canonical   []byte
	OperationID string
	Digest      string
	Actor       string
	Table       string
	Epoch       string
	Revision    string
	Selected    []string
	// ExpectedRevision distinguishes an omitted member guard (nil value) from
	// an exact raw decimal guard. Every selected ID must have an entry.
	ExpectedRevision map[string]*string
	// Entries is the typed finite model projection of the strict decode of
	// Canonical. The wire adapter must establish that binding before capture.
	Entries []MemberAction
}

// Result distinguishes a new commit, preventive refusal, and an identical
// operation replay. RetryReceipt is the original receipt returned on replay.
type Result string

const (
	Accepted Result = "accepted"
	Refused  Result = "refused"
	Replayed Result = "replayed"
)

// Step retains source evidence and its independently captured model state.
// BeforeModel/AfterModel are rendered into a TLC harness; Apply(q), not a Go
// oracle, must explain the transition. The adapter must capture these states
// from the same snapshots as Before/After, never from Receipt.
type Step struct {
	Index        uint64
	Request      Request
	Result       Result
	Before       Snapshot
	After        Snapshot
	Receipt      *Receipt
	PriorReceipt *Receipt // required for Replayed
	BeforeModel  ModelState
	AfterModel   ModelState
	Action       BatchAction
	// Baseline revisions are captured once after fixture setup. Projection
	// subtracts them without wraparound so model BatchInit starts at zero.
	TableBaseline  string
	MemberBaseline map[string]string
}

// ValidateStep checks the receipt against the two independently captured
// snapshots. It does not decide whether the store transition is allowed by the
// model: the generated TLC harness does that with the real Apply(q) action.
func ValidateStep(s Step) error {
	if len(s.Request.Canonical) == 0 || s.Request.OperationID == "" || s.Request.Digest == "" || s.Request.Actor == "" || s.Request.Table == "" {
		return errors.New("incomplete request identity")
	}
	if s.Before.Image == nil || s.After.Image == nil || s.Before.Members == nil || s.After.Members == nil {
		return errors.New("missing independent store snapshot")
	}
	beforeRev, err := decimal(s.Before.TableRevision)
	if err != nil {
		return fmt.Errorf("before table revision: %w", err)
	}
	afterRev, err := decimal(s.After.TableRevision)
	if err != nil {
		return fmt.Errorf("after table revision: %w", err)
	}
	if _, err := decimal(s.Request.Revision); err != nil {
		return fmt.Errorf("request revision: %w", err)
	}
	if _, err := decimal(s.Request.Epoch); err != nil {
		return fmt.Errorf("request epoch: %w", err)
	}
	seen := map[string]bool{}
	for _, id := range s.Request.Selected {
		if id == "" || seen[id] {
			return fmt.Errorf("duplicate or empty selected member %q", id)
		}
		seen[id] = true
		if _, ok := s.Before.Members[id]; !ok {
			return fmt.Errorf("missing before observation for %q", id)
		}
		if _, ok := s.After.Members[id]; !ok {
			return fmt.Errorf("missing after observation for %q", id)
		}
	}
	if len(seen) == 0 {
		return errors.New("empty selection")
	}
	if len(s.Action.Members) != len(s.Request.Selected) {
		return errors.New("model action selection differs from request")
	}
	if !reflect.DeepEqual(s.Action.Members, s.Request.Entries) {
		return errors.New("model action entries differ from decoded request")
	}
	guardCount := uint64(0)
	for i, m := range s.Action.Members {
		if m.ID != s.Request.Selected[i] {
			return errors.New("model action selection order differs from request")
		}
		if !m.Change {
			guardCount++
		}
	}
	if s.TableBaseline == "" {
		return errors.New("missing fixed table revision baseline")
	}
	projected, err := ProjectRevision(s.Request.Revision, s.TableBaseline)
	if err != nil {
		return fmt.Errorf("table revision projection: %w", err)
	}
	if projected != s.Action.ProjectedRevision {
		return errors.New("model request revision differs from projected runtime revision")
	}
	for i, id := range s.Request.Selected {
		base, ok := s.MemberBaseline[id]
		if !ok {
			return fmt.Errorf("member %s lacks fixed revision baseline", id)
		}
		if _, err := ProjectRevision(s.Before.Members[id].Revision, base); err != nil {
			return fmt.Errorf("member %s before projection: %w", id, err)
		}
		if _, err := ProjectRevision(s.After.Members[id].Revision, base); err != nil {
			return fmt.Errorf("member %s after projection: %w", id, err)
		}
		rawGuard, ok := s.Request.ExpectedRevision[id]
		if !ok {
			return fmt.Errorf("member %s lacks decoded revision guard entry", id)
		}
		modelGuard := s.Action.Members[i].Revision
		if rawGuard == nil {
			if modelGuard != nil {
				return fmt.Errorf("member %s omitted guard became a model comparison", id)
			}
			continue
		}
		want, err := ProjectRevision(*rawGuard, base)
		if err != nil {
			return fmt.Errorf("member %s guard projection: %w", id, err)
		}
		if modelGuard == nil || *modelGuard != want {
			return fmt.Errorf("member %s model revision differs from raw guard", id)
		}
	}
	if s.Before.Epoch == "" || s.After.Epoch == "" {
		return errors.New("missing observed epoch")
	}
	if s.Result == Refused || s.Result == Replayed {
		if !reflect.DeepEqual(s.Before.Image, s.After.Image) {
			return errors.New("noncommitting call changed the complete store image")
		}
		if beforeRev != afterRev || s.Before.Operations != s.After.Operations || s.Before.Receipts != s.After.Receipts {
			return errors.New("noncommitting call changed revision, operation, or receipt count")
		}
		if s.Result == Refused && s.Receipt != nil {
			return errors.New("refusal returned a receipt")
		}
		if s.Result == Replayed {
			if s.Receipt == nil || s.PriorReceipt == nil || !reflect.DeepEqual(*s.Receipt, *s.PriorReceipt) {
				return errors.New("retry did not return the original receipt identity and payload")
			}
			if s.Before.Recorded == nil || !sameOperation(*s.Before.Recorded, s.Request, *s.PriorReceipt) {
				return errors.New("retry lacks the original durable operation identity")
			}
		}
		return nil
	}
	if s.Result != Accepted {
		return fmt.Errorf("unknown result %q", s.Result)
	}
	if s.Before.Epoch != s.Request.Epoch || s.After.Epoch != s.Request.Epoch || s.Before.TableRevision != s.Request.Revision {
		return errors.New("accepted request did not match prestate epoch and revision")
	}
	if s.Receipt == nil {
		return errors.New("accepted batch has no receipt")
	}
	r := *s.Receipt
	if r.StreamID == "" || r.OperationID != s.Request.OperationID || r.Digest != s.Request.Digest || r.Actor != s.Request.Actor || r.Table != s.Request.Table || r.Epoch != s.Request.Epoch {
		return errors.New("receipt identity disagrees with request")
	}
	if r.BeforeRevision != s.Before.TableRevision || r.AfterRevision != s.After.TableRevision {
		return errors.New("receipt revision disagrees with snapshots")
	}
	if beforeRev == ^uint64(0) || afterRev != beforeRev+1 {
		return errors.New("accepted batch did not advance table revision once")
	}
	if s.After.Operations != s.Before.Operations+1 || s.After.Receipts != s.Before.Receipts+1 {
		return errors.New("accepted batch did not append exactly one operation and receipt")
	}
	if s.Before.Recorded != nil || s.After.Recorded == nil || !sameOperation(*s.After.Recorded, s.Request, r) {
		return errors.New("durable operation record differs from exact request bytes or receipt")
	}
	if s.After.LastReceipt == nil || !reflect.DeepEqual(*s.After.LastReceipt, r) {
		return errors.New("change-stream receipt differs from returned receipt")
	}
	changed := map[string]bool{}
	for id := range seen {
		b, a := s.Before.Members[id], s.After.Members[id]
		if err := checkMember(b); err != nil {
			return fmt.Errorf("before %s: %w", id, err)
		}
		if err := checkMember(a); err != nil {
			return fmt.Errorf("after %s: %w", id, err)
		}
		br, _ := decimal(b.Revision)
		ar, _ := decimal(a.Revision)
		physicalChange := b.Exists != a.Exists || !reflect.DeepEqual(b.Place, a.Place) || !reflect.DeepEqual(nonNil(b.Fields), nonNil(a.Fields))
		if physicalChange {
			if br == ^uint64(0) || ar != br+1 {
				return fmt.Errorf("changed member %s did not advance revision once", id)
			}
			changed[id] = true
		} else if ar != br {
			return fmt.Errorf("unchanged member %s advanced revision", id)
		}
	}
	if r.SelectedCount != uint64(len(seen)) || r.ChangedCount != uint64(len(changed)) || r.GuardCount != guardCount {
		return errors.New("receipt selection, change, or guard count disagrees with snapshots")
	}
	if r.Kind != "changed" && r.Kind != "noop" || (r.Kind == "noop") != (len(changed) == 0) {
		return errors.New("receipt outcome disagrees with member changes")
	}
	deltas := map[string]bool{}
	for _, d := range r.Members {
		if !seen[d.ID] || deltas[d.ID] {
			return fmt.Errorf("unexpected or duplicate receipt member %q", d.ID)
		}
		if !equalMember(d.Before, s.Before.Members[d.ID]) || !equalMember(d.After, s.After.Members[d.ID]) {
			return fmt.Errorf("receipt member %s disagrees with independent snapshots", d.ID)
		}
		deltas[d.ID] = true
	}
	if len(deltas) != len(seen) {
		return errors.New("receipt omits selected member")
	}
	return nil
}

func sameOperation(o OperationRecord, q Request, r Receipt) bool {
	return o.Table == q.Table && o.Epoch == q.Epoch && o.OperationID == q.OperationID &&
		o.Digest == q.Digest && o.ReceiptID == r.StreamID && SameBytes(o.Canonical, q.Canonical)
}

func equalMember(a, b Member) bool {
	if a.Exists != b.Exists || a.Revision != b.Revision || !reflect.DeepEqual(a.Place, b.Place) {
		return false
	}
	// nil and an empty map both represent no present application fields.
	return len(a.Fields) == len(b.Fields) && reflect.DeepEqual(nonNil(a.Fields), nonNil(b.Fields))
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func checkMember(m Member) error {
	if _, err := decimal(m.Revision); err != nil {
		return fmt.Errorf("member revision: %w", err)
	}
	if !m.Exists && (m.Place != nil || len(m.Fields) != 0) {
		return errors.New("missing record has placement or fields")
	}
	if m.Place != nil {
		if m.Place.Row == "" || m.Place.Column == "" || m.Place.Score == "" {
			return errors.New("incomplete placement or score")
		}
	}
	return nil
}

func decimal(s string) (uint64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, fmt.Errorf("invalid decimal uint64 %q", s)
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(v, 10) != s {
		return 0, fmt.Errorf("invalid decimal uint64 %q", s)
	}
	return v, nil
}

// ProjectRevision keeps exact uint64 arithmetic; a raw value below the fixed
// seed baseline is a failed observation, never a wrapped or reset counter.
func ProjectRevision(raw, baseline string) (uint64, error) {
	r, err := decimal(raw)
	if err != nil {
		return 0, err
	}
	b, err := decimal(baseline)
	if err != nil {
		return 0, err
	}
	if r < b {
		return 0, fmt.Errorf("revision %s fell below fixture baseline %s", raw, baseline)
	}
	return r - b, nil
}

// SameImage compares complete images without interpreting Redis types. An
// adapter must encode each key's type and complete value in stable bytes.
func SameImage(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		if y, ok := b[k]; !ok || !bytes.Equal(x, y) {
			return false
		}
	}
	return true
}
