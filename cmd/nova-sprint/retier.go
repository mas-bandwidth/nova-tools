package main

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// retierFriend is one api friend's part of costs retier: her work records that hold no
// token, the ones whose usage her OpenCode sessions gave (and what they priced to), and the
// ones none did, by work card.
type retierFriend struct {
	Name    string   `json:"friend"`
	Cards   int      `json:"cards"`
	Priced  int      `json:"priced"`
	Missing []string `json:"missing"`
	Total   string   `json:"total"`
}

// cmdCostsRetier is costs retier (sprint/retier.go): the one-time backfill that writes the
// tier into every stored cost record that has none and each primary's tier totals, and
// prices an api friend's past work from her OpenCode sessions (friendusage.go). It prints
// one RETIER line per stream (its landed cost by tier before and after), one RETIER FRIEND
// line per api friend (her cards found, priced and with no recoverable usage, named, and
// the total priced), then, unless --dry-run, writes in one bounded step (--cards primaries)
// and prints the step's lines. Idempotent: a second run writes nothing.
func (a *app) cmdCostsRetier(args []string, stdout, stderr io.Writer) int {
	const name = "costs retier"
	fs, c := a.verbSetup(name)
	dry := fs.Bool("dry-run", false, "print each stream's landed cost by tier before and after and each api friend's usage found, and write nothing")
	maxCards := fs.Int("cards", 250, "the most primaries one run writes; run again for the rest (the RETIER OK line says how many are left); 0 is all")
	root := fs.String("root", "", "the directory the friends' working directories are under, <root>/<friend>-working (default: HOME), as friend sync takes it")
	rest, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(rest) > 0 || *maxCards < 0 {
		return refuse(stderr, name, "takes no argument but its flags, and --cards is 0 or more")
	}
	if *root == "" {
		*root = a.getenv("HOME")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if s.Routes, _, err = st.Routes(ctx); err != nil {
		return a.readFailed(name, err, stderr)
	}
	billing, err := st.FriendBillings(ctx)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	usage, friends, err := a.apiFriendUsage(ctx, s, billing, *root)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	req := sprint.RetierReq{Max: *maxCards, FriendUsage: usage, Who: c.actor}
	res := sprint.Retier(s, req)
	for i := range friends {
		total := new(big.Rat)
		for _, key := range friendKeys(s, friends[i].Name, usage) {
			if v, ok := new(big.Rat).SetString(res.Priced[key]); ok {
				total.Add(total, v)
			}
		}
		friends[i].Total = cardcost.Cents(total)
	}
	var lines []string
	for _, rs := range res.Streams {
		lines = append(lines, sprint.RetierLine(rs))
	}
	for _, f := range friends {
		lines = append(lines, fmt.Sprintf("RETIER FRIEND friend=%s billing=api cards=%d priced=%d unrecovered=%d total=%s missing=%s",
			f.Name, f.Cards, f.Priced, len(f.Missing), f.Total, orDashStr(strings.Join(f.Missing, ","), "-")))
	}
	cards, records := 0, 0
	for _, rs := range res.Streams {
		cards, records = cards+rs.Cards, records+rs.Records
	}
	if !c.json {
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
	}
	facts := map[string]any{"streams": append([]sprint.RetierStream{}, res.Streams...), "friends": append([]retierFriend{}, friends...), "cards": cards, "records": records, "left": res.Left, "dry_run": *dry}
	if *dry || cards == 0 {
		why := "dry run, nothing was written"
		if !*dry {
			why = "nothing to write: every record has its tier and every primary its tier totals"
		}
		sayOK(stdout, c.json, name, fmt.Sprintf("RETIER OK streams=%d cards=%d records=%d left=%d: %s", len(res.Streams), cards, records, res.Left, why), facts)
		return 0
	}
	if code := a.runStep(name, *c, st, store.RetierStep(req), stdout, stderr); code != 0 {
		return code
	}
	more := ""
	if res.Left > 0 {
		more = "; run again for the rest"
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("RETIER OK streams=%d cards=%d records=%d left=%d written (a running machine applies them with its next tick)%s", len(res.Streams), cards, records, res.Left, more), facts)
	return 0
}

// apiFriendUsage is the usage of every work record of an api friend that holds no token,
// read from her OpenCode sessions (friendSessionUsage) over the window the record's take
// ran in (its end, back by its wait and run, and a minute), by the record's key; and each
// api friend's count of those records, the priced ones, and the work cards none was found
// for, by name.
func (a *app) apiFriendUsage(ctx context.Context, s *sprint.Snapshot, billing map[string]string, root string) (map[string]string, []retierFriend, error) {
	usage := map[string]string{}
	by := map[string]*retierFriend{}
	home := a.getenv("HOME")
	for _, pr := range s.Work.Column(sprint.States...) {
		for _, con := range sprint.CardCostOf(pr).Consumers {
			friend, ok := sprint.FriendOfRow(con.Who)
			if !ok || con.Kind != "work" || billing[friend] != config.BillingAPI || con.Usage.Tokens.Reported() {
				continue
			}
			f := by[friend]
			if f == nil {
				f = &retierFriend{Name: friend}
				by[friend] = f
			}
			f.Cards++
			end, err := time.Parse(time.RFC3339, con.At)
			if err != nil {
				f.Missing = append(f.Missing, con.Card)
				continue
			}
			back := 24 * time.Hour
			if con.Usage.Run >= 0 {
				back = time.Duration(con.Usage.Run+max(con.Usage.Wait, 0))*time.Second + time.Minute
			}
			dir := filepath.Join(root, friend+"-working")
			u, err := friendSessionUsage(ctx, friendStores(dir, home), dir, pr.ID, end.Add(-back), end)
			if err != nil {
				return nil, nil, fmt.Errorf("%s's sessions for %s: %s", friend, con.Card, oneline.Escape(err.Error()))
			}
			if u == "" {
				f.Missing = append(f.Missing, con.Card)
				continue
			}
			usage[con.Key] = u
			f.Priced++
		}
	}
	var out []retierFriend
	for _, f := range by {
		slices.Sort(f.Missing)
		out = append(out, *f)
	}
	slices.SortFunc(out, func(x, y retierFriend) int { return strings.Compare(x.Name, y.Name) })
	return usage, out, nil
}

// friendKeys are the record keys of usage whose record is the friend's.
func friendKeys(s *sprint.Snapshot, friend string, usage map[string]string) []string {
	var out []string
	for _, pr := range s.Work.Column(sprint.States...) {
		for _, con := range sprint.CardCostOf(pr).Consumers {
			if f, ok := sprint.FriendOfRow(con.Who); ok && f == friend && usage[con.Key] != "" {
				out = append(out, con.Key)
			}
		}
	}
	return out
}
