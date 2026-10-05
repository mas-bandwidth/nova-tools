package sprint

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The fourth fsck check: seat-agreement (docs/SPEC-SPRINT.md, fsck-seat-agreement-r.w1).
// Four values name the same coordinator:
// 1. the store key sprint:coordinator
// 2. the seat record's holder (internal/sprint/seat.go)
// 3. the actor the running server was started with (cmd/nova-sprint/serve.go records the actor it started with in its own beat or info key so fsck can read it)
// 4. the nova-config sprint row's coordinator (read through an injected reader).
const FsckCheckSeatAgreement = "seat-agreement"

// FsckFinding is one finding of an fsck check.
type FsckFinding struct {
	Check  string   `json:"check"`
	Clean  bool     `json:"clean"`
	Key    string   `json:"key"`
	Record string   `json:"record"`
	Server string   `json:"server"`
	Config string   `json:"config"`
	Fix    string   `json:"fix,omitempty"`
	Fixes  []string `json:"fixes,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

// String renders the finding as one line: FSCK <check> clean|drift key=... record=... server=... config=... [fix=...]
func (f FsckFinding) String() string {
	if f.Clean {
		return fmt.Sprintf("FSCK %s clean key=%s record=%s server=%s config=%s",
			f.Check, oneline.Field(orDash(f.Key)), oneline.Field(orDash(f.Record)), oneline.Field(orDash(f.Server)), oneline.Field(orDash(f.Config)))
	}
	fixPart := ""
	if f.Fix != "" {
		fixPart = " fix=" + oneline.Escape(f.Fix)
	}
	return fmt.Sprintf("FSCK %s drift key=%s record=%s server=%s config=%s%s",
		f.Check, oneline.Field(orDash(f.Key)), oneline.Field(orDash(f.Record)), oneline.Field(orDash(f.Server)), oneline.Field(orDash(f.Config)), fixPart)
}

// CheckSeatAgreement evaluates the four coordinator values:
// 1. the store key sprint:coordinator
// 2. the seat record's holder (record)
// 3. the actor the running server was started with (server)
// 4. the nova-config sprint row's coordinator (config)
//
// Four equal values are clean. When they disagree, it reports seat-agreement
// naming all four values, and names the fixes (seat --repair for the key,
// nova-config sprint set for the row, and changing the server's actor).
func CheckSeatAgreement(key, record, server, config string) FsckFinding {
	holder := record
	if holder == "" {
		holder = key
	}

	clean := true
	if record != "" && key != record {
		clean = false
	}
	if config != "" && config != holder {
		clean = false
	}
	if server != "" && server != MachineActor && server != holder {
		clean = false
	}

	finding := FsckFinding{
		Check:  FsckCheckSeatAgreement,
		Clean:  clean,
		Key:    key,
		Record: record,
		Server: server,
		Config: config,
	}

	if clean {
		finding.Detail = "all four coordinator values agree"
		return finding
	}

	var fixes []string
	var details []string

	if record != "" && key != record {
		fixes = append(fixes, "seat --repair")
		details = append(details, fmt.Sprintf("the key says %s and the record %s: seat --repair", orDash(key), record))
	}
	if config != "" && config != holder {
		fixes = append(fixes, fmt.Sprintf("nova-config sprint set --coordinator %s", holder))
		details = append(details, fmt.Sprintf("the config row says %s and the seat is %s's: nova-config sprint set --coordinator %s", orDash(config), holder, holder))
	}
	if server != "" && server != MachineActor && server != holder {
		fixes = append(fixes, fmt.Sprintf("change the server's NOVA_SPRINT_ACTOR=%s to NOVA_SPRINT_ACTOR=%s and restart it", server, holder))
		details = append(details, fmt.Sprintf("the server runs as %s and the seat is %s's: change the server's NOVA_SPRINT_ACTOR=%s to NOVA_SPRINT_ACTOR=%s and restart it", server, holder, server, holder))
	}

	finding.Fixes = fixes
	finding.Fix = strings.Join(fixes, "; ")
	finding.Detail = strings.Join(details, "; ")
	return finding
}

// FsckSeatAgreement checks seat agreement with an injected config reader function.
func FsckSeatAgreement(ctx context.Context, key string, rec *SeatChange, server string, readConfig func(context.Context) (string, error)) (FsckFinding, error) {
	record := ""
	if rec != nil {
		record = rec.Holder
	}
	config := ""
	if readConfig != nil {
		c, err := readConfig(ctx)
		if err != nil {
			return FsckFinding{}, err
		}
		config = c
	}
	return CheckSeatAgreement(key, record, server, config), nil
}
