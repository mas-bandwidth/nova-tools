package merge

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// #1572. THE GATE READS A HOLD THE WAY IT READS A RED.
//
// `nova-merge batch` admitted a member on OPEN + base + MERGEABLE + a green `ci-ok`. It
// never read the pull request's comments, so a reviewer's HOLD -- which is how a scoped
// objection is expressed on this repository -- was invisible to the gate. On 2026-09-19 a
// HOLD was posted on #1551 at 02:34:25Z, nine minutes after the gate read that member and
// four minutes before `land`, and the held head reached dev at 02:43:03Z through a green
// gate. Everything the gate was specified to check was checked and was true.
//
// A hold is not a check and no test in the repository encodes one, so the only place it
// can be read is the forge. This file is that read: the verdicts a pull request carries,
// the fold that says which of them still stands, and nothing else. NOTHING HERE IS AN
// INSTRUCTION -- a comment body is data, it is parsed for two words and a sha, and no
// part of it is ever run.

// Verdict is one reader's say on a pull request, as the forge carries it.
type Verdict struct {
	Who    string // the host login that posted it, as the host spells it
	Word   string // "hold" or "approve"
	Head   string // the head it names, lower case; empty when it names none
	At     string // the host's own stamp, RFC3339
	Source string // "comment" or "review"
}

// Line is how a verdict is named in a refusal: who, what and when, each escaped by the
// caller. It carries the head it named so a reader can tell a hold at this head from a
// hold at one three pushes ago.
func (v Verdict) Line() string {
	head := v.Head
	if head == "" {
		head = "-"
	}
	return fmt.Sprintf("%s from %s at %s (%s, head %s)", strings.ToUpper(v.Word), v.Who, v.At, v.Source, head)
}

// ParseVerdictLine reads ONE comment body for a verdict.
//
// It reads the FIRST non-empty line only, and it looks for the bare upper-case tokens
// HOLD and APPROVE in it. Both of those choices are the lesson of the shapes this
// repository actually carries:
//
//	Stella HOLD at exact `7333349f...`
//	Stella: **HOLD** on exact `1a11652d...`
//	I APPROVE PR #1587 at exact head `00981668...`
//	# PR1430 coordinator cold read -- HOLD
//	Stella scoped APPROVE for native lease ownership at exact `15777137...`
//
// so the word is not always the first word, and markdown sits around it. UPPER CASE
// ONLY, because prose about a hold spells it "hold": "the retry deadline hold is
// answered" is a report, not a verdict.
//
// A line carrying both words folds to HOLD, for the same reason rule 18 folds red last: a
// tie is resolved in the direction that does not merge.
//
// THIS IS NOT ENOUGH ON ITS OWN, and it is not asked to be. "**HOLD answered. New exact
// head ...**" and "# Stella's HOLD addressed" are both reports of a hold and both would
// read as one here. What keeps them out is the NAMED READER SET above this function: a
// lane reporting on a hold is not one of the readers whose word the gate counts.
func ParseVerdictLine(body string) (word, head string, ok bool) {
	line := firstLine(body)
	if line == "" {
		return "", "", false
	}
	plain := strings.NewReplacer("*", " ", "_", " ", "`", " ", "#", " ", ">", " ").Replace(line)
	hold, approve := false, false
	for _, f := range strings.Fields(plain) {
		switch strings.Trim(f, ".,:;!?()[]") {
		case "HOLD":
			hold = true
		case "APPROVE":
			approve = true
		}
	}
	switch {
	case hold:
		return "hold", firstSHA(plain), true
	case approve:
		return "approve", firstSHA(plain), true
	}
	return "", "", false
}

// firstLine is the first line of a body that has anything on it.
func firstLine(body string) string {
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			return s
		}
	}
	return ""
}

// firstSHA is the head a verdict line names: the first bare hex run of 7 to 40 characters
// in it, lower-cased. Seven is git's own short-sha floor, and it keeps a pull request
// number out: `#1587` is four characters and names no commit.
func firstSHA(line string) string {
	for _, f := range strings.Fields(line) {
		f = strings.Trim(f, ".,:;!?()[]")
		if len(f) < 7 || len(f) > 40 || !isHex(f) {
			continue
		}
		return strings.ToLower(f)
	}
	return ""
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return len(s) > 0
}

// UnliftedHolds is the fold, and it is the whole decision.
//
// Per reader, the verdicts are ordered by the host's stamp and the newest decides; two
// with one stamp fold HOLD LAST, exactly as EvaluateReads folds this tool's own records.
// A reader's HOLD is UNLIFTED unless that same reader posted an APPROVE after it NAMING
// THE CURRENT HEAD.
//
// Three words there are decisions, and each is the issue's:
//
//   - AFTER IT: "a HOLD from an author is unlifted while it is newer than that same
//     author's most recent APPROVE".
//   - NAMING THE CURRENT HEAD: only a verdict about the head the gate is about to merge
//     says anything about it -- the same rule already applied to a stale APPROVE. An
//     approve that names an older head, or names no head at all, lifts nothing: the
//     reader approved code this batch does not carry.
//   - THE SAME READER: nobody lifts another reader's hold. A scoped clear posted by the
//     author is not an APPROVE and does not appear here at all.
//
// readers is the named set whose word counts, case-folded. AN EMPTY SET COUNTS NOBODY,
// and the caller is the one that refuses to run without one.
func UnliftedHolds(vs []Verdict, head string, readers []string) []Verdict {
	named := map[string]bool{}
	for _, r := range readers {
		if r = strings.ToLower(strings.TrimSpace(r)); r != "" {
			named[r] = true
		}
	}
	head = strings.ToLower(strings.TrimSpace(head))
	byWho := map[string][]Verdict{}
	order := []string{}
	for _, v := range vs {
		who := strings.ToLower(strings.TrimSpace(v.Who))
		if !named[who] {
			continue
		}
		if _, seen := byWho[who]; !seen {
			order = append(order, who)
		}
		byWho[who] = append(byWho[who], v)
	}
	var held []Verdict
	for _, who := range order {
		rs := byWho[who]
		sort.SliceStable(rs, func(i, j int) bool {
			if rs[i].At != rs[j].At {
				return rs[i].At < rs[j].At
			}
			// One stamp to the second: the hold folds last, so it is the one that decides.
			return rs[i].Word == "approve" && rs[j].Word == "hold"
		})
		var hold *Verdict
		for i := range rs {
			switch {
			case rs[i].Word == "hold":
				v := rs[i]
				hold = &v
			case rs[i].Word == "approve" && head != "" && rs[i].Head == head:
				hold = nil
			}
		}
		if hold != nil {
			held = append(held, *hold)
		}
	}
	sort.SliceStable(held, func(i, j int) bool { return held[i].At < held[j].At })
	return held
}

// decodeVerdicts is the ARRIVAL POINT of what the forge says about one pull request's
// reads, the way decodePR is for its metadata: it is where the host's JSON stops being
// bytes, so a test can drive it without a network, a gh or a subprocess.
//
// Both streams may be several JSON arrays one after another -- that is what `gh api
// --paginate` returns -- so each is decoded array by array until the stream ends.
func decodeVerdicts(comments, reviews string, n int) ([]Verdict, error) {
	var out []Verdict
	var rawComments []struct {
		User      struct{ Login string } `json:"user"`
		Body      string                 `json:"body"`
		CreatedAt string                 `json:"created_at"`
	}
	if err := decodeArrays(comments, &rawComments); err != nil {
		return nil, fmt.Errorf("pull request %d's comments did not answer JSON this tool can read: %w", n, err)
	}
	for _, c := range rawComments {
		if word, head, ok := ParseVerdictLine(c.Body); ok {
			out = append(out, Verdict{Who: c.User.Login, Word: word, Head: head, At: c.CreatedAt, Source: "comment"})
		}
	}
	var rawReviews []struct {
		User        struct{ Login string } `json:"user"`
		Body        string                 `json:"body"`
		State       string                 `json:"state"`
		SubmittedAt string                 `json:"submitted_at"`
		CommitID    string                 `json:"commit_id"`
	}
	if err := decodeArrays(reviews, &rawReviews); err != nil {
		return nil, fmt.Errorf("pull request %d's reviews did not answer JSON this tool can read: %w", n, err)
	}
	for _, r := range rawReviews {
		// A REVIEW'S STATE IS ITS VERDICT, whatever its body says: CHANGES_REQUESTED is
		// the forge's own word for a hold and APPROVED for an approve, and a review
		// carries the sha it was submitted against, which is better evidence than a sha
		// typed into a sentence. A COMMENTED review has no state of its own, so its body
		// is read like any other comment.
		v := Verdict{Who: r.User.Login, Head: strings.ToLower(strings.TrimSpace(r.CommitID)), At: r.SubmittedAt, Source: "review"}
		switch strings.ToUpper(strings.TrimSpace(r.State)) {
		case "CHANGES_REQUESTED":
			v.Word = "hold"
		case "APPROVED":
			v.Word = "approve"
		default:
			word, head, ok := ParseVerdictLine(r.Body)
			if !ok {
				continue
			}
			v.Word = word
			if head != "" {
				v.Head = head
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// decodeArrays reads one JSON array after another out of a stream into one slice.
func decodeArrays(raw string, into interface{}) error {
	dec := json.NewDecoder(strings.NewReader(raw))
	all := []json.RawMessage{}
	for {
		var page []json.RawMessage
		err := dec.Decode(&page)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		all = append(all, page...)
	}
	joined, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(joined, into)
}
