package fold

// split.go is nova-tools #3107 (#2756 v6 4.1.1, control 55), stacked on the
// fold of #2618: the fold keeps PR-producing cards and read cards apart, per
// work type and route, with their denominators; it refuses to finish (exit 1,
// a red `unknown <n>`) while any card of the sprint has no end record or any
// PR the sprint opened has no state; and it prints every PR approved at head
// that has not landed, with the line saying why.
//
// Fold finding 4 (2026-09-22) is why the classes are apart: 802 read cards,
// $17.37, opened no PRs by design and were counted as useful beside code
// cards, which is what made flash look good. A card is a read card only by a
// declared read type (4.10 (2)); any other type, or none, is priced as
// PR-producing, so a mistyped read shows as a code card with no PR rather than
// as a cheap useful one.

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// The two card classes.
const (
	ClassCode = "code"
	ClassRead = "read"
)

// readTypes are the work types that produce a read, not a PR (4.10 (2)).
var readTypes = map[string]bool{"read": true, "rule": true, "cold": true, "probe": true, "schema-read": true}

// prStates are the states a PR record can hold (2.3). A PR with no record, or
// a state outside this list, has no known state and blocks the fold.
var prStates = map[string]bool{
	"opened": true, "reading": true, "landable": true, "landing": true,
	"landed": true, "dropped": true, "closed": true,
}

// ClassOf is the class of a work type.
func ClassOf(typ string) string {
	if readTypes[typ] {
		return ClassRead
	}
	return ClassCode
}

// Split is one row: the cards of one class, work type and route. The class
// lines use the same shape with Type and Route empty.
type Split struct {
	Class, Type, Route string
	Cards, Done        int
	Useful             int
	PRs                int // PRs the cards opened (code only; a read card's PR is the one it read)
	Landed             int
	Closed             int // closed unmerged
	Open               int // opened, reading, landable, landing or dropped at the fold
	Priced             int
	USDMicro           int64
}

// Unpriced counts the cards whose cost was never measured.
func (s Split) Unpriced() int { return s.Cards - s.Priced }

// Approved is one PR approved 8+ at its head that has not landed.
type Approved struct {
	PR, Route, Type, Head, State, Why string
}

// Unknown names what the fold cannot account for: cards with no end record
// (no outcome) and PRs with no state.
type Unknown struct {
	Cards int
	PRs   int
	Cut   int      // every card of the sprint, ci included: the denominator
	Opens int      // every PR the sprint's code cards opened
	First []string // up to maxFirst labels or repo#n, cards first
}

// N is the unknown count the gate prints.
func (u Unknown) N() int { return u.Cards + u.PRs }

const maxFirst = 5

// Apart is the fold's split record.
type Apart struct {
	Classes  []Split // code then read
	Splits   []Split // class, type, route order
	Approved []Approved
	Unknown  Unknown
}

// UnknownError is the gate's refusal: the fold does not commit while any
// outcome is unknown. The verb exits 1 on it.
type UnknownError struct {
	Sprint string
	U      Unknown
}

// N is the unknown count.
func (e *UnknownError) N() int { return e.U.N() }

func (e *UnknownError) Error() string {
	return fmt.Sprintf("sprint %s has unknown %d (cards %d with no end record, prs %d with no state; first %s): the fold commits only when every card has ended and every PR has a state",
		e.Sprint, e.U.N(), e.U.Cards, e.U.PRs, strings.Join(e.U.First, " "))
}

var apartFields = []string{"type", "state", "outcome", "route", "usd", "repo", "pr", "head", "ci_for", "score"}

// readApart reads the cards of s:<S> again with their type and score, the PR
// records of every PR a code card opened, and the dispositions, each in one
// pipeline, and builds the split.
func readApart(ctx context.Context, client *redis.Client, key string, usefulMin int) (Apart, error) {
	var a Apart
	idx, err := scanKeys(ctx, client, key+":idx:card:*")
	if err != nil {
		return a, err
	}
	labelSet := map[string]bool{}
	if len(idx) > 0 {
		pipe := client.Pipeline()
		members := make([]*redis.StringSliceCmd, len(idx))
		for i, k := range idx {
			members[i] = pipe.SMembers(ctx, k)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return a, fmt.Errorf("read the index sets of %s: %w", key, err)
		}
		for _, m := range members {
			for _, label := range m.Val() {
				labelSet[label] = true
			}
		}
	}
	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	cards := make([]map[string]string, len(labels))
	if len(labels) > 0 {
		pipe := client.Pipeline()
		cmds := make([]*redis.SliceCmd, len(labels))
		for i, label := range labels {
			cmds[i] = pipe.HMGet(ctx, key+":card:"+label, apartFields...)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return a, fmt.Errorf("read the cards of %s: %w", key, err)
		}
		for i, cmd := range cmds {
			card := map[string]string{}
			for j, v := range cmd.Val() {
				if s, ok := v.(string); ok {
					card[apartFields[j]] = s
				}
			}
			cards[i] = card
		}
	}

	// The PR records and dispositions of every PR a code card opened.
	prKeys := map[string]bool{}
	for _, c := range cards {
		if c["ci_for"] == "" && ClassOf(c["type"]) == ClassCode && c["repo"] != "" && c["pr"] != "" {
			prKeys[c["repo"]+":"+c["pr"]] = true
		}
	}
	prs := map[string]map[string]string{}
	disp := map[string]map[string]string{}
	if len(prKeys) > 0 {
		pipe := client.Pipeline()
		prCmds := map[string]*redis.MapStringStringCmd{}
		dispCmds := map[string]*redis.MapStringStringCmd{}
		for k := range prKeys {
			prCmds[k] = pipe.HGetAll(ctx, key+":pr:"+k)
			dispCmds[k] = pipe.HGetAll(ctx, key+":disp:"+k)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return a, fmt.Errorf("read the PR records of %s: %w", key, err)
		}
		for k := range prKeys {
			prs[k], disp[k] = prCmds[k].Val(), dispCmds[k].Val()
		}
	}

	rows := map[[3]string]*Split{}
	classes := map[string]*Split{ClassCode: {Class: ClassCode}, ClassRead: {Class: ClassRead}}
	var unknownCards, unknownPRs []string
	seenPR := map[string]bool{}
	for i, c := range cards {
		a.Unknown.Cut++
		if strings.TrimSpace(c["outcome"]) == "" {
			unknownCards = append(unknownCards, labels[i])
		}
		if c["ci_for"] != "" {
			continue // ci cards are the fold's own line
		}
		typ, route := c["type"], c["route"]
		if typ == "" {
			typ = "-"
		}
		if route == "" {
			route = "-"
		}
		class := ClassOf(c["type"])
		k := [3]string{class, typ, route}
		row := rows[k]
		if row == nil {
			row = &Split{Class: class, Type: typ, Route: route}
			rows[k] = row
		}

		var useful, landed, hasPR, closed, open bool
		prKey := c["repo"] + ":" + c["pr"]
		if class == ClassRead {
			n, err := strconv.Atoi(strings.TrimSpace(c["score"]))
			useful = err == nil && n >= usefulMin
		} else if c["repo"] != "" && c["pr"] != "" {
			hasPR = true
			pr := prs[prKey]
			st := pr["state"]
			if !prStates[st] {
				if !seenPR[prKey] {
					seenPR[prKey] = true
					unknownPRs = append(unknownPRs, prKey)
				}
			}
			landed = c["state"] == "landed" || st == "landed"
			closed = !landed && st == "closed"
			open = !landed && !closed && prStates[st]
			approved := usefulAtHead(disp[prKey], c["head"], usefulMin)
			useful = landed || approved
			if approved && !landed {
				a.Approved = append(a.Approved, Approved{
					PR: c["repo"] + "#" + c["pr"], Route: route, Type: typ, Head: c["head"],
					State: stateOr(st), Why: whyLine(pr),
				})
			}
		}
		priced, micro := false, int64(0)
		switch v := strings.TrimSpace(c["usd"]); v {
		case "", tokens.Dash:
		default:
			m, ok := tokens.ParseMicro(v)
			if !ok {
				return a, fmt.Errorf("card %s has usd=%q, not a dollar amount", labels[i], v)
			}
			priced, micro = true, m
		}
		for _, s := range []*Split{row, classes[class]} {
			s.Cards++
			if c["outcome"] == "DONE" {
				s.Done++
			}
			if useful {
				s.Useful++
			}
			if hasPR {
				s.PRs++
			}
			if landed {
				s.Landed++
			}
			if closed {
				s.Closed++
			}
			if open {
				s.Open++
			}
			if priced {
				s.Priced++
				s.USDMicro += micro
			}
		}
	}
	a.Unknown.Opens = len(prKeys)
	sort.Slice(unknownPRs, func(i, j int) bool { return prLess(unknownPRs[i], unknownPRs[j]) })
	a.Unknown.Cards, a.Unknown.PRs = len(unknownCards), len(unknownPRs)
	for _, l := range unknownCards {
		if len(a.Unknown.First) < maxFirst {
			a.Unknown.First = append(a.Unknown.First, l)
		}
	}
	for _, k := range unknownPRs {
		if len(a.Unknown.First) < maxFirst {
			a.Unknown.First = append(a.Unknown.First, strings.Replace(k, ":", "#", 1))
		}
	}

	a.Classes = []Split{*classes[ClassCode], *classes[ClassRead]}
	for _, r := range rows {
		a.Splits = append(a.Splits, *r)
	}
	sort.Slice(a.Splits, func(i, j int) bool {
		x, y := a.Splits[i], a.Splits[j]
		if x.Class != y.Class {
			return x.Class == ClassCode // code rows first
		}
		if x.Type != y.Type {
			return x.Type < y.Type
		}
		return x.Route < y.Route
	})
	sort.Slice(a.Approved, func(i, j int) bool {
		return prLess(strings.Replace(a.Approved[i].PR, "#", ":", 1), strings.Replace(a.Approved[j].PR, "#", ":", 1))
	})
	return a, nil
}

// prLess orders repo:n keys by repo, then by the number.
func prLess(a, b string) bool {
	ra, na, _ := strings.Cut(a, ":")
	rb, nb, _ := strings.Cut(b, ":")
	if ra != rb {
		return ra < rb
	}
	x, errx := strconv.Atoi(na)
	y, erry := strconv.Atoi(nb)
	if errx == nil && erry == nil && x != y {
		return x < y
	}
	return na < nb
}

func stateOr(st string) string {
	if st == "" {
		return "-"
	}
	return st
}

// whyLine names every gate the PR record says stands between an approved PR
// and a merge, in the order a lander meets them. It reads the record alone
// (2.3); `why <repo>#<n>` (#3106) prints the same gates with the holder's
// state and who can release, and replaces this once it lands.
func whyLine(pr map[string]string) string {
	var gates []string
	if pr["state"] == "closed" {
		gates = append(gates, "closed unmerged")
	}
	if pr["draft"] == "true" {
		gates = append(gates, "draft")
	}
	if n, _ := strconv.Atoi(pr["holds_open"]); n > 0 {
		gates = append(gates, fmt.Sprintf("holds %d open", n))
	}
	if bar, err := strconv.Atoi(pr["land_bar"]); err == nil {
		if got, _ := strconv.Atoi(pr["reads_ok"]); got < bar {
			gates = append(gates, fmt.Sprintf("reads %d of %d at head", got, bar))
		}
	}
	if m := pr["mergeable"]; m != "" && m != "MERGEABLE" {
		gates = append(gates, "mergeable "+m)
	}
	if p := pr["stack_parent"]; p != "" && p != "none" {
		gates = append(gates, "stack parent "+p)
	}
	if d := pr["drop_key"]; d != "" {
		gates = append(gates, "dropped "+d)
	}
	if l := pr["lane"]; l != "" && pr["lane_result"] != "" && pr["lane_result"] != "LANDED" {
		gates = append(gates, "lane "+l+" "+pr["lane_result"])
	}
	if len(gates) == 0 {
		if len(pr) == 0 {
			return "no PR record"
		}
		return "no gate recorded at state " + stateOr(pr["state"])
	}
	return strings.Join(gates, "; ")
}

const (
	red   = "\x1b[31m"
	reset = "\x1b[0m"
)

func splitFields(s Split) string {
	return fmt.Sprintf("cards=%d done=%d useful=%d prs=%d landed=%d closed=%d open=%d usd=%s usd_per_useful=%s usd_per_landed=%s unpriced=%d",
		s.Cards, s.Done, s.Useful, s.PRs, s.Landed, s.Closed, s.Open, splitUSD(s),
		per(s.USDMicro, s.Useful, s.Priced), per(s.USDMicro, s.Landed, s.Priced), s.Unpriced())
}

func splitUSD(s Split) string {
	if s.Priced == 0 {
		return tokens.Dash
	}
	return tokens.Usd(s.USDMicro)
}

// PrintApart prints the split rows, the two class lines, the approved-not-
// landed PRs with their why, and the outcome line: `unknown 0` plain, or
// `unknown <n>` in red when the fold will refuse.
func PrintApart(out io.Writer, sprint string, a Apart) {
	for _, s := range a.Splits {
		fmt.Fprintf(out, "FOLD SPLIT sprint=%s class=%s type=%s route=%s %s\n",
			sprint, s.Class, oneline.Field(s.Type), oneline.Field(s.Route), splitFields(s))
	}
	for _, s := range a.Classes {
		fmt.Fprintf(out, "FOLD CLASS sprint=%s class=%s %s\n", sprint, s.Class, splitFields(s))
	}
	for _, p := range a.Approved {
		fmt.Fprintf(out, "FOLD APPROVED-NOT-LANDED sprint=%s pr=%s route=%s type=%s head=%s state=%s why=%s\n",
			sprint, oneline.Field(p.PR), oneline.Field(p.Route), oneline.Field(p.Type), oneline.Field(p.Head),
			oneline.Field(p.State), oneline.Quote(p.Why))
	}
	u := a.Unknown
	if u.N() == 0 {
		fmt.Fprintf(out, "FOLD OUTCOMES sprint=%s unknown 0 cards=%d prs=%d\n", sprint, u.Cut, u.Opens)
		return
	}
	first := make([]string, len(u.First))
	for i, f := range u.First {
		first[i] = oneline.Field(f)
	}
	fmt.Fprintf(out, "%sFOLD UNKNOWN sprint=%s unknown %d cards=%d prs=%d first=%s%s\n",
		red, sprint, u.N(), u.Cards, u.PRs, strings.Join(first, ","), reset)
}

// unknownGate is the refusal while any outcome is unknown.
func unknownGate(sprint string, a Apart) error {
	if a.Unknown.N() == 0 {
		return nil
	}
	return &UnknownError{Sprint: sprint, U: a.Unknown}
}

func splitSexp(head string, s Split) string {
	return fmt.Sprintf("(%s :cards %d :done %d :useful %d :prs %d :landed %d :closed %d :open %d :usd %s :usd-per-useful %s :usd-per-landed %s :unpriced %d)",
		head, s.Cards, s.Done, s.Useful, s.PRs, s.Landed, s.Closed, s.Open, q(splitUSD(s)),
		q(per(s.USDMicro, s.Useful, s.Priced)), q(per(s.USDMicro, s.Landed, s.Priced)), s.Unpriced())
}

// apartSexp is the split's part of the fold record, one keyword per list.
func apartSexp(a Apart) string {
	var b strings.Builder
	b.WriteString("\n  :classes (")
	for i, s := range a.Classes {
		if i > 0 {
			b.WriteString("\n             ")
		}
		b.WriteString(splitSexp("class "+q(s.Class), s))
	}
	b.WriteString(")\n  :splits (")
	for i, s := range a.Splits {
		if i > 0 {
			b.WriteString("\n            ")
		}
		b.WriteString(splitSexp("split "+q(s.Class)+" "+q(s.Type)+" "+q(s.Route), s))
	}
	b.WriteString(")\n  :approved-not-landed (")
	for i, p := range a.Approved {
		if i > 0 {
			b.WriteString("\n                        ")
		}
		fmt.Fprintf(&b, "(approved-not-landed %s :route %s :type %s :head %s :state %s :why %s)",
			q(p.PR), q(p.Route), q(p.Type), q(p.Head), q(p.State), q(p.Why))
	}
	b.WriteString(")")
	return b.String()
}
