package jevcalib

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Columns is the --pairs TSV header, exactly, in this order (#2536 rev 3 C2).
var Columns = []string{"repo", "pr", "head", "prompt8", "jev_score", "friend", "verdict", "friend_score", "author"}

// Row is one Jev score (by Prompt8 at Head) beside one friend's typed line at
// the same head.
type Row struct {
	Repo        string
	PR          int
	Head        string
	Prompt8     string
	Jev         int
	Friend      string
	Verdict     string // APPROVE or HOLD
	FriendScore int
	Author      string
}

// Pair is one head: the prompt's Jev score and the friends' answer at that
// head, the lowest of each friend's latest score, HOLD if any friend held.
type Pair struct {
	Repo        string
	PR          int
	Head        string
	Jev         int
	FriendScore int
	Land        bool // every friend APPROVE and the lowest score 8 or more
}

// ReadRows reads a pairs TSV. A header other than Columns is an error: the
// columns are positional and a reordered file would pair the wrong numbers.
func ReadRows(r io.Reader) ([]Row, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	if !sc.Scan() {
		return nil, fmt.Errorf("jevcalib: pairs: empty file, want the header %s", strings.Join(Columns, " "))
	}
	if got := strings.Split(strings.TrimRight(sc.Text(), "\r"), "\t"); strings.Join(got, "\t") != strings.Join(Columns, "\t") {
		return nil, fmt.Errorf("jevcalib: pairs: header %q, want %q", strings.Join(got, " "), strings.Join(Columns, " "))
	}
	var out []Row
	line := 1
	for sc.Scan() {
		line++
		t := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(t) == "" {
			continue
		}
		f := strings.Split(t, "\t")
		if len(f) != len(Columns) {
			return nil, fmt.Errorf("jevcalib: pairs line %d: %d columns, want %d", line, len(f), len(Columns))
		}
		pr, err1 := strconv.Atoi(f[1])
		jev, err2 := strconv.Atoi(f[4])
		fs, err3 := strconv.Atoi(f[7])
		if err1 != nil || err2 != nil || err3 != nil || pr <= 0 || jev < 1 || jev > 10 || fs < 1 || fs > 10 {
			return nil, fmt.Errorf("jevcalib: pairs line %d: pr, jev_score and friend_score must be numbers (scores 1-10)", line)
		}
		v := strings.ToUpper(f[6])
		if v != "APPROVE" && v != "HOLD" {
			return nil, fmt.Errorf("jevcalib: pairs line %d: verdict %q, want APPROVE or HOLD", line, f[6])
		}
		out = append(out, Row{Repo: f[0], PR: pr, Head: f[2], Prompt8: f[3], Jev: jev, Friend: f[5], Verdict: v, FriendScore: fs, Author: f[8]})
	}
	return out, sc.Err()
}

// Join is the pairs one prompt is judged on. It keeps the rows whose Prompt8
// is prompt8, drops the author's own line and any jev@* line, lets a later
// row for one friend at one head replace an earlier one (file order), and
// folds each head's friends into one Pair.
func Join(rows []Row, prompt8 string) []Pair {
	type key struct {
		repo string
		pr   int
		head string
	}
	type answer struct {
		verdict string
		score   int
	}
	jev := map[key]int{}
	friends := map[key]map[string]answer{}
	var order []key
	for _, r := range rows {
		if r.Prompt8 != prompt8 || r.Friend == r.Author || strings.HasPrefix(r.Friend, "jev@") || r.Friend == "jev" {
			continue
		}
		k := key{r.Repo, r.PR, r.Head}
		if _, ok := friends[k]; !ok {
			friends[k] = map[string]answer{}
			order = append(order, k)
		}
		jev[k] = r.Jev
		friends[k][r.Friend] = answer{r.Verdict, r.FriendScore}
	}
	out := make([]Pair, 0, len(order))
	for _, k := range order {
		p := Pair{Repo: k.repo, PR: k.pr, Head: k.head, Jev: jev[k], FriendScore: 11, Land: true}
		for _, a := range friends[k] {
			if a.score < p.FriendScore {
				p.FriendScore = a.score
			}
			if a.verdict != "APPROVE" {
				p.Land = false
			}
		}
		if p.FriendScore < 8 {
			p.Land = false
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].PR < out[j].PR
	})
	return out
}
