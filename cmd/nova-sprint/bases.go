package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// bases is a read (docs/SPEC-SPRINT.md section 11, bases-view-r.w2): one row per base a
// card not landed or dropped names, so a base nobody watches is seen without a chain of
// card calls. It writes nothing to the store; its one write is the fetch into land's kept
// clone of each repository, the clone land fetches into itself.
func init() {
	verbEffect["bases"] = "inspection: reads the work and merge tables and writes nothing to the store; fetches origin's dev and each base into land's kept clone of each repository, once a call, and clones nothing"
}

// basesTrunk is the branch each base is counted ahead of and behind.
const basesTrunk = "dev"

// baseRow is one base in use: the repository its cards name (REPO:), its open cards by
// state, ahead and behind origin's dev (nil when not known), and the gate at its tip as
// the lander last recorded it: green, red, or - when never gated, and when (RFC3339).
type baseRow struct {
	Base   string              `json:"base"`
	Repo   string              `json:"repo"`
	N      int                 `json:"n"`
	Cards  map[string][]string `json:"cards"`
	Ahead  *int                `json:"ahead"`
	Behind *int                `json:"behind"`
	Gate   string              `json:"gate"`
	Gated  string              `json:"gated,omitempty"`
}

// baseStates are the states an open card is counted in, in the work table's order.
var baseStates = []sprint.State{sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging}

func (a *app) cmdBases(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("bases")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "bases", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "bases", err.Error())
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return a.readFailed("bases", err, stderr)
	}
	rows := basesInUse(s)
	notes := a.basesAhead(ctx, rows)
	cards := 0
	for _, r := range rows {
		cards += r.N
	}
	if c.json {
		// ignored: strings, ints and maps of string slices always encode
		b, _ := json.Marshal(struct {
			Bases []baseRow `json:"bases"`
			Cards int       `json:"cards"`
			Notes []string  `json:"notes"`
		}{rows, cards, nonNil(notes)})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, r := range rows {
		line := fmt.Sprintf("BASES %s repo=%s cards=%d", oneline.Escape(r.Base), oneline.Field(dashed(r.Repo)), r.N)
		for _, state := range baseStates {
			line += fmt.Sprintf(" %s=%d", state, len(r.Cards[state]))
		}
		fmt.Fprintf(stdout, "%s ahead=%s behind=%s gate=%s gated=%s\n", line, intOrDash(r.Ahead), intOrDash(r.Behind), r.Gate, dashed(r.Gated))
	}
	for _, n := range notes {
		fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(n))
	}
	fmt.Fprintf(stdout, "BASES OK bases=%d cards=%d\n", len(rows), cards)
	return 0
}

// basesInUse is a row per (repository, base) an open primary names (placed, not landed,
// not a sentinel), in base order, each with the gate the lander last recorded at its tip:
// green at a card on it landing (land gates the tip before it merges, landgo.go), red at
// a stream stopped on its gate (the base-gate rule's third failure, MergeReq.BaseRed: the
// stop's time, for the base of each card it holds merging); the latest record wins, red on
// a tie, and - when none. A refusal before the third is the lander's memory, not the store's.
func basesInUse(s *sprint.Snapshot) []baseRow {
	type mark struct {
		gate string
		at   time.Time
	}
	marks := map[string]mark{}
	record := func(base, gate, at string) {
		t, err := time.Parse(time.RFC3339, at)
		if base == "" || err != nil {
			return
		}
		if m, ok := marks[base]; !ok || t.After(m.at) || t.Equal(m.at) && gate == "red" {
			marks[base] = mark{gate, t}
		}
	}
	baseOf := func(c *sprint.Card) (repo, base string) {
		cb := swarm.ReadCardBase([]byte(c.F("brief")))
		return cb.Repo, cb.Ref
	}
	byKey := map[[2]string]*baseRow{}
	for _, c := range s.Work.Cards() {
		if !c.Placed() || c.F("kind") == "sentinel" {
			continue
		}
		repo, base := baseOf(c)
		if base == "" {
			continue
		}
		if c.Col == sprint.Landed {
			record(base, "green", c.F("landed"))
			continue
		}
		k := [2]string{repo, base}
		if byKey[k] == nil {
			byKey[k] = &baseRow{Base: base, Repo: repo, Cards: map[string][]string{}}
		}
		byKey[k].N++
		byKey[k].Cards[c.Col] = append(byKey[k].Cards[c.Col], c.ID)
	}
	for _, stream := range s.Streams() {
		ctl := s.StreamCtl(stream)
		if ctl.F("state") == sprint.StreamStopped && ctl.F("cause") == "base" {
			for _, c := range s.Work.Cell(stream, sprint.Merging) {
				_, base := baseOf(c)
				record(base, "red", ctl.F("since"))
			}
		}
	}
	rows := make([]baseRow, 0, len(byKey))
	for _, r := range byKey {
		r.Gate = "-"
		if m, ok := marks[r.Base]; ok {
			r.Gate, r.Gated = m.gate, m.at.UTC().Format(time.RFC3339)
		}
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(x, y baseRow) int {
		return strings.Compare(x.Base+"\x00"+x.Repo, y.Base+"\x00"+y.Repo)
	})
	return rows
}

// basesAhead sets each row's ahead and behind origin's dev, by git rev-list in land's kept
// clone of its repository, after one fetch per clone of dev and every base named; a base
// origin does not hold is found by one ls-remote when that fetch fails, and the rest are
// fetched again. It clones nothing: a repository land keeps no clone of is a note, as is a
// fetch that fails (the counts are then the last fetch's). The notes, in order.
func (a *app) basesAhead(ctx context.Context, rows []baseRow) []string {
	var notes []string
	root, err := a.landRoot()
	if err != nil {
		return []string{"no land directory to read clones in (" + oneline.Err(err) + "): ahead and behind are not known"}
	}
	var repos []string
	for _, r := range rows {
		if r.Repo != "" && !slices.Contains(repos, r.Repo) {
			repos = append(repos, r.Repo)
		}
	}
	git := func(dir string, args ...string) (string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, args...)
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(res.Stderr)))
		}
		return strings.TrimSpace(string(res.Stdout)), nil
	}
	for _, repo := range repos {
		dir := filepath.Join(root, repoDirName(repo))
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			notes = append(notes, "land keeps no clone of "+repo+" (at "+dir+"), so its bases' ahead and behind are not known; land makes it at its first landing there")
			continue
		}
		names := []string{basesTrunk}
		for _, r := range rows {
			if r.Repo == repo && !slices.Contains(names, r.Base) {
				names = append(names, r.Base)
			}
		}
		fetch := func(names []string) error {
			args := []string{"fetch", "--no-tags", "origin"}
			for _, n := range names {
				args = append(args, "+refs/heads/"+n+":refs/remotes/origin/"+n)
			}
			_, err := git(dir, args...)
			return err
		}
		missing := map[string]bool{}
		if err := fetch(names); err != nil {
			heads, lerr := git(dir, "ls-remote", "--heads", "origin")
			if lerr != nil {
				notes = append(notes, "the fetch of origin in "+dir+" failed, so ahead and behind are as of its last fetch: "+firstLine("", err))
			} else {
				var have []string
				for _, n := range names {
					if strings.Contains("\n"+heads+"\n", "\trefs/heads/"+n+"\n") {
						have = append(have, n)
					} else {
						missing[n] = true
						notes = append(notes, "the base "+n+" is no branch of origin "+repo)
					}
				}
				if len(have) == 0 {
					continue
				}
				if err := fetch(have); err != nil {
					notes = append(notes, "the fetch of origin in "+dir+" failed, so ahead and behind are as of its last fetch: "+firstLine("", err))
				}
			}
		}
		for i := range rows {
			r := &rows[i]
			if r.Repo != repo || missing[r.Base] || missing[basesTrunk] {
				continue
			}
			out, err := git(dir, "rev-list", "--left-right", "--count", "refs/remotes/origin/"+basesTrunk+"...refs/remotes/origin/"+r.Base)
			f := strings.Fields(out)
			if err != nil || len(f) != 2 {
				notes = append(notes, "the base "+r.Base+" could not be counted against "+basesTrunk+" in "+dir+": "+firstLine(out, err))
				continue
			}
			behind, berr := strconv.Atoi(f[0])
			ahead, aerr := strconv.Atoi(f[1])
			if berr == nil && aerr == nil {
				r.Ahead, r.Behind = &ahead, &behind
			}
		}
	}
	return notes
}

// intOrDash is a count as a line says it: - when not known.
func intOrDash(n *int) string {
	if n == nil {
		return "-"
	}
	return strconv.Itoa(*n)
}

// holdBase refuses an add whose brief names a personal base (BASE: <name>/..., the name
// the sprint's coordinator, its owner or a row of the friends table: personalNames),
// naming every such base and the flag that admits it, unless allow (docs/SPEC-SPRINT.md
// section 11, bases-view-r.w2: a card on a branch someone keeps for themselves sits where
// no sprint watches).
func (a *app) holdBase(verbName string, st *store.Store, allow bool, stderr io.Writer, briefs ...string) int {
	if allow {
		return 0
	}
	var names, bad []string
	read := false
	for _, b := range briefs {
		base := swarm.ReadCardBase([]byte(b)).Ref
		owner, _, ok := strings.Cut(base, "/")
		if !ok || slices.Contains(bad, base) {
			continue
		}
		if !read {
			var err error
			if names, err = personalNames(context.Background(), st); err != nil {
				return a.readFailed(verbName, err, stderr)
			}
			read = true
		}
		if slices.Contains(names, owner) {
			bad = append(bad, base)
		}
	}
	if len(bad) == 0 {
		return 0
	}
	return refuse(stderr, verbName, fmt.Sprintf("the BASE %s is a personal branch (<name>/* for the sprint's coordinator, its owner or a friend: %s), where no sprint watches the gate; nothing was written; run: nova-sprint %s again with the card on a shared base, or with --allow-personal-base to admit it there",
		strings.Join(bad, ", "), strings.Join(names, ","), verbName))
}

// personalNames is every name whose <name>/* branches are personal: the sprint's
// coordinator, its owner and each row of the friends table, in that order, each once.
func personalNames(ctx context.Context, st *store.Store) ([]string, error) {
	coord, err := st.B.Coordinator(ctx)
	if err != nil {
		return nil, err
	}
	owner, err := st.Owner(ctx)
	if err != nil {
		return nil, err
	}
	friends, err := st.FriendNames(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range append([]string{coord, owner}, friends...) {
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}
