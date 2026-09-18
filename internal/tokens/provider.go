package tokens

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"sort"
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

// Parsers are the export shapes this tool knows, one per provider. A label that is not
// one of them is a bad invocation rather than an unreadable file: the caller named a
// parser that does not exist.
var Parsers = []string{"google", "openai", "xai"}

// KnownParser reports whether a --provider kind names a parser.
func KnownParser(name string) bool {
	_, ok := shapes[name]
	return ok
}

// zoneDeclaration is the comment an export of per-day totals carries to say which zone its
// days are. Without it, and without per-row timestamps, the export cannot be turned into
// UTC days by any arithmetic and is refused rather than assumed.
const zoneDeclaration = "# timezone:"

// A shape is ONE provider's export, and the parser is chosen by the kind the caller
// declared: `--provider google:emma=<file>` says this file is Google's export and Emma
// downloaded it. One union of every provider's column names would accept a Google export
// declared as xAI and write `provider:xai` beside numbers that parser never read -- the
// column that makes a number traceable naming the wrong source (measured 2026-09-11).
//
// The names are the ones this family has seen; a real export that spells a column
// differently is TOKENS UNREADABLE naming the parser and the column, which is a question
// for the table and never a guess by this tool.
type shape struct {
	// columns are the type columns THIS export carries, and nothing else.
	columns map[string]Type
	// stamp, date and model are the three names that are not counts.
	stamp, date, model string
}

var shapes = map[string]shape{
	"google": {
		columns: map[string]Type{
			"input_tokens": Input, "output_tokens": Output, "cache_write_tokens": CacheWrite,
			"cache_read_tokens": CacheRead, "reasoning_tokens": Reasoning,
		},
		stamp: "timestamp", date: "date", model: "model",
	},
	"openai": {
		columns: map[string]Type{
			"prompt_tokens": Input, "completion_tokens": Output,
			"cache_creation_tokens": CacheWrite, "cached_tokens": CacheRead, "reasoning_tokens": Reasoning,
		},
		stamp: "timestamp", date: "date", model: "model",
	},
	"xai": {
		columns: map[string]Type{
			"input": Input, "output": Output, "cache_write": CacheWrite,
			"cache_read": CacheRead, "reasoning": Reasoning,
		},
		stamp: "timestamp", date: "date", model: "model",
	},
}

// ParserColumns is the sorted list of column names one parser reads, for a refusal that
// says what the file WANTS and not only what was wrong.
func ParserColumns(kind string) []string {
	sh, ok := shapes[kind]
	if !ok {
		return nil
	}
	out := []string{sh.model}
	for name := range sh.columns {
		out = append(out, name)
	}
	sort.Strings(out)
	return append([]string{sh.stamp + " or " + sh.date}, out...)
}

// ReadProvider reads one billing export with the parser its kind names. The label on
// every row it feeds is `<kind>:<name>` -- `google:emma`, as the spec's own day-file
// example writes it -- so two friends' exports from one provider are two sources.
func ReadProvider(kind, name, path string, _ *Rules) *Source {
	s := &Source{Label: Label(kind, name), Kind: KindProvider, Path: path, Basis: UTC}
	s.Stat.Files = 1
	sh, known := shapes[kind]
	if !known {
		s.unreadable(path, "there is no "+kind+" parser; --provider wants <kind>:<label>=<path> with kind one of "+strings.Join(Parsers, ", "))
		return s
	}

	raw, err := readSource(path)
	if err != nil {
		s.unreadable(path, err.Error())
		return s
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	// A `grok usage` export is JSON, not CSV: the xAI parser reads both shapes, chosen by
	// the first non-space byte. The JSON shape is the documented `grok usage` object
	// (a sessionId and a turns array) and folds to the same rows the CSV shape produces.
	if trimmed := strings.TrimSpace(text); trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[') {
		return readXaiJSON(kind, path, text, s)
	}

	zone := ""
	var body []string
	// fileLine maps a line of the COMMENT-STRIPPED text back to its line in the file.
	// The `# timezone:` declaration and every other comment are dropped before the CSV
	// reader sees them, so a record index was never a line in the file: an export whose
	// declaration is line 1 reported its first data row (line 3) as line=2, and a quoted
	// field carrying a newline made the count drift further with every one of them.
	var fileLine []int
	// A `#` is a comment only where a comment can be. Inside a quoted field a line is the
	// field's own text, and deleting it parses the record from the wrong bytes: the
	// count of quote characters so far is odd exactly while the reader is inside one (an escaped
	// `""` per RFC 4180 is two, which keeps the parity), so that is the test. In standard CSV,
	// backslashes are ordinary characters, not quote escapes.
	inQuotes := false
	for i, line := range strings.Split(text, "\n") {
		if !inQuotes && strings.HasPrefix(line, "#") {
			if rest, ok := strings.CutPrefix(line, zoneDeclaration); ok {
				zone = strings.TrimSpace(rest)
			}
			continue
		}
		body = append(body, line)
		fileLine = append(fileLine, i+1)
		if strings.Count(line, `"`)%2 == 1 {
			inQuotes = !inQuotes
		}
	}
	rd := csv.NewReader(strings.NewReader(strings.Join(body, "\n")))
	rd.FieldsPerRecord = -1
	var records [][]string
	var recordLine []int
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.unreadable(path, "the export is not the comma-separated shape the "+kind+" parser reads")
			return s
		}
		// FieldPos is where this record STARTED in the stripped text, which the map above
		// turns back into the line the reader would count to in the file itself.
		n := len(records) + 1
		if len(rec) > 0 {
			if at, _ := rd.FieldPos(0); at >= 1 && at <= len(fileLine) {
				n = fileLine[at-1]
			}
		}
		records = append(records, rec)
		recordLine = append(recordLine, n)
	}
	if len(records) == 0 {
		s.unreadable(path, "the export is not the comma-separated shape the "+kind+" parser reads")
		return s
	}
	header := records[0]
	stampCol, dateCol, modelCol := -1, -1, -1
	types := map[int]Type{}
	var reports []Type
	for i, col := range header {
		col = strings.TrimSpace(col)
		switch {
		case col == sh.stamp:
			stampCol = i
		case col == sh.date:
			dateCol = i
		case col == sh.model:
			modelCol = i
		default:
			t, ok := sh.columns[col]
			if !ok {
				s.unreadable(path, "the "+kind+" parser does not know the column "+col+" in: "+strings.Join(header, ",")+"; it reads "+strings.Join(ParserColumns(kind), ", ")+", and a file of another provider's shape is declared by ITS kind")
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
		n := recordLine[i+1]
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

// xaiJSONColumns maps the grok usage JSON's camelCase turn fields to the five token types
// the xAI parser already reports from the CSV shape. totalTokens is a derived sum, and
// modelCalls/turnCount are not token spend, so neither is a column: an unknown field is
// ignored rather than admitted as a seventh type. costUsdTicks is not token spend either
// -- it is the turn's COST, and it folds into the message's Usd below, never into a type.
var xaiJSONColumns = map[string]Type{
	"inputTokens":         Input,
	"outputTokens":        Output,
	"cacheCreationTokens": CacheWrite,
	"cachedReadTokens":    CacheRead,
	"reasoningTokens":     Reasoning,
}

// readXaiJSON folds the documented `grok usage` JSON shape into the same rows the CSV
// shape produces. Each turn is one message: its endedAt is the UTC day, primaryModelId is
// the model, and the five token fields are the counts. A turn field the source did not
// carry is an absence (a dash), never a zero, exactly as the CSV parser reads a blank
// cell. A document with none of the expected keys is UNREADABLE naming both shapes.
func readXaiJSON(kind, path, text string, s *Source) *Source {
	reports := []Type{Input, Output, CacheWrite, CacheRead, Reasoning}
	sortTypes(reports)
	s.Reports = reports
	s.Basis = UTC

	var root map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		s.unreadable(path, providerJSONReason(kind))
		return s
	}
	turnsRaw, ok := root["turns"]
	if !ok {
		s.unreadable(path, providerJSONReason(kind))
		return s
	}
	turns, ok := turnsRaw.([]interface{})
	if !ok {
		s.unreadable(path, providerJSONReason(kind))
		return s
	}
	for i, e := range turns {
		turn, ok := e.(map[string]interface{})
		if !ok {
			s.unparsed(path, i+1, "a turn that is not a JSON object")
			continue
		}
		endedAt, ok := turn["endedAt"].(string)
		if !ok {
			s.unparsed(path, i+1, "a turn whose endedAt is not an RFC 3339 string")
			continue
		}
		tm, err := time.Parse(time.RFC3339, endedAt)
		if err != nil {
			s.unparsed(path, i+1, "the turn's endedAt is not RFC 3339")
			continue
		}
		model, _ := turn["primaryModelId"].(string)
		m := Message{
			Day:   tm.UTC().Format(dayLayout),
			Basis: UTC,
			Model: model,
			Repo:  Unattributed,
		}
		for field, t := range xaiJSONColumns {
			v, ok := turn[field].(json.Number)
			if !ok {
				continue
			}
			if n, err := strconv.ParseInt(v.String(), 10, 64); err == nil {
				m.Counts.Set(t, n)
			}
		}
		// The turn's cost is its costUsdTicks: an integer count of micro-dollar
		// ticks, the unit the fold's usd= holds (rule 20: the cost is "from the
		// usage `usd` column or a cost tick the source reported"). A lexeme that
		// is not a non-negative integer is an absence rather than a guess, and
		// usd= is 0 where no source reported one.
		if v, ok := turn["costUsdTicks"].(json.Number); ok {
			if n, err := strconv.ParseInt(v.String(), 10, 64); err == nil && n >= 0 {
				m.Usd = n
			}
		}
		s.Stream = append(s.Stream, m)
	}
	return s
}

// providerJSONReason is the refusal for a JSON document that has none of the expected
// keys: it names both shapes the parser reads rather than guessing which one was meant.
func providerJSONReason(kind string) string {
	return "the export is neither the comma-separated shape nor the grok usage JSON shape (a sessionId and a turns array) the " + kind + " parser reads"
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
