package tokens

import (
	"encoding/csv"
	"strconv"
	"strings"
	"time"
)

// --provider <label>=<file>: a billing export.
//
// For a harness that records nothing a tool can read — Emma's (Antigravity, Gemini),
// Johnny's (Grok), Stella's (Codex). The account holder downloads the export; the label
// names the provider and therefore the parser. The repo is the fixed word `unattributed`:
// the tool never splits a provider total across repos by any proportion, because a split
// nobody measured is a number nobody can defend.

// Parsers are the export shapes this tool knows. A label that is not one of them is a bad
// invocation rather than an unreadable file: the caller named a parser that does not exist.
var Parsers = []string{"google", "xai", "openai"}

// KnownParser reports whether a --provider label names a parser.
func KnownParser(name string) bool {
	for _, p := range Parsers {
		if p == name {
			return true
		}
	}
	return false
}

// exportColumns maps an export's own column names onto the five types. An export column
// that is not here and is not a timestamp, a date or a model is a shape the parser does
// not know, and that is TOKENS UNREADABLE rather than a guess.
var exportColumns = map[string]Type{
	"input_tokens": Input, "prompt_tokens": Input, "input": Input,
	"output_tokens": Output, "completion_tokens": Output, "output": Output,
	"cache_write_tokens": CacheWrite, "cache_creation_tokens": CacheWrite, "cache_write": CacheWrite,
	"cache_read_tokens": CacheRead, "cached_tokens": CacheRead, "cache_read": CacheRead,
	"reasoning_tokens": Reasoning, "reasoning": Reasoning,
}

// zoneDeclaration is the comment an export of per-day totals carries to say which zone its
// days are. Without it, and without per-row timestamps, the export cannot be turned into
// UTC days by any arithmetic and is refused rather than assumed.
const zoneDeclaration = "# timezone:"

// ReadProvider reads one billing export.
func ReadProvider(label, path string, _ *Rules) *Source {
	s := &Source{Label: Label(KindProvider, label), Kind: KindProvider, Path: path, Basis: UTC}
	s.Stat.Files = 1

	raw, err := readSource(path)
	if err != nil {
		s.unreadable(path, err.Error())
		return s
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	zone := ""
	var body []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			if rest, ok := strings.CutPrefix(line, zoneDeclaration); ok {
				zone = strings.TrimSpace(rest)
			}
			continue
		}
		body = append(body, line)
	}
	rd := csv.NewReader(strings.NewReader(strings.Join(body, "\n")))
	rd.FieldsPerRecord = -1
	records, err := rd.ReadAll()
	if err != nil || len(records) == 0 {
		s.unreadable(path, "the export is not the comma-separated shape the "+label+" parser reads")
		return s
	}
	header := records[0]
	stampCol, dateCol, modelCol := -1, -1, -1
	types := map[int]Type{}
	var reports []Type
	for i, name := range header {
		name = strings.TrimSpace(name)
		switch {
		case name == "timestamp":
			stampCol = i
		case name == "date":
			dateCol = i
		case name == "model":
			modelCol = i
		default:
			t, ok := exportColumns[name]
			if !ok {
				s.unreadable(path, "the "+label+" parser does not know the column "+name+" in: "+strings.Join(header, ","))
				return s
			}
			types[i] = t
			reports = append(reports, t)
		}
	}
	sortTypes(reports)
	s.Reports = reports
	if modelCol < 0 {
		s.unreadable(path, "the export names no model column")
		return s
	}
	basis := UTC
	if stampCol < 0 {
		if dateCol < 0 {
			s.unreadable(path, "the export carries neither a timestamp per row nor a date column")
			return s
		}
		if zone == "" {
			s.unreadable(path, "the export carries only per-day totals and declares no zone; a `"+zoneDeclaration+" <zone>` line is what it wants, and no arithmetic turns local days into UTC days")
			return s
		}
		if !ValidZone(zone) {
			s.unreadable(path, "the declared zone is not a zone name without whitespace: "+zone)
			return s
		}
		basis = zone
	}
	s.Basis = basis

	for i, rec := range records[1:] {
		n := i + 2
		if len(rec) != len(header) {
			s.unparsed(path, n, strconv.Itoa(len(rec))+" fields, want "+strconv.Itoa(len(header)))
			continue
		}
		var day string
		if stampCol >= 0 {
			t, err := time.Parse(time.RFC3339, strings.TrimSpace(rec[stampCol]))
			if err != nil {
				s.unparsed(path, n, "the timestamp is not RFC 3339: "+rec[stampCol])
				continue
			}
			day = t.UTC().Format("2006-01-02")
		} else {
			day = strings.TrimSpace(rec[dateCol])
			if !ValidDay(day) {
				s.unparsed(path, n, "the date is not YYYY-MM-DD: "+rec[dateCol])
				continue
			}
		}
		m := Message{Day: day, Basis: basis, Model: strings.TrimSpace(rec[modelCol]), Repo: Unattributed}
		for col, t := range types {
			cell := strings.TrimSpace(rec[col])
			if cell == "" || cell == Dash {
				continue
			}
			v, err := strconv.ParseInt(cell, 10, 64)
			if err != nil {
				continue
			}
			m.Counts.Set(t, v)
		}
		s.Stream = append(s.Stream, m)
	}
	return s
}

// sortTypes puts a reports list into the five types' own order, so that `reports=` reads
// the same way on every line whatever order an export's columns arrived in.
func sortTypes(ts []Type) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j] < ts[j-1]; j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}
