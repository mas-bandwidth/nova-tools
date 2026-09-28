// Package adopt is the adoption matrix read from Redis receipts (nova-tools
// #3186, the matrix slice). A verb's adoption is one hash, adopt:<verb>
// {who, at, pov, state, gap, hand, note}, plus the age index adopt:verbs and
// the receipt history adopt:<verb>:receipts. Receipt is one FCALL
// (ns_adopt_receipt: the check and the write in one step); Matrix is one
// FCALL_RO (ns_adopt_matrix). The hand-kept table at the bottom of rowan-new
// reports/hacks-to-verbs.md is what Markdown prints from the store.
package adopt

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// States a receipt may carry. adopted-gaps, blocked and hack need a gap.
var States = []string{"adopted", "adopted-gaps", "in-flight", "unexercised", "blocked", "hack"}

// POVs are the four points of view a receipt is written from.
var POVs = []string{"coordinator", "bench", "reader", "friend"}

// NeedsGap is true for a state that must name the issue holding its gap.
func NeedsGap(state string) bool {
	return state == "adopted-gaps" || state == "blocked" || state == "hack"
}

// Adopted is true for a state that counts toward x in the status line.
func Adopted(state string) bool { return state == "adopted" || state == "adopted-gaps" }

var gapRe = regexp.MustCompile(`^([A-Za-z0-9._-]+/)?[A-Za-z0-9._-]+#[0-9]+$`)

// Receipt is one adoption receipt as the verb takes it.
type Receipt struct {
	Verb  string // the verb adopted, whitespace collapsed (see CleanVerb)
	Who   string // the seat writing it
	POV   string // one of POVs
	State string // one of States
	Gap   string // <repo>#<n> or <owner>/<repo>#<n>; required by NeedsGap
	Hand  string // the hand step the verb replaces (kept once set)
	Note  string
}

// CleanVerb trims and collapses whitespace and drops backticks, so
// "`land  stream`" and "land stream" are one record.
func CleanVerb(v string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(v, "`", "")), " ")
}

// Check is the usage check before any write: a failure is exit 2.
func (r Receipt) Check() error {
	if r.Verb == "" {
		return fmt.Errorf("--verb is required")
	}
	if len(r.Verb) > 120 {
		return fmt.Errorf("--verb is longer than 120 bytes")
	}
	for _, s := range []string{r.Verb, r.Who, r.Hand, r.Note, r.Gap} {
		if strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return fmt.Errorf("control characters are not allowed in a receipt")
		}
	}
	if r.Who == "" || strings.ContainsAny(r.Who, " :") {
		return fmt.Errorf("--as must be one seat name")
	}
	if !member(POVs, r.POV) {
		return fmt.Errorf("--pov must be one of %s", strings.Join(POVs, ", "))
	}
	if !member(States, r.State) {
		return fmt.Errorf("--state must be one of %s", strings.Join(States, ", "))
	}
	if r.Gap != "" && !gapRe.MatchString(r.Gap) {
		return fmt.Errorf("--gap must be <repo>#<n> or <owner>/<repo>#<n>")
	}
	return nil
}

func member(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

// Outcome is what Write did.
type Outcome int

const (
	Recorded  Outcome = iota // the receipt was stored
	Unchanged                // the same who, pov and body as the last one: nothing written
	Refused                  // Reason names why; nothing written
)

// Result is one Write.
type Result struct {
	Outcome  Outcome
	Reason   string
	At       int64
	Receipts int
}

// Write stores one receipt in one ns_adopt_receipt call.
func Write(ctx context.Context, c redis.Cmdable, r Receipt) (Result, error) {
	reply, err := c.FCall(ctx, "ns_adopt_receipt", nil, r.Verb, r.Who, r.POV, r.State, r.Gap, r.Hand, r.Note).StringSlice()
	if err != nil {
		return Result{}, err
	}
	if len(reply) < 2 {
		return Result{}, fmt.Errorf("ns_adopt_receipt: malformed reply %v", reply)
	}
	switch reply[0] {
	case "REFUSED":
		return Result{Outcome: Refused, Reason: reply[1]}, nil
	case "UNCHANGED":
		at, _ := strconv.ParseInt(reply[1], 10, 64)
		return Result{Outcome: Unchanged, At: at}, nil
	case "RECORDED":
		if len(reply) != 3 {
			return Result{}, fmt.Errorf("ns_adopt_receipt: malformed RECORDED %v", reply)
		}
		at, _ := strconv.ParseInt(reply[1], 10, 64)
		n, _ := strconv.Atoi(reply[2])
		return Result{Outcome: Recorded, At: at, Receipts: n}, nil
	}
	return Result{}, fmt.Errorf("ns_adopt_receipt: unexpected reply %v", reply)
}

// Row is one verb of the matrix: its latest receipt, and the POVs that hold one.
type Row struct {
	Verb, Hand, State, Who, POV, Gap string
	At                               int64
	Receipts                         int
	POVs                             []string
}

// Matrix is every verb in age order (oldest first), one FCALL_RO.
func Matrix(ctx context.Context, c redis.Cmdable) ([]Row, error) {
	v, err := c.FCallRO(ctx, "ns_adopt_matrix", nil).Slice()
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(v))
	for _, item := range v {
		f, ok := item.([]interface{})
		if !ok || len(f) != 9 {
			return nil, fmt.Errorf("ns_adopt_matrix: malformed row %v", item)
		}
		s := make([]string, len(f))
		for i, x := range f {
			s[i], _ = x.(string)
		}
		at, _ := strconv.ParseInt(s[4], 10, 64)
		n, _ := strconv.Atoi(s[7])
		var povs []string
		if s[8] != "" {
			povs = strings.Split(s[8], ",")
		}
		rows = append(rows, Row{Verb: s[0], Hand: s[1], State: s[2], Who: s[3], At: at,
			POV: s[5], Gap: s[6], Receipts: n, POVs: povs})
	}
	return rows, nil
}

// Status is the x/y line: x the verbs whose latest receipt is adopted or
// adopted-gaps, y every verb with a receipt. y=0 prints "adopted 0/0 -".
func Status(rows []Row) string {
	x := 0
	for _, r := range rows {
		if Adopted(r.State) {
			x++
		}
	}
	if len(rows) == 0 {
		return "adopted 0/0 -"
	}
	return fmt.Sprintf("adopted %d/%d %d%%", x, len(rows), x*100/len(rows))
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Line is one row as a receipt line.
func (r Row) Line() string {
	return fmt.Sprintf("ADOPT ROW verb=%s state=%s who=%s at=%d pov=%s gap=%s povs=%d/%d receipts=%d hand=%s",
		oneline.Quote(r.Verb), oneline.Field(dash(r.State)), oneline.Field(dash(r.Who)), r.At,
		oneline.Field(dash(r.POV)), oneline.Field(dash(r.Gap)), len(r.POVs), len(POVs), r.Receipts,
		oneline.Quote(r.Hand))
}

// When is a receipt time in Eastern, the way the reports print it.
func When(ms int64) string {
	if ms == 0 {
		return "-"
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04Z")
	}
	return time.UnixMilli(ms).In(loc).Format("2006-01-02 3:04 PM") + " ET"
}

func cell(s string) string {
	return strings.ReplaceAll(dash(s), "|", `\|`)
}

// Markdown is the matrix in the shape of the hacks-to-verbs table, with the
// latest receipt's who, when, pov and gap beside the state; (n/4) counts the
// POVs that hold a receipt.
func Markdown(rows []Row) string {
	var b strings.Builder
	b.WriteString("| hand step today | verb | state | who | when | pov | gap |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s | %s (%d/%d) | %s |\n",
			cell(r.Hand), strings.ReplaceAll(r.Verb, "|", `\|`), cell(r.State), cell(r.Who),
			When(r.At), cell(r.POV), len(r.POVs), len(POVs), cell(r.Gap))
	}
	return b.String()
}

// TSV is the matrix as tab-separated rows with a header.
func TSV(rows []Row) string {
	var b strings.Builder
	b.WriteString("verb\tstate\twho\tat\tpov\tgap\tpovs\treceipts\thand\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%d\t%s\n", r.Verb, dash(r.State), dash(r.Who), r.At,
			dash(r.POV), dash(r.Gap), dash(strings.Join(r.POVs, ",")), r.Receipts, dash(r.Hand))
	}
	return b.String()
}
