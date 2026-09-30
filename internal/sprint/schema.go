package sprint

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The four tables, by their logical names. A deployment prefixes each (Names).
const (
	Work    = "work"
	Readers = "readers"
	Merge   = "merge"
	Fleet   = "fleet"
)

// ApplyOrder is the fixed order a step that touches more than one table writes
// them in: the work table always last (docs/SPEC-SPRINT.md section 10).
var ApplyOrder = []string{Fleet, Readers, Merge, Work}

// ViewOrder is the order the view shows the tables in.
var ViewOrder = []string{Work, Readers, Merge, Fleet}

// Readers table columns.
const (
	Asked   = "asked"
	Reading = "reading"
	OK      = "ok"
	Broken  = "broken"
)

// Merge table columns. since (hidden) is when the stream's state last
// changed, for the machine. returned (hidden) holds a primary sent back from
// merging, so that accepting it again moves it rather than creating it: the
// table layer never places a removed member again. ctl (hidden) holds the
// stream's control card, whose fields are the stream's state.
const (
	Queued   = "queued"
	Merged   = "merged"
	Stuck    = "stuck"
	CI       = "ci"
	StateCol = "state"
	Since    = "since"
	Returned = "returned"
	Ctl      = "ctl"
)

// Fleet table columns (ready and working are named as in the work table).
// withdrawn (hidden) holds a work card withdrawn because no member was up, so
// that the tick deals the same card again rather than cutting another: the table
// layer never places a removed member again. ok and failed (hidden) hold the
// member's finished work cards, finished ok and finished failed. done and ok%
// are the table's own formulas over them, computed at render and never
// written: done is sum(ok+failed), ok% (the column okpct) is
// pct(ok/ok+failed), and the footer pools ok% over the members. ctl (hidden)
// holds the member's control card: its status.
const (
	Done       = "done"
	OkPct      = "okpct"
	DoneOK     = "ok"
	DoneFailed = "failed"
	Status     = "status"
	Load       = "load"
	Withdrawn  = "withdrawn"
)

// Stream states (the merge table's state column).
const (
	StreamWaiting = "waiting"
	StreamMerging = "merging"
	StreamStopped = "stopped"
	StreamLanded  = "landed"
)

// Member status.
const (
	Up   = "up"
	Down = "down"
)

// Names is where one deployment keeps its tables and keys: every name carries
// the prefix. The nova-sprint command never sets one (its tables are work,
// merge, readers and fleet, its view sprint); the field stays because Layer 1
// refuses an empty namespace, which the event-driven machine (sprintfn) passes
// as the prefix, until that machine takes the tool's place (IT23).
type Names struct{ Prefix string }

// Table is the stored name of a logical table.
func (n Names) Table(logical string) string { return n.Prefix + logical }

// View is the stored name of the view.
func (n Names) View() string { return n.Prefix + "sprint" }

// MemberPrefix is the record namespace of a logical table. Each table has its
// own: a primary is a member of work and of merge, and the two records are
// separate.
func (n Names) MemberPrefix(logical string) string {
	return n.Prefix + "sprint:" + logical[:1] + ":"
}

// TSetMemberPrefix is the record namespace of a logical table on Layer 1's
// table store (tset/1), the event-driven machine's: <prefix>member:<logical>:,
// the shape Layer 1's own tests use. MemberPrefix above lies under
// <prefix>sprint:, which Layer 1 reserves with <prefix>table:, <prefix>tables
// and the epoch key (DefineTable and table_set.lua refuse it CONFIG, L1 1.2),
// so the new machine's tables take this one; the present build keeps
// MemberPrefix, and its keys stay where they are, until the switch (IT23)
// retires it. No two logical tables' prefixes overlap: each ends in ':'.
// (Upper design version 2.1, errata 1, E7.3 and its addendum: IT12 chooses.)
func (n Names) TSetMemberPrefix(logical string) string {
	return n.Prefix + "member:" + logical + ":"
}

// Key is a sprint key outside the tables (the inbox, the operation records).
func (n Names) Key(name string) string { return n.Prefix + "sprint:" + name }

// EpochKey is the sprint's epoch: one hash whose field n is the epoch every
// one of the four tables is bound to (the table layer's epoch key). clear
// advances it; its fields cleared and shape say when, and what to restore.
func (n Names) EpochKey() string { return n.Key("epoch") }

// KeyAt is a sprint key of one epoch: the key itself at epoch 0, with the
// epoch after it at a later one. The sprint's own keys (the fence, the
// notifications, the judgments, the cursor, the operation records) are per
// epoch, so an epoch's inbox is its own and the old one stays readable.
func (n Names) KeyAt(name string, epoch uint64) string {
	if epoch == 0 {
		return n.Key(name)
	}
	return n.Key(name) + "@" + strconv.FormatUint(epoch, 10)
}

// StoredID is a card's id as the table layer holds it at an epoch: the id at
// epoch 0, the id and the epoch after it at a later one. The table layer
// keeps a record of every id it held, bound to its epoch, so a card id is used
// again in a later epoch under another stored id.
func StoredID(id string, epoch uint64) string {
	if epoch == 0 {
		return id
	}
	return id + "~" + strconv.FormatUint(epoch, 10)
}

// OpFamily is an operation's family of ids at an epoch: the family at epoch
// 0, the family and the epoch after it (as StoredID) at a later one. Every
// operation id, notification id and judgment id is built on it, so none is
// the same in two epochs; a caller's operation id holds no '~'.
func OpFamily(family string, epoch uint64) string { return StoredID(family, epoch) }

// IDEpoch is the epoch an operation, notification or judgment id belongs to:
// the number after its last '~', 0 when it has none.
func IDEpoch(id string) uint64 {
	i := strings.LastIndexByte(id, '~')
	if i < 0 {
		return 0
	}
	j := i + 1
	for j < len(id) && id[j] >= '0' && id[j] <= '9' {
		j++
	}
	n, err := strconv.ParseUint(id[i+1:j], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// OtherEpoch is the refusal of a judgment id of another epoch than the
// sprint's: it names the epoch the id belongs to.
func OtherEpoch(id string, epoch, now uint64) string {
	if epoch > now {
		return fmt.Sprintf("judgment %s belongs to epoch %d, which is unknown to this sprint (its epoch is %d); nothing was changed; run: nova-sprint inbox", id, epoch, now)
	}
	return fmt.Sprintf("judgment %s belongs to epoch %d, and the sprint's epoch is %d: a clear closed it with its epoch, and it is never acted on at another; nothing was changed; run: nova-sprint inbox", id, epoch, now)
}

// noJudgment is the refusal of a judgment id no open judgment has: one of
// another epoch than the snapshot's names its epoch.
func noJudgment(s *Snapshot, id string) string {
	if e := IDEpoch(id); e != s.Epoch {
		return OtherEpoch(id, e, s.Epoch)
	}
	return "no open judgment " + id + "; run: nova-sprint inbox"
}

// CardID is the card's id of a stored id.
func CardID(stored string) string {
	if i := strings.LastIndexByte(stored, '~'); i > 0 {
		return stored[:i]
	}
	return stored
}

// Logical maps a stored table name back to its logical name.
func (n Names) Logical(stored string) string { return strings.TrimPrefix(stored, n.Prefix) }

// Definitions are the four tables as init creates them.
func (n Names) Definitions() []ntable.Table {
	mk := func(logical, spec string, hidden ...string) ntable.Table {
		cols, err := ntable.ParseColumns(spec)
		if err != nil {
			panic(fmt.Sprintf("sprint table %s: %v", logical, err))
		}
		return ntable.Table{Name: n.Table(logical), Columns: cols, Hidden: hidden, MemberPrefix: n.MemberPrefix(logical),
			EpochKey: n.EpochKey(), EpochField: "n"}
	}
	return []ntable.Table{
		mk(Work, "waiting,ready,working,review,merging,landed"),
		mk(Readers, "asked,reading,ok,broken"),
		mk(Merge, "queued,merged,stuck,ci:text,state:text,since:text,returned,ctl:first:none", Since, Returned, Ctl),
		mk(Fleet, "ready,working,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%,status:text,load:text,withdrawn,ok,failed,ctl:first:none",
			Withdrawn, DoneOK, DoneFailed, Ctl),
	}
}

// ViewDef is the view as init sets it: the four tables and the summary line
// counting landed primaries.
func (n Names) ViewDef() ntable.View {
	v := ntable.View{Name: n.View(), Title: "SPRINT TABLE", Summary: Landed}
	for _, t := range ViewOrder {
		v.Tables = append(v.Tables, n.Table(t))
	}
	return v
}

// Identities.

var idRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

// ValidID says a primary id, a stream, a reader or a member name is one word
// of letters, digits, _ and -. A dot is refused: it separates the parts of a
// card's identity.
func ValidID(s string) bool { return len(s) <= 128 && idRE.MatchString(s) }

// WorkCardID is the identity of a primary's work card for one attempt.
func WorkCardID(primary string, attempt int) string {
	return primary + ".w" + strconv.Itoa(attempt)
}

// ReadCardID is the identity of one reader's read of a primary at one attempt.
func ReadCardID(primary string, attempt int, reader string) string {
	return primary + ".r" + strconv.Itoa(attempt) + "." + reader
}

// CtlID is the identity of a stream's or a member's control card.
func CtlID(name string) string { return "ctl-" + name }

// ParseWorkCard splits a work card identity into its primary and attempt.
func ParseWorkCard(id string) (primary string, attempt int, ok bool) {
	i := strings.LastIndex(id, ".w")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+2:])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return id[:i], n, true
}

// ParseReadCard splits a read card identity into its primary, attempt and reader.
func ParseReadCard(id string) (primary string, attempt int, reader string, ok bool) {
	parts := strings.Split(id, ".")
	if len(parts) != 3 || !strings.HasPrefix(parts[1], "r") {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(parts[1][1:])
	if err != nil || n < 1 {
		return "", 0, "", false
	}
	return parts[0], n, parts[2], true
}
