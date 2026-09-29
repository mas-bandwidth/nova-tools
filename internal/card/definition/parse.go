package definition

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

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
type Definition struct {
	File         string
	ContractSHA  string // the sha= token of the contract line, "" when absent
	ContractNote string // the text after it on the contract line
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

var contractShaRE = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// Parse reads an array of card files. It returns one Definition per file, in
// order, and no refusals; or no definitions and every refusal the array drew.
// A file is read by the rules in the package comment. Parse never panics and never
// reads a file or the network: its input is the bytes it is given.
func Parse(files []Source) ([]Definition, []Refusal) {
	if len(files) == 0 {
		return nil, []Refusal{{Operation: OpParse, Cause: CauseEmptyArray,
			Found: "no card files", Next: "pass at least one card file; a single card is an array of one"}}
	}
	if len(files) > MaxFiles {
		return nil, []Refusal{{Operation: OpParse, Cause: CauseTooManyFiles,
			Found: fmt.Sprintf("%d card files, the limit is %d", len(files), MaxFiles),
			Next:  fmt.Sprintf("narrow the request to at most %d files; nothing is chunked for you", MaxFiles)}}
	}
	total := 0
	for _, f := range files {
		total += len(f.Data)
	}
	if total > MaxTotalBytes {
		return nil, []Refusal{{Operation: OpParse, Cause: CauseTotalTooLarge,
			Found: fmt.Sprintf("%d bytes across the array, the limit is %d", total, MaxTotalBytes),
			Next:  "narrow the request to fewer or smaller files"}}
	}
	var refs []Refusal
	var defs []Definition
	seen := map[string]int{}
	for i, f := range files {
		label := f.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
			refs = append(refs, Refusal{Operation: OpParse, File: label, Cause: CauseFileName,
				Found: "the file has no name", Next: "give every card file a name (its path in the repository)"})
			continue
		}
		if j, dup := seen[f.Name]; dup {
			refs = append(refs, Refusal{Operation: OpParse, File: label, Cause: CauseDuplicateFile,
				Found: fmt.Sprintf("file %q is entry %d and entry %d of the array", f.Name, j+1, i+1),
				Next:  "pass each card file once"})
			continue
		}
		seen[f.Name] = i
		d, rs := parseFile(label, f.Data)
		refs = append(refs, rs...)
		if len(rs) == 0 {
			defs = append(defs, d)
		}
	}
	if len(refs) > 0 {
		return nil, refs
	}
	return defs, nil
}

type fileLine struct {
	text  string
	start int
}

func splitLines(s string) []fileLine {
	var out []fileLine
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

// byteRefusals checks what the whole file must be before any line is read.
func byteRefusals(name string, data []byte) []Refusal {
	s := string(data)
	var out []Refusal
	add := func(line int, c Cause, found, next string) {
		out = append(out, Refusal{Operation: OpParse, File: name, Line: line, Cause: c, Found: found, Next: next})
	}
	switch {
	case len(data) > MaxCardBytes:
		add(0, CauseFileTooLarge, fmt.Sprintf("%d bytes, the limit is %d", len(data), MaxCardBytes), "shorten the card; a brief is a brief")
		return out
	case len(data) == 0:
		add(1, CauseEmptyFile, "the file is empty", "write the contract line, the header and the brief")
		return out
	}
	if strings.HasPrefix(s, "\xef\xbb\xbf") {
		add(1, CauseBOM, "the file starts with a byte-order mark", "save the file as UTF-8 without a byte-order mark")
	}
	for off := 0; off < len(s); {
		r, size := utf8.DecodeRuneInString(s[off:])
		if r == utf8.RuneError && size == 1 {
			add(lineAt(s, off), CauseInvalidUTF8, fmt.Sprintf("byte 0x%02x at offset %d is not valid UTF-8", s[off], off), "save the file as UTF-8")
			break
		}
		off += size
	}
	if i := strings.IndexByte(s, '\r'); i >= 0 {
		add(lineAt(s, i), CauseCarriageReturn, "a carriage return (CRLF or bare CR line ending)",
			"save the file with LF line endings; the pinned bytes are the identity, so one card is one byte sequence")
	}
	if i := strings.IndexByte(s, 0); i >= 0 {
		add(lineAt(s, i), CauseNUL, "a NUL byte", "remove the NUL byte")
	}
	return out
}

// parseContract reads line 1: `RESULT: <id> sha=<hex> <note>` or the colon-less
// `RESULT <id> sha=<hex> <note>`. It returns the ID, the sha and the note.
func parseContract(l string) (id, sha, note string, cause Cause, why string) {
	var rest string
	switch {
	case strings.HasPrefix(l, "RESULT: "):
		rest = l[len("RESULT: "):]
	case strings.HasPrefix(l, "RESULT "):
		rest = l[len("RESULT "):]
	default:
		return "", "", "", CauseContractLine, "line 1 is not a contract line: it is `RESULT: <id> sha=<hex>`"
	}
	if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return "", "", "", CauseContractLine, "the contract line names no card ID"
	}
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		end = len(rest)
	}
	id, after := rest[:end], strings.TrimLeft(rest[end:], " \t")
	if strings.HasPrefix(id, "sha=") {
		return "", "", "", CauseContractLine, "the contract line names no card ID before its sha="
	}
	if strings.HasPrefix(after, "sha=") {
		tok := after
		if k := strings.IndexAny(after, " \t"); k >= 0 {
			tok = after[:k]
		}
		after = strings.TrimLeft(after[len(tok):], " \t")
		sha = tok[len("sha="):]
		if !contractShaRE.MatchString(sha) {
			return id, "", "", CauseContractSHA, fmt.Sprintf("sha=%s is not 7 to 40 hexadecimal digits", sha)
		}
	}
	return id, sha, strings.TrimRight(after, " \t"), "", ""
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

// looseHeaderLine reports whether a line that is not a KEY: value line is a case
// or spacing variant of a known key (`kind: x`, `KIND : x`, ` KIND: x`,
// `DEPENDS_ON: x`), and which key it varies.
func looseHeaderLine(t string) (string, bool) {
	i := strings.IndexByte(t, ':')
	if i <= 0 {
		return "", false
	}
	k := t[:i]
	for j := 0; j < len(k); j++ {
		c := k[j]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '\t' || c == '_' || c == '-'
		if !ok {
			return "", false
		}
	}
	canon, ok := looseKnown[looseKey(k)]
	return canon, ok
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// parseFile reads one card file.
func parseFile(name string, data []byte) (Definition, []Refusal) {
	if rs := byteRefusals(name, data); len(rs) > 0 {
		return Definition{}, rs
	}
	s := string(data)
	ls := splitLines(s)
	var refs []Refusal
	add := func(line int, key string, c Cause, found, next string) {
		refs = append(refs, Refusal{Operation: OpParse, File: name, Line: line, Key: key, Cause: c, Found: found, Next: next})
	}

	d := Definition{File: name, Lines: map[string]int{}, Digest: sum(data)}
	contractID, sha, note, cc, why := parseContract(ls[0].text)
	if why != "" {
		add(1, "", cc, why, "write line 1 as `RESULT: <id> sha=<hex>`, the contract line the swarm cards carry")
	}
	d.ContractSHA, d.ContractNote = sha, note

	raw := map[string]string{}
	i := 1
	for ; i < len(ls); i++ {
		t, n := ls[i].text, i+1
		if strings.TrimSpace(t) == "" {
			continue
		}
		key, val, ok := cardhdr.KeyValue(t)
		if ok {
			if !knownKey[key] {
				if canon, loose := looseKnown[looseKey(key)]; loose {
					add(n, key, CauseAmbiguousSpelling, fmt.Sprintf("%q is a variant of %s", key, canon), "spell the key exactly "+canon)
				} else {
					add(n, key, CauseUnknownKey, fmt.Sprintf("%q is not a key of the v2 profile", key),
						"remove the line, or start the brief with a line that is not KEY: value (a heading is one)")
				}
				continue
			}
			if first, dup := d.Lines[key]; dup {
				add(n, key, CauseDuplicateKey, fmt.Sprintf("%s is declared on lines %d and %d", key, first, n), "keep one "+key+" line")
				continue
			}
			d.Lines[key] = n
			raw[key] = val
			continue
		}
		if canon, loose := looseHeaderLine(t); loose {
			add(n, strings.TrimSpace(t[:strings.IndexByte(t, ':')]), CauseAmbiguousSpelling,
				fmt.Sprintf("this line is a variant of %s (case, spacing or a leading blank)", canon), "spell the key exactly "+canon+" at column zero, no blank before the colon")
			continue
		}
		break // the first nonblank line that is not KEY: value ends the header
	}
	if i >= len(ls) {
		add(len(ls), "", CauseNoBrief, "the header runs to the end of the file", "add the brief below the header")
	} else {
		d.Brief = s[ls[i].start:]
		d.BriefLine = i + 1
		d.BriefDigest = sum([]byte(d.Brief))
		stranded := strandedKeys(ls[i:], i)
		keys := make([]string, 0, len(stranded))
		for k := range stranded {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool { return stranded[keys[a]] < stranded[keys[b]] })
		for _, k := range keys {
			add(stranded[k], k, CauseStranded,
				fmt.Sprintf("%s: is on line %d, below the header; the brief starts on line %d", k, stranded[k], d.BriefLine),
				"move the line into the header block, directly under the contract line, or indent or fence it in the brief")
		}
		for _, k := range requiredKeys {
			if _, in := raw[k]; !in && stranded[k] == 0 {
				add(0, k, CauseRequiredMissing, "no "+k+": line in the header", "add `"+k+": <value>` to the header block")
			}
		}
	}

	skip := map[string]bool{keyBrief: true}
	for _, k := range requiredKeys {
		if _, in := raw[k]; !in {
			skip[k] = true // named above as missing or stranded
		}
	}
	bad := func(key string, c Cause, found, next string) {
		add(d.Lines[key], key, c, found, next)
		skip[key] = true
	}
	for _, k := range append([]string{KeyEntry}, requiredKeys...) {
		v, in := raw[k]
		if !in {
			continue
		}
		switch {
		case strings.TrimSpace(v) == "":
			bad(k, CauseEmptyValue, k+" has no value", "write a value after "+k+":")
		case hasControl(v):
			bad(k, CauseInvalidValue, k+" holds a control character", "keep the value to printable text on one line")
		}
	}

	setText := func(key string, dst *string) {
		if v, in := raw[key]; in && !skip[key] {
			*dst = v
		}
	}
	setText(KeySchema, &d.Schema)
	setText(KeyID, &d.ID)
	setText(KeyEntry, &d.Entry)
	setText(KeyTitle, &d.Title)
	setText(KeyKind, &d.Kind)
	setText(KeyTier, &d.Tier)
	setText(KeyDoneWhen, &d.DoneWhen)
	setText(KeyDoors, &d.Doors)
	setText(KeyProbes, &d.Probes)
	if v, in := raw[KeyPaths]; in && !skip[KeyPaths] {
		p, w := parsePaths(v)
		if w != "" {
			bad(KeyPaths, CauseInvalidPaths, w, "write `PATHS: none` or up to eight repository-relative globs, comma-separated")
		}
		d.Paths = p
	}
	if v, in := raw[KeyDependsOn]; in && !skip[KeyDependsOn] {
		p, w := parseDepends(v)
		if w != "" {
			bad(KeyDependsOn, CauseInvalidDependsOn, w, "write `DEPENDS-ON: -` or comma-separated card IDs")
		}
		d.DependsOn = p
	}
	if v, in := raw[KeyTest]; in && !skip[KeyTest] {
		tl, w := cardhdr.ParseTest(v)
		if w != "" {
			bad(KeyTest, CauseInvalidTest, w, "write `TEST: <package> <TestName>`, or `TEST: none <why>` for a kind that completes without a pull request")
		}
		d.Test = tl
	}
	if d.ID != "" && contractID != "" && d.ID != contractID && idWhy(d.ID) == "" {
		bad(KeyID, CauseIDMismatch, fmt.Sprintf("the ID header is %q and the contract line (line 1) names %q", d.ID, contractID),
			"make the ID header and the contract line name the same ID")
	}
	if contractID != "" && idWhy(contractID) != "" {
		add(1, "", CauseContractLine, "the contract line's ID: "+idWhy(contractID), "use an ID of ASCII letters, digits, underscore and hyphen")
	}
	refs = append(refs, checkDefinition(OpParse, d, skip, d.Lines, name)...)
	if len(refs) > 0 {
		return Definition{}, refs
	}
	return d, nil
}

// strandedKeys finds the profile keys at column zero in the body, outside fenced
// blocks: line numbers by key, the first of each. offset is the index of the first
// body line in the file.
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
		if key, _, ok := cardhdr.KeyValue(l.text); ok && knownKey[key] {
			if _, seen := out[key]; !seen {
				out[key] = offset + j + 1
			}
		}
	}
	return out
}
