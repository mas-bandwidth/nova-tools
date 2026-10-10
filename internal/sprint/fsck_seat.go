package sprint

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// The fsck check seat-agreement (docs/SPEC-SPRINT.md, "Handing over the seat",
// fsck-seat-agreement-r.w2): four values name the same coordinator, the store's
// coordinator key, the seat record's holder, the actor the running server was
// started with (its record, store.SetServerActor) and the nova-config sprint
// row's coordinator. They split when a server's unit kept an old actor across a
// handover, and when the config row kept an old coordinator that an apply then
// wrote. The check names each split and the line that fixes it; it fixes none.

// FsckSeatAgreement is the check's name.
const FsckSeatAgreement = "seat-agreement"

// ConfigCoordinator reads the nova-config sprint row's coordinator: "" when
// the row names none. The command reads it with nova-config's library; a test
// passes a fake.
type ConfigCoordinator func(context.Context) (string, error)

// SeatAgreement is the check's finding: the four values, the seat's holder they
// are held to (the record's, else the key's while the seat has no record), and
// Drift, how they disagree, each with its fix ("" when they agree). Record is ""
// with no record, Server "" with no fresh server's record, Config "" when the
// row names no coordinator: a value that names no one is not a disagreement.
type SeatAgreement struct {
	Check  string `json:"check"`
	Key    string `json:"key"`
	Record string `json:"record"`
	Server string `json:"server"`
	Config string `json:"config"`
	Holder string `json:"holder"`
	Drift  string `json:"drift,omitempty"`
}

// CheckSeatAgreement is the finding on the four values: the key, the record
// and the server as SeatDrift holds them (seat --repair for the key, the
// server's NOVA_SPRINT_ACTOR line for the server), and the config row against
// the holder (the nova-config sprint set line for the row).
func CheckSeatAgreement(key, record, server, config string) SeatAgreement {
	f := SeatAgreement{Check: FsckSeatAgreement, Key: key, Record: record, Server: server, Config: config, Holder: record}
	if f.Holder == "" {
		f.Holder = key
	}
	var why []string
	if d := SeatDrift(key, record, server); d != "" {
		why = append(why, d)
	}
	if config != "" && config != f.Holder {
		why = append(why, "the config row says "+config+" and the seat is "+orDash(f.Holder)+"'s: nova-config sprint set --coordinator "+orDash(f.Holder))
	}
	f.Drift = strings.Join(why, "; ")
	return f
}

// FsckSeat is the check with the config row read by read; key, record and
// server are as store.SeatCheck reads them.
func FsckSeat(ctx context.Context, key, record, server string, read ConfigCoordinator) (SeatAgreement, error) {
	config, err := read(ctx)
	if err != nil {
		return SeatAgreement{}, fmt.Errorf("the nova-config sprint row was not read: %w", err)
	}
	return CheckSeatAgreement(key, record, server, config), nil
}

// Line is the finding as fsck prints it: OK or DRIFT, the check and the four
// values, and how they disagree when they do.
func (f SeatAgreement) Line() string {
	status := "OK"
	if f.Drift != "" {
		status = "DRIFT"
	}
	line := fmt.Sprintf("FSCK %s check=%s key=%s record=%s server=%s config=%s", status, f.Check,
		oneline.Field(orDash(f.Key)), oneline.Field(orDash(f.Record)), oneline.Field(orDash(f.Server)), oneline.Field(orDash(f.Config)))
	if f.Drift == "" {
		return line
	}
	return line + " " + oneline.Escape(f.Drift)
}
