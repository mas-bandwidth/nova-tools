// Package tset implements the typed tset/1 request and response boundary.
package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const Version = "tset/1"

// Decimal is an exact unsigned integer transported as a JSON string.
type Decimal string

// Store applies one atomic step, pipelines independent steps, and reads tables.
// A deliberate refusal is returned as *Refusal. Transport failures have an
// unknown write outcome once a step has been dispatched.
type Store interface {
	Step(context.Context, Step) (Reply, error)
	Steps(context.Context, []Step) ([]StepResult, error)
	Read(context.Context, ReadPlan) (ReadReply, error)
}

// Step is one tset/1 atomic request. Op and Intent are either both nil or both
// non-nil; pointer presence distinguishes an omitted value from an empty one.
// Fence explicitly settles a named operation without applying its effects.
type Step struct {
	Epoch   Decimal
	Space   string
	Op      *string
	Intent  *string
	Fence   bool
	Result  string
	Entries []Entry
	Notes   []Note
}

// Entry is a typed union selected by Kind. From and To are cell references for
// member operations. AdvanceFrom is the epoch argument of an advance entry.
// CountMax and ScoreMax both encode as max, for count and rcount respectively.
// Rows is the complete named rank set guarded by a rowset entry.
type Entry struct {
	Kind         string
	Table        string
	From         string
	To           string
	AdvanceFrom  Decimal
	IDs          []string
	Scores       []string
	Revs         []Decimal
	Set          map[string]string
	Each         []map[string]string
	Unset        []string
	BeforeFields []string
	About        []string
	Meta         json.RawMessage
	Add          []string
	Del          []string
	Rows         []RowRank
	Cells        []string
	CountMax     []uint64
	ScoreMin     string
	ScoreMax     string
	AtLeast      *uint64
	AtMost       *uint64
}

type Note struct {
	Line  NoteLine
	About []string
}

type NoteLine struct {
	Kind string
	Meta json.RawMessage
}

// Reply is the successful write envelope. Counts are JSON integers; epochs,
// revisions and sequences are exact decimal strings.
type Reply struct {
	Status          string          `json:"status"`
	EpochBefore     Decimal         `json:"epoch_before"`
	EpochAfter      Decimal         `json:"epoch_after"`
	ActiveEpoch     Decimal         `json:"active_epoch,omitempty"`
	Changed         int             `json:"changed"`
	Guarded         int             `json:"guarded"`
	ChangedPerEntry []int           `json:"changed_per_entry"`
	FirstSeq        Decimal         `json:"first_seq"`
	LastSeq         Decimal         `json:"last_seq"`
	Lines           int             `json:"lines"`
	Result          string          `json:"result"`
	Replay          bool            `json:"replay"`
	Counters        json.RawMessage `json:"counters"`
	MemPlan         *MemPlan        `json:"-"` // Mem-only, fresh ordinary steps; never a wire field.
}

// MemPlan is the in-memory twin's normalized observation of a successful
// ordinary step. It is not persisted in receipts or encoded on the Redis wire.
type MemPlan struct {
	Entries []MemPlanEntry
	Before  map[string]map[string]MemberRecord // observed table, then ID; includes guards and no-ops
}

type MemPlanEntry struct {
	Index        int
	Entry        Entry
	Before       []MemberRecord // aligned with effective Entry.IDs
	After        []MemberRecord // aligned with effective Entry.IDs
	FieldChanges []MemFieldChange
	Added        []RowRank // effective rows.add with assigned ranks
	Deleted      []string  // effective rows.del
}

// MemFieldChange contains only actual application-field mutations for one
// changed member. It is aligned with a MemPlanEntry's effective Entry.IDs.
type MemFieldChange struct {
	Set   map[string]string
	Unset []string
}

// MarshalJSON keeps the amended compact replay response free of invented
// fields. Fresh replies retain the complete success envelope, including zeros.
func (r Reply) MarshalJSON() ([]byte, error) {
	if r.Replay {
		return json.Marshal(struct {
			Status      string  `json:"status"`
			EpochBefore Decimal `json:"epoch_before"`
			EpochAfter  Decimal `json:"epoch_after"`
			FirstSeq    Decimal `json:"first_seq"`
			LastSeq     Decimal `json:"last_seq"`
			Changed     int     `json:"changed"`
			Result      string  `json:"result"`
			Replay      bool    `json:"replay"`
		}{r.Status, r.EpochBefore, r.EpochAfter, r.FirstSeq, r.LastSeq, r.Changed, r.Result, true})
	}
	type plain Reply
	return json.Marshal(plain(r))
}

// StepResult preserves one reply/error slot per pipeline input.
type StepResult struct {
	Reply      Reply
	Err        error
	RawRequest []byte
}

type RefusalDetail struct {
	EntryIndex  *int     `json:"entry_index,omitempty"`
	QueryIndex  *int     `json:"query_index,omitempty"`
	ActiveEpoch Decimal  `json:"active_epoch,omitempty"`
	Table       string   `json:"table,omitempty"`
	IDs         []string `json:"ids"`
	Cells       []string `json:"cells"`
	Rows        []string `json:"rows"`
	Budget      string   `json:"budget,omitempty"`
	Limit       *int64   `json:"limit,omitempty"`
	Actual      *int64   `json:"actual,omitempty"`
}

// Refusal is the complete deliberate no-write response.
type Refusal struct {
	Status  string        `json:"status"`
	Code    string        `json:"code"`
	Detail  RefusalDetail `json:"detail"`
	Message string        `json:"message"`
}

func (r *Refusal) Error() string {
	if r == nil {
		return ""
	}
	if r.Message != "" {
		return r.Code + ": " + r.Message
	}
	return r.Code
}

// NewRefusal constructs the shared no-write refusal envelope. The caller may
// replace Message with a more specific explanation that discloses no prose or
// field values from the request.
func NewRefusal(code string, detail RefusalDetail) *Refusal {
	if detail.IDs == nil {
		detail.IDs = []string{}
	}
	if detail.Cells == nil {
		detail.Cells = []string{}
	}
	if detail.Rows == nil {
		detail.Rows = []string{}
	}
	return &Refusal{Status: "refused", Code: code, Detail: detail,
		Message: fmt.Sprintf("%s; nothing was changed", code)}
}

func (r Refusal) MarshalJSON() ([]byte, error) {
	if r.Detail.IDs == nil {
		r.Detail.IDs = []string{}
	}
	if r.Detail.Cells == nil {
		r.Detail.Cells = []string{}
	}
	if r.Detail.Rows == nil {
		r.Detail.Rows = []string{}
	}
	type plain Refusal
	return json.Marshal(plain(r))
}

var ErrOutcomeUnknown = errors.New("tset: dispatched step outcome unknown")

// ReadPlan is one atomic multi-query read or one page query.
type ReadPlan struct {
	Epoch   Decimal
	Space   string
	Mode    string
	Queries []ReadQuery
}

type DoneIdentity struct {
	Epoch        Decimal `json:"epoch"`
	Op           string  `json:"op"`
	IntentDigest string  `json:"intent_digest"`
}

type CardCursorPosition struct {
	About        string `json:"about"`
	NextIndex    int64  `json:"next_index"`
	ThroughIndex int64  `json:"through_index"`
}

type CardCursor struct {
	Epoch       Decimal              `json:"epoch"`
	Fields      []string             `json:"fields"`
	IncludeMeta bool                 `json:"include_meta"`
	Positions   []CardCursorPosition `json:"positions"`
}

// ReadQuery covers the L1 table queries and the L2 log queries. Fields nil
// means omitted; an allocated empty slice means an explicit empty projection.
type ReadQuery struct {
	Kind        string
	Table       string
	Cell        string
	Key         string
	Min         string
	Max         string
	Limit       int
	Desc        bool
	Records     bool
	Fields      []string
	Cells       []string
	IDs         []string
	Ops         []DoneIdentity
	AfterSeq    Decimal
	ThroughSeq  *Decimal
	IDsLimit    int
	Abouts      []string
	Cursor      *CardCursor
	IncludeMeta bool
}

// Query is retained as a short alias for code constructing a ReadPlan.
type Query = ReadQuery

type CellPlace struct {
	Row string `json:"row"`
	Col string `json:"col"`
}

type FieldValue struct {
	Present bool   `json:"present"`
	Value   string `json:"-"`
}

// FieldValue always carries value: null when absent, an exact string (even
// empty) when present. Presence cannot be inferred from text length.
func (f FieldValue) MarshalJSON() ([]byte, error) {
	if !f.Present {
		return []byte(`{"present":false,"value":null}`), nil
	}
	return json.Marshal(struct {
		Present bool   `json:"present"`
		Value   string `json:"value"`
	}{true, f.Value})
}

func (f *FieldValue) UnmarshalJSON(data []byte) error {
	var value struct {
		Present *bool           `json:"present"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Present == nil || len(value.Value) == 0 {
		return errors.New("field presence and value are required")
	}
	f.Present = *value.Present
	if !f.Present {
		var text *string
		if err := json.Unmarshal(value.Value, &text); err != nil || text != nil {
			return errors.New("absent field value must be null")
		}
		f.Value = ""
		return nil
	}
	var text *string
	if err := json.Unmarshal(value.Value, &text); err != nil {
		return fmt.Errorf("present field value: %w", err)
	}
	if text == nil {
		return errors.New("present field value must be a string")
	}
	f.Value = *text
	return nil
}

type MemberRecord struct {
	ID       string                `json:"id"`
	Exists   bool                  `json:"exists"`
	Epoch    Decimal               `json:"epoch,omitempty"`
	Revision Decimal               `json:"revision,omitempty"`
	Place    *CellPlace            `json:"place"`
	Score    string                `json:"score,omitempty"`
	Fields   map[string]FieldValue `json:"fields,omitempty"`
}

func (r MemberRecord) MarshalJSON() ([]byte, error) {
	m := map[string]any{"id": r.ID, "exists": r.Exists, "place": r.Place,
		"epoch": nil, "revision": nil, "score": nil, "fields": r.Fields}
	if r.Epoch != "" {
		m["epoch"] = r.Epoch
	}
	if r.Revision != "" {
		m["revision"] = r.Revision
	}
	if r.Score != "" {
		m["score"] = r.Score
	}
	if r.Fields == nil {
		m["fields"] = map[string]FieldValue{}
	}
	return json.Marshal(m)
}

type RowRank struct {
	Row  string  `json:"row"`
	Rank Decimal `json:"rank"`
}

type DoneSlot struct {
	Status       string       `json:"status"`
	IntentDigest string       `json:"intent_digest,omitempty"`
	Receipt      *DoneReceipt `json:"receipt,omitempty"`
}

// DoneReceipt contains only the A11 stable fields retained in done@epoch.
type DoneReceipt struct {
	Status      string  `json:"status"`
	EpochBefore Decimal `json:"epoch_before"`
	EpochAfter  Decimal `json:"epoch_after"`
	FirstSeq    Decimal `json:"first_seq"`
	LastSeq     Decimal `json:"last_seq"`
	Changed     int     `json:"changed"`
	Result      string  `json:"result"`
}

type ReadAnswer struct {
	Kind    string            `json:"kind,omitempty"`
	IDs     []string          `json:"ids,omitempty"`
	Scores  []string          `json:"scores,omitempty"`
	HasMore bool              `json:"has_more,omitempty"`
	Counts  []uint64          `json:"counts,omitempty"`
	Sum     uint64            `json:"sum,omitempty"`
	Records []MemberRecord    `json:"records,omitempty"`
	Rows    []RowRank         `json:"rows,omitempty"`
	Done    []DoneSlot        `json:"slots,omitempty"`
	LastSeq Decimal           `json:"last_seq,omitempty"`
	Lines   []json.RawMessage `json:"lines,omitempty"`
	Raw     json.RawMessage   `json:"-"`
}

func (a ReadAnswer) MarshalJSON() ([]byte, error) {
	if len(a.Raw) > 0 {
		return a.Raw, nil
	}
	m := map[string]any{"kind": a.Kind}
	switch a.Kind {
	case "range":
		m["ids"] = nonnilStrings(a.IDs)
		m["scores"] = nonnilStrings(a.Scores)
		m["has_more"] = a.HasMore
		if a.Records != nil {
			m["records"] = a.Records
		}
	case "count", "rcount":
		if a.Counts == nil {
			m["counts"] = []uint64{}
		} else {
			m["counts"] = a.Counts
		}
		m["sum"] = a.Sum
	case "ids":
		if a.Records == nil {
			m["records"] = []MemberRecord{}
		} else {
			m["records"] = a.Records
		}
	case "rows":
		if a.Rows == nil {
			m["rows"] = []RowRank{}
		} else {
			m["rows"] = a.Rows
		}
	case "done":
		if a.Done == nil {
			m["slots"] = []DoneSlot{}
		} else {
			m["slots"] = a.Done
		}
	case "last":
		m["last_seq"] = a.LastSeq
	case "lines", "cardlines":
		if a.Lines == nil {
			m["lines"] = []json.RawMessage{}
		} else {
			m["lines"] = a.Lines
		}
	}
	return json.Marshal(m)
}

func (a *ReadAnswer) UnmarshalJSON(data []byte) error {
	type plain ReadAnswer
	if err := json.Unmarshal(data, (*plain)(a)); err != nil {
		return err
	}
	a.Raw = append(a.Raw[:0], data...)
	return nil
}

func nonnilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

type ReadReply struct {
	Status      string            `json:"status"`
	Epoch       Decimal           `json:"epoch"`
	ActiveEpoch Decimal           `json:"active_epoch"`
	TimeMS      Decimal           `json:"time_ms"`
	Answers     []ReadAnswer      `json:"answers,omitempty"`
	Items       []json.RawMessage `json:"items,omitempty"`
	Next        json.RawMessage   `json:"next,omitempty"`
	Through     json.RawMessage   `json:"through,omitempty"`
	Exhausted   bool              `json:"exhausted,omitempty"`
	Complete    bool              `json:"complete,omitempty"`
	Counters    json.RawMessage   `json:"counters"`
}

func (r ReadReply) MarshalJSON() ([]byte, error) {
	m := map[string]any{"status": r.Status, "epoch": r.Epoch,
		"active_epoch": r.ActiveEpoch, "time_ms": r.TimeMS, "counters": r.Counters}
	if r.Status == "page" {
		if r.Items == nil {
			m["items"] = []json.RawMessage{}
		} else {
			m["items"] = r.Items
		}
		if r.Next == nil {
			m["next"] = nil
		} else {
			m["next"] = r.Next
		}
		if r.Through == nil {
			m["through"] = nil
		} else {
			m["through"] = r.Through
		}
		m["exhausted"] = r.Exhausted
	} else {
		if r.Answers == nil {
			m["answers"] = []ReadAnswer{}
		} else {
			m["answers"] = r.Answers
		}
		m["complete"] = r.Complete
	}
	return json.Marshal(m)
}
