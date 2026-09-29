package definition

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Source is one card file as bytes: its name (a path, used only to name the file
// in refusals and to key an array) and its content.
type Source struct {
	Name string
	Data []byte
}

// Definition is one card, read. It is plain data: the header fields, the brief,
// the line each header key sits on, and the digests of the file and of the brief.
// A Definition is made only by reading bytes; nothing in the package takes one
// from a caller.
type Definition struct {
	File         string
	BaseCommit   string // the sha= token of the contract line: the commit the work starts from; "" when absent
	ContractNote string // the text after the contract line's ID and sha= token
	Schema       string
	ID           string
	Entry        string // "" when the card carries no ENTRY line
	Title        string
	Kind         string
	Paths        []string // empty for `PATHS: none`
	DependsOn    []string // empty for `DEPENDS-ON: -`
	Tier         string
	Test         cardhdr.TestLine
	DoneWhen     string
	Doors        string
	Probes       string
	Brief        string // every byte from the first line after the header to the end
	BriefLine    int    // the line the brief starts on
	Lines        map[string]int
	Digest       string // hex SHA-256 of the file's bytes
	BriefDigest  string // hex SHA-256 of the brief's bytes
}

// parse reads an array of card files. It returns one Definition per file, in
// order, and no refusals; or no definitions and every refusal the array drew (at
// most card.MaxRefusals, the rest counted). A file is read by the rules in the
// package comment. parse never panics and never reads a file or the network: its
// input is the bytes it is given.
func parse(files []Source) ([]Definition, *Refusals) {
	if len(files) == 0 {
		return nil, just(ref(OpParse, CauseEmptyArray, "", 0, "", "no card files", "at least 1", "pass at least one card file; a single card is an array of one"))
	}
	if len(files) > MaxFiles {
		return nil, just(ref(OpParse, CauseTooMany, "", 0, "", plural(len(files), "card file"), fmt.Sprintf("%d card files", MaxFiles),
			fmt.Sprintf("narrow the request to at most %d files; nothing is chunked for you", MaxFiles)))
	}
	total := 0
	for _, f := range files {
		total += len(f.Data)
	}
	if total > MaxTotalBytes {
		return nil, just(ref(OpParse, CauseTooLarge, "", 0, "", fmt.Sprintf("%d bytes across the array", total), fmt.Sprintf("%d bytes", MaxTotalBytes),
			"narrow the request to fewer or smaller files"))
	}
	c := &card.Collector{}
	var defs []Definition
	seen := map[string]int{}
	for i, f := range files {
		label := f.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
			c.Add(ref(OpParse, CauseRequired, label, 0, "", "the file has no name", "a name", "give every card file a name (its path in the repository)"))
			continue
		}
		if j, dup := seen[f.Name]; dup {
			c.Add(ref(OpParse, CauseDuplicateFile, label, 0, "", fmt.Sprintf("entry %d and entry %d of the array", j+1, i+1), "each file once", "pass each card file once"))
			continue
		}
		seen[f.Name] = i
		before := c.Len()
		d := parseFile(c, label, f.Data)
		if c.Len() == before {
			defs = append(defs, d)
		}
	}
	if err := c.Err(); err != nil {
		return nil, err
	}
	return defs, nil
}

type fileLine struct {
	text  string
	start int
}

func splitLines(s string) []fileLine {
	out := make([]fileLine, 0, strings.Count(s, "\n")+1)
	start := 0
	for start < len(s) {
		end := strings.IndexByte(s[start:], '\n')
		if end < 0 {
			out = append(out, fileLine{s[start:], start})
			break
		}
		out = append(out, fileLine{s[start : start+end], start})
		start += end + 1
	}
	return out
}

func lineAt(s string, off int) int { return 1 + strings.Count(s[:off], "\n") }

// byteRefusals adds what the whole file must be before any line is read, and
// reports whether the file may be read line by line.
func byteRefusals(c *card.Collector, name string, data []byte) bool {
	s := string(data)
	add := func(line int, cause Cause, found, limit, next string) {
		c.Add(ref(OpParse, cause, name, line, "", found, limit, next))
	}
	switch {
	case len(data) > MaxCardBytes:
		add(0, CauseTooLarge, fmt.Sprintf("%d bytes", len(data)), fmt.Sprintf("%d bytes", MaxCardBytes), "shorten the card; a brief is a brief")
		return false
	case len(data) == 0:
		add(1, CauseEmptyFile, "the file is empty", "a contract line, a header and a brief", "write the contract line, the header and the brief")
		return false
	}
	ok := true
	if strings.HasPrefix(s, "\xef\xbb\xbf") {
		add(1, CauseBOM, "the file starts with a byte-order mark", "no byte-order mark", "save the file as UTF-8 without a byte-order mark")
		ok = false
	}
	for off := 0; off < len(s); {
		r, size := utf8.DecodeRuneInString(s[off:])
		if r == utf8.RuneError && size == 1 {
			add(lineAt(s, off), CauseInvalidUTF8, fmt.Sprintf("byte 0x%02x at offset %d", s[off], off), "valid UTF-8", "save the file as UTF-8")
			ok = false
			break
		}
		off += size
	}
	if i := strings.IndexByte(s, '\r'); i >= 0 {
		add(lineAt(s, i), CauseCarriageReturn, "a carriage return (CRLF or bare CR line ending)", "LF line endings only",
			"save the file with LF line endings; the pinned bytes are the identity, so one card is one byte sequence")
		ok = false
	}
	if i := strings.IndexByte(s, 0); i >= 0 {
		add(lineAt(s, i), CauseControlChar, "a NUL byte", "no NUL byte", "remove the NUL byte")
		ok = false
	}
	return ok
}

var baseCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// contract is a parsed contract line.
type contract struct {
	id, base, note string
}

// parseContract reads line 1: `RESULT: <id>`, then optionally ` sha=<hex>`, the
// base commit the work starts from (40 or 64 lower-case hexadecimal characters),
// then optionally a note. The colon is required.
func parseContract(l string) (contract, *problem) {
	const fix = "write line 1 as `RESULT: <id> sha=<base commit>`; the colon is required and sha= is optional"
	if !strings.HasPrefix(l, "RESULT: ") {
		return contract{}, bad(CauseContractLine, card.Value(l), "line 1 is `RESULT: <id>`, with the colon", fix)
	}
	rest := l[len("RESULT: "):]
	if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return contract{}, bad(CauseContractLine, card.Value(l), "the contract line names a card ID", fix)
	}
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		end = len(rest)
	}
	var k contract
	k.id, rest = rest[:end], strings.TrimLeft(rest[end:], " \t")
	if strings.HasPrefix(k.id, "sha=") {
		return contract{}, bad(CauseContractLine, card.Value(l), "the contract line names a card ID before its sha=", fix)
	}
	if c, why := card.IDFault(k.id); c != "" {
		return contract{}, bad(CauseContractLine, card.Value(k.id), "the contract line's ID: "+why, "use an ID of ASCII letters, digits, underscore and hyphen")
	}
	if strings.HasPrefix(rest, "sha=") {
		tok := rest
		if i := strings.IndexAny(rest, " \t"); i >= 0 {
			tok = rest[:i]
		}
		rest = strings.TrimLeft(rest[len(tok):], " \t")
		k.base = tok[len("sha="):]
		if !baseCommitRE.MatchString(k.base) {
			return contract{}, bad(CauseContractSHA, card.Value(k.base), "sha= is the base commit: 40 or 64 lower-case hexadecimal characters", fix)
		}
	}
	k.note = strings.TrimRight(rest, " \t")
	if len(k.note) > MaxContractNoteBytes || card.TextFault(k.note, MaxContractNoteBytes) != "" {
		return contract{}, bad(CauseContractLine, card.Value(k.note), fmt.Sprintf("the note is one clean line of at most %d bytes", MaxContractNoteBytes), fix)
	}
	return k, nil
}

// fenceOf reads a fence marker at the start of a line (up to three spaces, then
// three or more backticks or tildes): the marker byte, its length and the rest.
func fenceOf(l string) (ch byte, n int, rest string) {
	i := 0
	for i < len(l) && i < 3 && l[i] == ' ' {
		i++
	}
	if i >= len(l) || (l[i] != '`' && l[i] != '~') {
		return 0, 0, ""
	}
	ch = l[i]
	j := i
	for j < len(l) && l[j] == ch {
		j++
	}
	if j-i < 3 {
		return 0, 0, ""
	}
	return ch, j - i, l[j:]
}

func sum(b []byte) string { return string(card.Sum(b)) }

// parseFile reads one card file, adding to c every refusal it draws.
func parseFile(c *card.Collector, name string, data []byte) Definition {
	if !byteRefusals(c, name, data) {
		return Definition{}
	}
	s := string(data)
	ls := splitLines(s)
	add := func(line int, key string, cause Cause, found, limit, next string) {
		if c.Full() {
			c.Skip()
			return
		}
		c.Add(ref(OpParse, cause, name, line, key, found, limit, next))
	}
	addp := func(line int, key string, p *problem) { add(line, key, p.cause, p.found, p.limit, p.next) }

	d := Definition{File: name, Lines: map[string]int{}, Digest: sum(data)}
	con, p := parseContract(ls[0].text)
	if p != nil {
		addp(1, "", p)
	}
	d.BaseCommit, d.ContractNote = con.base, con.note

	raw := map[string]string{}
	i := 1
	for ; i < len(ls); i++ {
		t, n := ls[i].text, i+1
		if strings.TrimSpace(t) == "" {
			continue
		}
		if key, val, ok := headerLine(t); ok {
			if !knownKey[key] {
				if c.Full() {
					c.Skip()
					continue
				}
				if canon, loose := looseKnown[looseKey(key)]; loose {
					add(n, key, CauseAmbiguousSpelling, "a variant of "+canon, "the exact spelling "+canon, "spell the key exactly "+canon)
					continue
				}
				add(n, key, CauseUnknownKey, "", "a key of the v2 profile",
					"remove the line, or start the brief with a line that is not an upper-case KEY: value line (a heading is one)")
				continue
			}
			if first, dup := d.Lines[key]; dup {
				add(n, key, CauseDuplicateKey, fmt.Sprintf("lines %d and %d", first, n), "each key once", "keep one "+key+" line")
				continue
			}
			d.Lines[key] = n
			raw[key] = val
			continue
		}
		if variant, canon, loose := looseHeaderLine(t); loose {
			add(n, variant, CauseAmbiguousSpelling, "a variant of "+canon+" (case, spacing or a leading blank)", "the exact spelling "+canon+" at column zero, no blank before the colon",
				"spell the key exactly "+canon)
			continue
		}
		break // the first nonblank line that is not a header line ends the header
	}

	stranded := map[string]int{}
	if i >= len(ls) {
		add(len(ls), "", CauseNoBrief, "the header runs to the end of the file", "a brief below the header", "add the brief below the header")
	} else {
		d.Brief = s[ls[i].start:]
		d.BriefLine = i + 1
		d.BriefDigest = sum([]byte(d.Brief))
		stranded = strandedKeys(ls[i:], i)
		keys := make([]string, 0, len(stranded))
		for k := range stranded {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool { return stranded[keys[a]] < stranded[keys[b]] })
		for _, k := range keys {
			add(stranded[k], k, CauseStranded, fmt.Sprintf("%s: on line %d, below the header", k, stranded[k]), fmt.Sprintf("the header, which ends before line %d", d.BriefLine),
				"move the line into the header block, directly under the contract line, or indent or fence it in the brief")
		}
		if strings.TrimSpace(d.Brief) == "" {
			add(d.BriefLine, "", CauseNoBrief, "the brief is empty", "a brief below the header", "add the brief below the header")
		}
	}
	for _, key := range requiredKeys {
		if _, in := raw[key]; !in && stranded[key] == 0 {
			add(0, key, CauseRequired, "", "a "+key+": line in the header", "add `"+key+": <value>` to the header block")
		}
	}

	failed := map[string]bool{}
	fail := func(key string, p *problem) {
		addp(d.Lines[key], key, p)
		failed[key] = true
	}
	var class Completion
	for _, key := range append([]string{KeyEntry}, requiredKeys...) {
		v, in := raw[key]
		if !in {
			continue
		}
		switch key {
		case KeySchema:
			switch {
			case v == "":
				fail(key, bad(CauseEmptyValue, "", "SCHEMA has a value", "write SCHEMA: v2"))
			case v == SchemaV2:
				d.Schema = v
			case v == "v3":
				fail(key, bad(CauseUnsupportedSchema, card.Value(v), "v3 is refused by name: the Work-path profile is not implemented", "write SCHEMA: v2 and card IDs in DEPENDS-ON"))
			default:
				fail(key, bad(CauseUnsupportedSchema, card.Value(v), "SCHEMA is v2", "write SCHEMA: v2"))
			}
		case KeyID:
			switch {
			case v == "":
				fail(key, bad(CauseEmptyValue, "", "ID has a value", "write the card ID after ID:"))
			default:
				if p := idProblem(key, v); p != nil {
					fail(key, p)
				} else if con.id != "" && v != con.id {
					fail(key, bad(CauseIDMismatch, "ID "+card.Value(v)+" and the contract line "+card.Value(con.id), "the ID header and the contract line (line 1) name the same ID",
						"make the ID header and the contract line name the same ID"))
				} else {
					d.ID = v
				}
			}
		case KeyEntry:
			if v == "" {
				fail(key, bad(CauseEmptyValue, "", "ENTRY has a value", "write a value after ENTRY:, or leave the line out"))
			} else if p := entryProblem(v); p != nil {
				fail(key, p)
			} else {
				d.Entry = v
			}
		case KeyTitle:
			if p := oneLineText(key, v, MaxTitleBytes); p != nil {
				fail(key, p)
			} else {
				d.Title = v
			}
		case KeyKind:
			if p := kindProblem(v); p != nil {
				fail(key, p)
			} else {
				d.Kind = v
				cs, _ := Classify([]string{v})
				class = cs[0]
			}
		case KeyPaths:
			if paths, p := parsePaths(v); p != nil {
				fail(key, p)
			} else {
				d.Paths = paths
			}
		case KeyDependsOn:
			if deps, p := parseDepends(v); p != nil {
				fail(key, p)
			} else {
				d.DependsOn = deps
			}
		case KeyTier:
			if p := tierProblem(v); p != nil {
				fail(key, p)
			} else {
				d.Tier = v
			}
		case KeyTest:
			tl, why := cardhdr.ParseTest(v)
			if why != "" {
				fail(key, bad(CauseInvalidTest, card.Value(v), short(why), "write `TEST: <package> <TestName>`, or `TEST: none <why>` for a kind that completes without a pull request"))
			} else if class == "" {
				d.Test = tl // the kind is refused already; its class decides `none`
			} else if p := testProblem(v, tl, class); p != nil {
				fail(key, p)
			} else {
				d.Test = tl
			}
		case KeyDoneWhen:
			if p := oneLineText(key, v, MaxProseBytes); p != nil {
				fail(key, p)
			} else {
				d.DoneWhen = v
			}
		case KeyDoors:
			if p := oneLineText(key, v, MaxDoorsBytes); p != nil {
				fail(key, p)
			} else if p := noneWord(key, v); p != nil {
				fail(key, p)
			} else {
				d.Doors = v
			}
		case KeyProbes:
			if p := oneLineText(key, v, MaxProseBytes); p != nil {
				fail(key, p)
			} else if p := noneWord(key, v); p != nil {
				fail(key, p)
			} else {
				d.Probes = v
			}
		}
	}
	return d
}

// strandedKeys finds the profile keys at column zero in the body, outside fenced
// blocks: line numbers by key, the first of each. offset is the index of the first
// body line in the file. A fence closes only on a marker of the same character and
// at least the opening length with no info string after it.
func strandedKeys(body []fileLine, offset int) map[string]int {
	out := map[string]int{}
	var fenceCh byte
	fenceN := 0
	for j, l := range body {
		if ch, n, rest := fenceOf(l.text); ch != 0 {
			switch {
			case fenceCh == 0:
				fenceCh, fenceN = ch, n
				continue
			case ch == fenceCh && n >= fenceN && strings.TrimSpace(rest) == "":
				fenceCh, fenceN = 0, 0
				continue
			}
		}
		if fenceCh != 0 {
			continue
		}
		if key, _, ok := headerLine(l.text); ok && knownKey[key] {
			if _, seen := out[key]; !seen {
				out[key] = offset + j + 1
			}
		}
	}
	return out
}
