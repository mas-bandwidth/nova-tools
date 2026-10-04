// Package keepalive carries daemon health independently of session turns.
// Its frames never enter the ordinary bus message stream (SPEC-FRIEND).
package keepalive

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
)

const (
	Version    uint = 1
	BatchLimit      = 1024
	FrameLimit      = 2048
	ReadLimit       = 4096
)

// Seat is the current sprint authority, not a sender-selected role claim.
type Seat struct {
	Holder     string `json:"holder"`
	Epoch      uint64 `json:"epoch"`
	Generation uint64 `json:"generation"`
}

// Frame combines one challenge and the last peer challenge's acknowledgment.
// Receipt alone proves nothing: the machine validates its own outstanding ACK.
type Frame struct {
	Version     uint   `json:"version"`
	From        string `json:"from"`
	To          string `json:"to"`
	Role        string `json:"role"`
	Seat        Seat   `json:"seat"`
	Instance    string `json:"instance"`
	Seq         uint64 `json:"seq"`
	AckInstance string `json:"ack_instance"`
	AckSeq      uint64 `json:"ack_seq"`
	Asleep      bool   `json:"asleep"`
}

// Validate checks shape only; current authority and nonce freshness belong to
// the machine. Names remain trusted routing metadata, not authentication.
func (f Frame) Validate() error {
	if f.Version != Version {
		return fmt.Errorf("keepalive version wants %d", Version)
	}
	for _, name := range []string{f.From, f.To, f.Seat.Holder} {
		if why := bus2.CheckName(name); why != "" {
			return fmt.Errorf("keepalive name: %s", why)
		}
	}
	if f.From == f.To {
		return fmt.Errorf("keepalive wants distinct sender and recipient")
	}
	if f.Seat.Generation == 0 {
		return fmt.Errorf("keepalive wants a nonzero seat generation")
	}
	switch f.Role {
	case "coordinator":
		if f.From != f.Seat.Holder {
			return fmt.Errorf("coordinator frame sender must hold the seat")
		}
	case "friend":
		if f.To != f.Seat.Holder {
			return fmt.Errorf("friend frame recipient must hold the seat")
		}
	default:
		return fmt.Errorf("keepalive role wants friend or coordinator")
	}
	if !validInstance(f.Instance) || f.Seq == 0 {
		return fmt.Errorf("keepalive wants an instance token and positive sequence")
	}
	if (f.AckSeq == 0) != (f.AckInstance == "") || (f.AckSeq != 0 && !validInstance(f.AckInstance)) {
		return fmt.Errorf("keepalive acknowledgment wants both instance token and positive sequence, or neither")
	}
	return nil
}

func validInstance(s string) bool {
	return len(s) > 0 && len(s) <= 64 && strings.IndexFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_')
	}) == -1
}

// Entry preserves the cursor even for a malformed frame. A decoder reports a
// bounded reason; the loop records it and continues rather than wedging.
type Entry struct {
	ID          string
	Frame       Frame
	DecodeError string
}

type Read struct {
	Entries []Entry
	Next    string
}

// AppendResult is per input frame. Unknown means no definitive success receipt;
// an error may follow a partial write, so the caller must not blindly retry.
type AppendResult struct {
	ID      string
	Unknown bool
}

// Store owns only short-lived keepalive streams. It cannot receive, acknowledge
// or trim an ordinary message stream. All cursors and frame proofs are volatile.
type Store interface {
	Head(context.Context, string) (string, error)
	AppendBatch(context.Context, []Frame) ([]AppendResult, error)
	ReadBatch(context.Context, string, string, int) (Read, error)
}
