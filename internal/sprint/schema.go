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

// ViewOrder is the order the stored view shows the four tables in.
var ViewOrder = []string{Work, Readers, Merge, Fleet}

// Friends is the friends table: one row per friend of nova-config's friend
// rows (the owner, 2026-10-02: "add a friends table, above fleet and below
// merge. friends | status for now. up/down/held"), with the fleet table's
// columns but load (the owner, 2026-10-02: "please give friends in the friends
// table the same ready, working, width, done, ok%, status that we have for
// machines, but no load, since they don't correspond to a machine (at the
// moment...)"). It is drawn by where from the friends' records (store/friends.go:
// the roster, each friend's job cards and her beat), never stored as a table:
// nothing in the four tables, the tick or an epoch holds it.
const Friends = "friends"

// ShownOrder is the order where shows the tables in: the four of the stored
// view, with friends after merge and before fleet.
var ShownOrder = []string{Work, Readers, Merge, Friends, Fleet}

// FriendsDef is the friends table's shape: the fleet table's columns but load.
// ready and working count her job cards in those states; width is her width
// as text, summed; ok and failed (hidden) count her jobs done ok and done
// failed, and done and ok% are the table's formulas over them, the footer
// pooling ok% over the friends; status is text with no fold. The rows are the
// friends'; where draws them from store.FriendRows.
func FriendsDef() ntable.Table {
	cols, err := ntable.ParseColumns("ready,working,width:text:sum,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%,status:text,ok,failed")
	if err != nil {
		panic(fmt.Sprintf("sprint table %s: %v", Friends, err))
	}
	return ntable.Table{Name: Friends, Columns: cols, Hidden: []string{DoneOK, DoneFailed}}
}

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
	// Cost is the work table's last column (the owner, 2026-10-01: "add a final
	// column to the work stream table, which is "cost""): a stream's cell is the sum
	// of its landed cards' totals, kept on the stream's control card (FieldCost) and
	// mirrored as the row's text; the footer is the sum over the streams.
	Cost     = "cost"
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
// holds the member's control card: its status and its width (width.go), which
// the width column shows beside working.
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
// merge, readers and fleet, its view sprint); the store tests set one to keep
// their keys apart.
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
	if _, ok := StaleStream(id); ok {
		return id + " is a stream that has not moved, read when the inbox is read, not a stored judgment: nothing answers it but its stream moving; run: nova-sprint wait " + id + " --for 30m"
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
		mk(Work, "waiting,ready,working,review,merging,landed,cost:text:sum"),
		mk(Readers, "asked,reading,ok,broken"),
		mk(Merge, "queued,merged,stuck,ci:text,state:text,since:text,returned,ctl:first:none", Since, Returned, Ctl),
		mk(Fleet, "ready,working,width:text:sum,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%,status:text,load:text,withdrawn,ok,failed,ctl:first:none",
			Withdrawn, DoneOK, DoneFailed, Ctl),
	}
}

// ViewDef is the view as init sets it: the four tables and the summary line
// counting landed primaries. Every table shows every row, empty or not.
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
func ValidID(s string) bool { return len(s) <= MaxIDLen && idRE.MatchString(s) }

// MaxIDLen is the longest identity ValidID takes.
const MaxIDLen = 128

// ValidCardID says a card's id is its parts joined by dots, each a ValidID word: a
// primary (p), a work card (p.w1), a read card (p.r1.reader).
func ValidCardID(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return false
	}
	for _, p := range parts {
		if !ValidID(p) {
			return false
		}
	}
	return true
}

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
