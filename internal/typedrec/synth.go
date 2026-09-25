package typedrec

import (
	"strconv"
	"strings"
)

// synth.go is the wrapper-written record (nova-tools#3689). Glenn 2026-09-24
// 11:25 PM: "make sure we rely on the model as little as possible. Just let it
// do work." The model writes two lines -- the card's line 1 verbatim (the sha
// anchor) and `DONE` | `ABSTAIN <why>` | `BLOCKED <why>` -- and an optional
// free-text note. Every typed field the card wrapper knows or can compute
// (SCHEMA, KIND, ATTEMPT, CHECK, REPO, BRANCH, PATHS, RED, GREEN, the Gates
// rows) is the wrapper's: Synthesize writes them, and a model-written one is
// dropped, never compared. A field only the model can know (a read's
// FINDINGS, a report's PROBES, a recut's PRIOR, ...) and a section other than
// Gates and Left owed are kept as the model wrote them.

// WrapperOwned are the typed fields the wrapper writes. A model-written line
// for one of them is dropped: the wrapper's value is the record.
var WrapperOwned = []string{"SCHEMA", "KIND", "ATTEMPT", "CHECK", "REPO", "BRANCH", "PATHS", "RED", "GREEN"}

// Note caps: the model's note is free text and becomes Left owed rows.
const (
	MaxNoteLine  = 512
	MaxNoteBytes = 2048
)

// WrapperFacts are the values the wrapper writes into the record.
type WrapperFacts struct {
	Kind    string
	Attempt int
	Repo    string
	Branch  string
	Paths   []string
	Check   string // pass | fail | not-run
	Red     string
	Green   string
	Gates   []string // Gates rows, without the leading "- "
}

// ModelLines is what the model wrote, split: its line 1 and line 2 verbatim,
// the typed fields the wrapper keeps (known to the kind, not WrapperOwned), its
// Left owed rows, its note (every other line before the first section, capped),
// and its other sections verbatim (heading and body).
type ModelLines struct {
	Line1 string
	Line2 string
	// Status is line 2's word (StatusDone, StatusAbstain, StatusBlocked), or
	// "" when line 2 is none of them.
	Status string
	// Owned is what the model wrote for a WrapperOwned field; Synthesize uses
	// it only where the wrapper has no value of its own (a card hash with no
	// repo), so a record is never missing a field the model did give.
	Owned    map[string]string
	Fields   []string
	LeftOwed []string
	Note     []string
	Sections []string
}

// SplitModel splits a model-written RESULT.md for a card of kind.
func SplitModel(raw []byte, kind string) ModelLines {
	norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	m := ModelLines{Owned: map[string]string{}}
	if len(lines) > 0 {
		m.Line1 = lines[0]
	}
	if len(lines) > 1 {
		m.Line2 = strings.TrimRight(lines[1], "\r \t")
	}
	switch word, _, _ := strings.Cut(m.Line2, " "); word {
	case StatusDone:
		if m.Line2 == StatusDone {
			m.Status = word
		}
	case StatusAbstain, StatusBlocked:
		m.Status = word
	}
	owned := map[string]bool{}
	for _, f := range WrapperOwned {
		owned[f] = true
	}
	seenField := map[string]bool{}
	seenHead := map[string]bool{}
	mode := "head" // head (before the first section), keep, owed, drop
	inFence := false
	noteBytes := 0
	addNote := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || noteBytes >= MaxNoteBytes {
			return
		}
		if len(s) > MaxNoteLine {
			s = s[:MaxNoteLine]
		}
		noteBytes += len(s)
		m.Note = append(m.Note, s)
	}
	for i := 2; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
		} else if !inFence && strings.HasPrefix(l, "## ") {
			name := l[3:]
			switch {
			case seenHead[name] || name == "Gates":
				mode = "drop" // the wrapper writes Gates; a repeated heading is dropped
			case name == "Left owed":
				mode = "owed"
			default:
				mode = "keep"
				m.Sections = append(m.Sections, l)
			}
			seenHead[name] = true
			continue
		}
		switch mode {
		case "head":
			if key, val, ok := fieldLine(l); ok && !inFence {
				if owned[key] {
					if _, dup := m.Owned[key]; !dup {
						m.Owned[key] = val
					}
					continue
				}
				if seenField[key] {
					continue
				}
				if req := Contract.RequirementFor(key, kind); req != ReqUnknown && req != "" {
					seenField[key] = true
					m.Fields = append(m.Fields, l)
					continue
				}
			}
			addNote(strings.TrimPrefix(l, "- "))
		case "owed":
			row := strings.TrimSpace(strings.TrimPrefix(l, "- "))
			if strings.HasPrefix(l, "- ") && row != "" && len(m.LeftOwed) < 64 {
				if len(row) > MaxNoteLine {
					row = row[:MaxNoteLine]
				}
				m.LeftOwed = append(m.LeftOwed, row)
			}
		case "keep":
			m.Sections = append(m.Sections, l)
		}
	}
	return m
}

// fieldLine reads `KEY: value` in the v2 typed-field syntax.
func fieldLine(l string) (key, val string, ok bool) {
	colon := strings.IndexByte(l, ':')
	if colon <= 0 || colon+1 >= len(l) || l[colon+1] != ' ' {
		return "", "", false
	}
	key = l[:colon]
	if !keyRegex.MatchString(key) {
		return "", "", false
	}
	return key, l[colon+2:], true
}

// Synthesize is the record the wrapper records for a card of f.Kind: the
// model's line 1 and line 2 as written, the wrapper's typed fields (each only
// where the kind knows it), the model's kept fields, `## Gates` from what the
// wrapper ran, `## Left owed` from the model's Left owed rows and note (or
// `- none`), and the model's other sections.
func Synthesize(raw []byte, f WrapperFacts) []byte {
	m := SplitModel(raw, f.Kind)
	var b strings.Builder
	b.WriteString(m.Line1 + "\n")
	b.WriteString(m.Line2 + "\n")
	put := func(key, val string) {
		val = oneLine(val)
		if val == "" {
			val = oneLine(m.Owned[key]) // the wrapper has none: the model's, if it gave one
		}
		if val == "" {
			return
		}
		if req := Contract.RequirementFor(key, f.Kind); req == ReqUnknown || req == "" {
			return
		}
		b.WriteString(key + ": " + val + "\n")
	}
	put("SCHEMA", "v2")
	put("KIND", f.Kind)
	if f.Attempt > 0 {
		put("ATTEMPT", strconv.Itoa(f.Attempt))
	}
	check := f.Check
	if check != "pass" && check != "fail" {
		check = "not-run"
	}
	put("CHECK", check)
	put("REPO", f.Repo)
	put("BRANCH", f.Branch)
	put("PATHS", strings.Join(f.Paths, " "))
	put("RED", f.Red)
	if check == "pass" {
		put("GREEN", f.Green)
	}
	for _, l := range m.Fields {
		b.WriteString(l + "\n")
	}
	b.WriteString("## Gates\n")
	gates := f.Gates
	if len(gates) == 0 {
		gates = []string{"TEST: not-run"}
	}
	for _, g := range gates {
		b.WriteString("- " + oneLine(g) + "\n")
	}
	b.WriteString("## Left owed\n")
	owed := append(append([]string{}, m.LeftOwed...), m.Note...)
	if len(owed) == 0 {
		owed = []string{"none"}
	}
	for _, o := range owed {
		b.WriteString("- " + o + "\n")
	}
	for _, l := range m.Sections {
		b.WriteString(l + "\n")
	}
	return []byte(b.String())
}

// oneLine makes a field value one line of at most MaxFieldSize-64 bytes.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if limit := MaxFieldSize - 64; len(s) > limit {
		s = s[:limit]
	}
	return s
}
