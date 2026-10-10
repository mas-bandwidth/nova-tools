package sprint

import (
	"cmp"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A HOLD's PATHS-PROPOSED line answered by rule (docs/SPEC-SPRINT.md section 8, the rules
// table's row paths; the owner, 2026-10-05: "every step the coordinator did by hand today is
// a missing instruction"). That day five cards held three to five times each on the same
// PATHS blocker: the worker wrote PATHS-PROPOSED in her report every time (docs/SPEC-CARD-
// CONTRACT.md section 4, member.PathsProposed), the failed rule dealt the same brief again
// until the attempt bound, and the coordinator widened PATHS by hand through drop and add
// --replaces. Now an attempt that comes back failed with the line is the paths rule's, never
// the failed or bound rule's, so the same brief is never dealt again on the same proposal:
//
//   - no proposed file is SHARED (named on the PATHS: or SHARED: line of another open card,
//     or on the card's own SHARED: line): the card is replaced by its twin (Recut, twins
//     inherit), its id the old one with a -t suffix (PathsTwinID), its brief the old one with
//     every PATHS: line widened to the proposal and a CARRY: line naming the held attempt's
//     pushed head (WidenBrief), the answer logged with the proposal;
//   - one is SHARED: the judgment stays the one judgment, its text the complete add
//     --replaces command, with the twin's brief written by the machine under the card's job
//     directory (TwinBriefPath), once; the card is dealt nothing more until a mind answers;
//   - a proposal that climbs out of the repository, is no glob, or names nothing outside the
//     PATHS the card has is left to a mind, and the card is not dealt again either.
//
// A stream marked land-protected does not take this twin. Finish widens PATHS in place on
// that stream, the same id, back to ready (hold_fix.go). This rule leaves such a judgment
// rather than cutting a twin, and a shared glob stays the twin command below.

// PropTwinBriefs (the work table's property) and EnvTwinBriefs (the machine's environment)
// name the directory the paths rule writes a twin's brief under, one directory a card (its
// job directory); the property over the environment, and with neither <home>/nova-sprint/jobs.
const (
	PropTwinBriefs = "twin_briefs"
	EnvTwinBriefs  = "NOVA_SPRINT_JOBS"
)

// The paths rule's acts.
const (
	ActTwin    = "replace by its widened twin"      // no proposed file is shared
	ActTwinCmd = "left with its twin command"       // a proposed file is shared: a mind's, the command printed
	pathsSaid  = "answered by rule paths: "         // the decided note's words
	pathsCmdAt = "paths proposed, shared: a mind's" // the judgment's text when it carries the command
)

// FieldPathsProposed is the proposal the paths rule last answered on a primary:
// "attempt <n>: <glob,...>".
const FieldPathsProposed = "paths_proposed"

// pathsTypes is the judgments a failed attempt raises (Finish): the failed work, the bound's
// second identical failure, the attempt cap.
var pathsTypes = []string{NWorkFailed, NBound, NBriefWrong}

// PathsProposal is a held attempt's proposal as the paths rule reads it.
type PathsProposal struct {
	Card    string   // the primary
	Attempt int      // the attempt that proposed
	Globs   []string // the proposal, as the report said it
	New     []string // the globs the card's PATHS do not hold already
	Head    string   // the attempt's pushed head, "" for none: the twin's CARRY:
	Shared  []string // "<glob> (<card>)" for each proposed glob another open card names
	Brief   string   // the twin's brief
	Twin    string   // the twin's id, "" when every one is taken
}

// mark is the proposal as the primary keeps it (FieldPathsProposed).
func (p PathsProposal) mark() string {
	return "attempt " + itoa(p.Attempt) + ": " + strings.Join(p.Globs, ",")
}

// Line is the proposal as the log keeps it.
func (p PathsProposal) Line() string { return member.ProposedKey + " " + strings.Join(p.Globs, ",") }

// heldProposal is the proposal of the primary's current attempt: its work card done failed
// with a PATHS-PROPOSED line in its report. ok is false when there is none.
func heldProposal(s *Snapshot, pr *Card) (p PathsProposal, ok bool) {
	n := pr.Int("attempt")
	if n == 0 || pr.Col != Review || pr.F("result") != "failed" {
		return p, false
	}
	wc := s.Fleet.Card(cmp.Or(pr.F("work"), WorkCardID(pr.ID, n)))
	if wc == nil {
		return p, false
	}
	globs, has := member.PathsProposed(wc.F("report"))
	if !has || len(globs) == 0 {
		return p, false
	}
	p = PathsProposal{Card: pr.ID, Attempt: n, Globs: globs}
	if h := wc.F("head"); typedrec.IsFullSha(h) {
		p.Head = h
	}
	return p, true
}

// rulePaths answers a judgment of a failed attempt whose report proposes PATHS; false when
// the attempt proposed none, and the judgment's own rule answers it.
func rulePaths(s *Snapshot, a *RuleAnswer) bool {
	pr := s.Work.Placed(a.Subject)
	if pr == nil || !slices.Contains(pathsTypes, a.open.Note.Type) {
		return false
	}
	p, ok := heldProposal(s, pr)
	if !ok {
		return false
	}
	// a finish that already parked the card on NEEDS leaves this judgment to hold-need
	// (hold_fix.go). A finish that refused the note is not twinned.
	if pr.F(FieldRuleNeed) != "" {
		return false
	}
	a.Rule, a.Card = RulePaths, pr.ID
	if strings.HasPrefix(a.open.Note.What, holdFixRefused) {
		left(a, "hold fix was not applied, so the paths rule does not twin it and the same brief is not dealt again")
		return true
	}
	said := fmt.Sprintf("attempt %d held with %s", p.Attempt, p.Line())
	if pr.F(FieldPathsProposed) == p.mark() && strings.HasPrefix(a.open.Note.What, pathsCmdAt) {
		left(a, said+": its twin command is in the judgment already, a mind's")
		return true
	}
	if why := badGlobs(p.Globs); why != "" {
		left(a, said+": "+why+"; a mind's, and the same brief is not dealt again")
		return true
	}
	brief := pr.F("brief")
	own := headerGlobs(brief, cardhdr.KeyPaths)
	for _, g := range p.Globs {
		if !slices.ContainsFunc(own, func(o string) bool { return o == g || globNames(o, g) }) {
			p.New = append(p.New, g)
		}
	}
	if len(p.New) == 0 {
		left(a, said+": the proposal is inside its PATHS already, so the blocker is not its PATHS; a mind's, and the same brief is not dealt again")
		return true
	}
	p.Shared = sharedGlobs(s, pr, p.New)
	carry := ""
	if p.Head != "" {
		carry = member.CarryLine(member.Carry{Card: pr.ID, Attempt: p.Attempt, Head: p.Head})
	}
	p.Brief = WidenBrief(brief, p.New, carry)
	// a stream marked land-protected widens PATHS in place at finish (hold_fix.go), the
	// same id. A judgment that still names such a proposal is not replaced by a twin.
	if landCovers(s, pr) && len(p.Shared) == 0 {
		left(a, said+": the stream is marked land-protected, so PATHS widen in place at finish and the card keeps its id")
		return true
	}
	if p.Twin = PathsTwinID(s, pr); p.Twin == "" {
		left(a, said+": every twin id of "+pr.ID+" is taken; a mind's")
		return true
	}
	a.paths = &p
	if len(p.Shared) > 0 {
		a.Act, a.Why = ActTwinCmd, said+": SHARED with another open card ("+strings.Join(p.Shared, "; ")+"), so the twin is a mind's: "+TwinCommand(s, pr, p)
		return true
	}
	a.Act, a.Why = ActTwin, said+": no proposed file is shared, so "+pr.ID+" is replaced by "+p.Twin+" with PATHS widened by "+strings.Join(p.New, ",")
	return true
}

// badGlobs is why a proposal is no PATHS: a glob that climbs out of the repository, or is no
// glob; "" when every one is a path in it.
func badGlobs(globs []string) string {
	var bad []string
	for _, g := range globs {
		if path.IsAbs(g) || slices.Contains(strings.Split(g, "/"), "..") {
			bad = append(bad, g+" climbs out of the repository")
		} else if _, err := path.Match(g, ""); err != nil {
			bad = append(bad, g+" is no glob: "+err.Error())
		}
	}
	return strings.Join(bad, "; ")
}

// headerGlobs is the files a brief's header line names (PATHS:, SHARED:): the first line of
// the key, read by the header's one reader (cardhdr.KeyValue), its files split on commas and
// blanks, none and - left out.
func headerGlobs(brief, key string) []string {
	for _, l := range strings.Split(brief, "\n") {
		k, v, ok := cardhdr.KeyValue(strings.TrimSpace(l))
		if !ok || k != key {
			continue
		}
		var out []string
		for _, g := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			if g != "none" && g != "-" {
				out = append(out, g)
			}
		}
		return out
	}
	return nil
}

// globNames is whether glob g names file or glob f: f matches it, or f is under the
// directory it names.
func globNames(g, f string) bool {
	m, _ := path.Match(g, f) // a bad glob names nothing
	return m || strings.HasPrefix(f, strings.TrimSuffix(g, "/")+"/")
}

// overlaps is whether two globs can name one file.
func overlaps(a, b string) bool { return a == b || globNames(a, b) || globNames(b, a) }

// sharedGlobs is each proposed glob a SHARED file: "<glob> (<card>)" for each other open
// primary whose PATHS: or SHARED: line names it, and "<glob> (its SHARED: line)" for one the
// card itself declares shared, in proposal order.
func sharedGlobs(s *Snapshot, pr *Card, globs []string) []string {
	var out []string
	var others []*Card
	for _, st := range []State{Waiting, Ready, Working, Review, Merging} {
		for _, c := range s.Work.Column(st) {
			if c.ID != pr.ID && !IsSentinel(c) {
				others = append(others, c)
			}
		}
	}
	mine := headerGlobs(pr.F("brief"), "SHARED")
	for _, g := range globs {
		var by []string
		if slices.ContainsFunc(mine, func(o string) bool { return overlaps(o, g) }) {
			by = append(by, "its SHARED: line")
		}
		for _, c := range others {
			brief := c.F("brief")
			names := append(headerGlobs(brief, cardhdr.KeyPaths), headerGlobs(brief, "SHARED")...)
			if slices.ContainsFunc(names, func(o string) bool { return overlaps(o, g) }) {
				by = append(by, c.ID)
			}
		}
		if len(by) > 0 {
			out = append(out, g+" ("+strings.Join(by, ", ")+")")
		}
	}
	return out
}

// twinSuffix is a -t twin's suffix: -t, -t2 to -t9.
var twinSuffix = regexp.MustCompile(`-t[2-9]?$`)

// PathsTwinIDs is the ids PathsTwinID chooses among: the id with -t after it, then -t2 to
// -t9; a twin the paths rule cut before (its id replacing the one it names, with the suffix)
// counts on from its own suffix, so p-t is followed by p-t2, never p-t-t.
func PathsTwinIDs(c *Card) []string {
	base := c.ID
	if prev := Split(c.F(FieldReplaces)); len(prev) == 1 && twinSuffix.MatchString(c.ID) && twinSuffix.ReplaceAllString(c.ID, "") == prev[0] {
		base = prev[0]
	}
	out := []string{base + "-t"}
	for n := 2; n <= 9; n++ {
		out = append(out, fmt.Sprintf("%s-t%d", base, n))
	}
	return out
}

// PathsTwinID is the first of PathsTwinIDs no card on the table or off it has, nor the card
// itself; "" when every one is taken.
func PathsTwinID(s *Snapshot, c *Card) string {
	for _, id := range PathsTwinIDs(c) {
		if id != c.ID && s.Work.Card(id) == nil {
			return id
		}
	}
	return ""
}

// WidenBrief is brief with every PATHS: line widened by globs and, with carry, its CARRY:
// line (PathsWidened, the read-broken rule's), and a PATHS: line after line 1 holding just
// globs when it has none.
func WidenBrief(brief string, globs []string, carry string) string {
	for _, l := range strings.Split(brief, "\n") {
		if k, _, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == cardhdr.KeyPaths {
			return PathsWidened(brief, globs, carry)
		}
	}
	lines := strings.Split(brief, "\n")
	lines = slices.Insert(lines, min(1, len(lines)), cardhdr.KeyPaths+": "+strings.Join(globs, ","))
	return PathsWidened(strings.Join(lines, "\n"), nil, carry)
}

// TwinBriefDir is the directory the paths rule writes twins' briefs under: PropTwinBriefs,
// else EnvTwinBriefs, else <home>/nova-sprint/jobs.
func TwinBriefDir(s *Snapshot) (string, error) {
	if d, ok := s.Work.Prop(PropTwinBriefs); ok && d != "" {
		return d, nil
	}
	if d := os.Getenv(EnvTwinBriefs); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "nova-sprint", "jobs"), nil
}

// TwinBriefPath is where the twin's brief of the card is written: <dir>/<card>/<twin>.md.
func TwinBriefPath(s *Snapshot, card, twin string) (string, error) {
	dir, err := TwinBriefDir(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, card, twin+".md"), nil
}

// TwinCommand is the complete command a mind runs to replace the card by the twin of the
// proposal: add --replaces, with the card's stream, needs, place and hold, and the brief file
// the machine writes (TwinBriefPath).
func TwinCommand(s *Snapshot, pr *Card, p PathsProposal) string {
	file, err := TwinBriefPath(s, pr.ID, p.Twin)
	if err != nil {
		file = filepath.Join("~", "nova-sprint", "jobs", pr.ID, p.Twin+".md")
	}
	cmd := "nova-sprint add --stream " + pr.Row + " --replaces " + pr.ID + " --before " + pr.ID + " --brief-file " + file
	waived := Split(pr.F("waived"))
	var needs []string
	for _, n := range Split(pr.F("needs")) {
		if !contains(waived, n) {
			needs = append(needs, n)
		}
	}
	if len(needs) > 0 {
		cmd += " --needs " + strings.Join(needs, ",")
	}
	if IsHeld(pr) {
		cmd += " --held"
	}
	return cmd
}

// writeTwinBrief writes the twin's brief where TwinCommand names it, its directory made.
func writeTwinBrief(s *Snapshot, card string, p PathsProposal) (string, error) {
	file, err := TwinBriefPath(s, card, p.Twin)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	return file, os.WriteFile(file, []byte(p.Brief+"\n"), 0o644)
}

// TickRulePaths answers the held cards the paths rule answers: each whose proposal shares a
// file has its brief file written and its judgment's text made the complete command, once;
// and the first whose proposal shares none is replaced by its twin (Recut, twins inherit),
// its judgment answered with the proposal logged. One twin a tick: each replace plans on the
// table the one before it leaves, so the next is the next tick's.
func TickRulePaths(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	var twin *RuleAnswer
	done := map[string]bool{}
	for _, a := range acting(s, r, ActTwin, ActTwinCmd) {
		if done[a.Card] || a.paths == nil {
			continue
		}
		done[a.Card] = true
		if a.Act == ActTwin {
			if twin == nil {
				twin = &a
			}
			continue
		}
		pr, prop := s.Work.Placed(a.Card), *a.paths
		file, err := writeTwinBrief(s, pr.ID, prop)
		if err != nil {
			p.refuse(pr.ID, "the paths rule could not write the twin's brief "+file+": "+err.Error())
			continue
		}
		n := a.open.Note
		n.What = pathsCmdAt + "; " + a.Why + "; the brief is written at " + file + "; " + n.What
		p.Updates = append(p.Updates, n)
		p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row,
			Changes: []Change{change(Work, setEntry(pr, map[string]string{FieldPathsProposed: prop.mark(), FieldRuleAnswer: RulePaths + ": " + a.Act + " at " + stamp(s.Now)}))},
			Moved:   pr.ID + " " + RuleSaid(RulePaths, a.Act+": "+prop.Line()+"; run: "+TwinCommand(s, pr, prop))})
	}
	if twin == nil {
		return p, 0
	}
	pr, prop := s.Work.Placed(twin.Card), *twin.paths
	q := Recut(s, RecutReq{ID: pr.ID, New: prop.Twin, Brief: prop.Brief, Rules: pr.F(FieldRules), Who: r.who()})
	if len(q.Refused) > 0 {
		p.Refused = append(p.Refused, q.Refused...)
		return p, 0
	}
	said := pathsSaid + pr.ID + " attempt " + itoa(prop.Attempt) + " held with " + prop.Line() + ": replaced by " + prop.Twin + ", PATHS widened by " + strings.Join(prop.New, ",")
	if prop.Head != "" {
		said += ", carrying on from " + prop.Head
	}
	o := twin.open
	at := -1
	for i := range q.Units {
		u := &q.Units[i]
		for j := range u.Changes {
			if c := &u.Changes[j]; c.Table == Work && c.Entry.ID == prop.Twin && c.Entry.Create != nil {
				c.Entry.Set[FieldPathsProposed] = prop.mark()
				c.Entry.Set[FieldRuleAnswer] = RulePaths + ": " + twin.Act + " at " + stamp(s.Now)
				if w := pr.F(FieldWho); w != "" && c.Entry.Set[FieldWho] == "" {
					c.Entry.Set[FieldWho] = w // whoever held the work keeps the twin
				}
				u.Moved += "; " + RuleSaid(RulePaths, prop.Line())
			}
		}
		if slices.ContainsFunc(u.Closes, func(c Open) bool { return c.Note.ID == o.Note.ID }) {
			at = i
		}
	}
	if len(q.Units) > 0 {
		if at < 0 {
			at = 0
			q.Units[0].Closes = append(q.Units[0].Closes, o)
		}
		q.Units[at].Notes = append(q.Units[at].Notes, decided(o, said, r.who(), s.Now, pr.ID))
	}
	q.Units = append(q.Units, p.Units...)
	q.Updates = append(q.Updates, p.Updates...)
	q.Refused = append(q.Refused, p.Refused...)
	return q, 0
}
