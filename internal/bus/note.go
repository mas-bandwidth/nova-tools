package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// The header keys this tool knows. An unknown key is a refusal at send and a FAIL at
// check, because the failure it guards is real and silent: a note whose "Sbuject:" line
// was accepted as prose has no subject, and a note whose "Rf:" line was accepted has no
// thread.
const (
	KeyFrom    = "From"
	KeyTo      = "To"
	KeyCc      = "Cc"
	KeyDate    = "Date"
	KeyID      = "Id"
	KeyRe      = "Re"
	KeySubject = "Subject"
	KeyKind    = "Kind"
)

// KnownKeys is the header's whole vocabulary, in the order send writes it. It is a list
// rather than a set because a refusal names it: a writer told only that their key is
// unknown has to go and find the eight that are not, and the eight fit on the line.
var KnownKeys = []string{KeyFrom, KeyTo, KeyCc, KeyDate, KeyID, KeyRe, KeySubject, KeyKind}

// KindReceipt and KindNote are the two values of the optional Kind line, which overrides
// the receipt heuristic in either direction.
const (
	KindReceipt = "receipt"
	KindNote    = "note"
)

// DateLayout is how send writes the Date line: the same shape `date -u` prints, which is
// what the table's people have been pasting by hand, so a tool-written note and a
// hand-written one read alike.
const DateLayout = "Mon Jan  2 15:04:05 UTC 2006"

// FileTimeLayout is the UTC minute in a note's filename.
const FileTimeLayout = "2006-01-02T1504Z"

// headingPrefix and bulletPrefix are the two presentation shapes ParseNote steps over: a
// markdown heading above the header, and a bullet on each header line.
const (
	headingPrefix = "# "
	bulletPrefix  = "- "
)

// maxQuotedKey is how much of an unknown header key a refusal quotes. A file whose first
// line is prose makes the whole first sentence look like a key, and a check over a table
// of them would otherwise print a paragraph per note.
const maxQuotedKey = 40

// idHexLen is how much of the sha256 an id carries. Twelve hex is 48 bits, INSIDE a
// per-sender namespace, over a table whose lifetime is thousands of notes rather than
// billions -- and send additionally refuses an id already on the table, so a collision is
// a refusal a person reads rather than a note that overwrites another.
const idHexLen = 12

// Header is a note's header, with the author's own text preserved. Only Date and Id are
// ever written by this tool; From, To, Cc, Re and Subject are the author's words and are
// round-tripped verbatim, because "Rowan (bud, the Studio, the mas account)" carries
// information the roster does not hold.
type Header struct {
	From    string
	To      string
	Cc      string
	Date    string
	ID      string
	Re      []string
	Subject string
	Kind    string

	// lines records the 1-based line number each key was read at, so a check failure can
	// name the line rather than the file.
	lines map[string]int
}

// Note is one parsed note file.
type Note struct {
	// Path is repo-relative and slash-separated: the form a Re line uses.
	Path   string
	Lane   string
	Header Header
	Body   string

	// Parse is set, and everything above it but Path and Lane is empty, when the file
	// would not parse. A table with one bad file stays usable: check names it, and every
	// other rule steps over it rather than guessing at what it meant.
	Parse *ParseError
}

// ParseNote parses a note's text. The header is every line before the first blank line;
// the body is everything after it.
//
// TWO TOLERANCES, for the two shapes a real table writes that a strict reader loses. Both
// are enumerated here, in SPEC.md, and pinned by a test; anything else is still a refusal.
//
//  1. A markdown HEADING before the header. A note whose file opens `# The subject` and
//     then, after a blank line, `From:` is the commonest unreadable shape on a table
//     people also read in a browser. The heading and the blank lines under it are skipped
//     and the header is read from the first line after them. Line numbers still count
//     from the top of the FILE, so a refusal names the line a person would open to.
//  2. A BULLET on each header line: `- From:`, `- To:`. One writer's notes are markdown
//     lists, and the bullet is presentation. A leading "- " is dropped from a header line
//     before the key is read.
//
// A note whose first line is prose still fails, and should: there is no honest way to
// tell a From line from a sentence that happens to hold a colon.
//
// WHAT THE REFUSALS SAY. A read of the family's own table found three shapes behind
// nearly every unreadable note, and a refusal that only says a file will not parse leaves
// the writer to guess which. So each of the three names its repair: a key in markdown
// bold (`**To**:`) is told that headers are plain `Key: value`; an unknown key (`Branch:`)
// is given the eight keys there are; and a body sentence standing in the header position
// is told the header ends at the first blank line. The failure is unchanged -- these are
// the same refusals with the fix in them.
func ParseNote(path, text string) (Note, error) {
	noteParses.Add(1)
	n := Note{Path: path}
	if i := strings.IndexByte(path, '/'); i > 0 {
		n.Lane = path[:i]
	}
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	// Tolerance 1. Only at the very top of the file, and only over blank lines: a heading
	// in the middle of a header is not a heading, it is a broken note.
	first := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], headingPrefix) {
		first = 1
		for first < len(lines) && strings.TrimSpace(lines[first]) == "" {
			first++
		}
	}

	h := Header{lines: make(map[string]int)}
	end := len(lines)
	for i := first; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			end = i
			break
		}
		// Tolerance 2.
		if rest, cut := strings.CutPrefix(line, bulletPrefix); cut {
			line = rest
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key {
			return n, fmt.Errorf("line %d: not a header line (a header is Key: value): %s", i+1, blankLineAdvice)
		}
		// A key nobody could have meant as a key is a BODY SENTENCE standing where the
		// header is, which on the real table is the commonest unreadable shape there is:
		// somebody wrote a paragraph, and a colon or a dash inside it made the first
		// clause look like a key. Saying "unknown header key" to that is true and useless,
		// so it says the thing that fixes it instead.
		if isProseKey(key) {
			return n, fmt.Errorf("line %d: %q is a sentence, not a header key: %s", i+1, truncate(key, maxQuotedKey), blankLineAdvice)
		}
		// A key in markdown bold -- `**To**: Rowan` -- is a table people also read in a
		// browser writing what it reads. It is one substitution away from correct and the
		// refusal says which.
		if plain, bold := unbold(key); bold {
			return n, fmt.Errorf("line %d: %q: headers are plain `Key: value`, not markdown bold; write %q", i+1, truncate(key, maxQuotedKey), plain+":")
		}
		value = strings.TrimSpace(value)
		switch key {
		case KeyRe:
			if value == "" {
				return n, fmt.Errorf("line %d: empty %s line", i+1, KeyRe)
			}
			h.Re = append(h.Re, value)
			if _, seen := h.lines[key]; !seen {
				h.lines[key] = i + 1
			}
			continue
		case KeyFrom, KeyTo, KeyCc, KeyDate, KeyID, KeySubject, KeyKind:
			if _, dup := h.lines[key]; dup {
				return n, fmt.Errorf("line %d: a second %s line", i+1, key)
			}
		default:
			// The key is quoted SHORT. A file whose first line is a paragraph has that
			// whole paragraph up to its first colon as the "key", and a refusal that
			// pasted it back would be one unreadable line per note in a check over a
			// table of them.
			//
			// The known keys are NAMED. `Branch:` is a real line off the real table, and
			// a writer told only that their key is unknown has to go and find the eight
			// that are not; they fit on the line, so they are on it.
			return n, fmt.Errorf("line %d: unknown header key %q (the keys are %s)", i+1, truncate(key, maxQuotedKey), strings.Join(KnownKeys, ", "))
		}
		h.lines[key] = i + 1
		switch key {
		case KeyFrom:
			h.From = value
		case KeyTo:
			h.To = value
		case KeyCc:
			h.Cc = value
		case KeyDate:
			h.Date = value
		case KeyID:
			h.ID = value
		case KeySubject:
			h.Subject = value
		case KeyKind:
			h.Kind = value
		}
	}
	if end == first {
		return n, fmt.Errorf("line %d: the file begins with a blank line, so it has no header", first+1)
	}
	if end < len(lines) {
		n.Body = strings.Join(lines[end+1:], "\n")
	}
	n.Header = h
	return n, nil
}

// blankLineAdvice is the one sentence that fixes every note whose header ran on into its
// body, which is most of the unreadable notes on a table people wrote by hand. It is
// shared by the two refusals that mean it so the two cannot say it differently.
const blankLineAdvice = "the header ends at the first blank line; put a blank line after the last header"

// isProseKey reports whether what stands where a key should stand is a sentence. A header
// key is one word: it holds no space, and it is short. Both halves are needed -- a
// paragraph up to its first colon holds spaces, and a colon that never arrives leaves a
// whole line -- and neither is a guess about what the writer meant, only about what they
// cannot have meant.
func isProseKey(key string) bool {
	return strings.ContainsAny(key, " \t") || len(key) > maxKeyLen
}

// maxKeyLen is the longest a header key can be before it is prose. The longest key this
// tool knows is "Subject", at seven; the margin is for a key somebody invents, which gets
// the unknown-key refusal and its list rather than the sentence one.
const maxKeyLen = 20

// unbold takes markdown emphasis off a key and says whether there was any. `**To**`,
// `**To` and `*To*` all arrive on a table whose notes are read in a browser, and all
// three are one substitution from a header.
func unbold(key string) (string, bool) {
	plain := strings.Trim(key, "*")
	if plain == "" || plain == key {
		return key, false
	}
	return plain, true
}

// LineOf reports the 1-based line a key was read at, or 0.
func (h Header) LineOf(key string) int { return h.lines[key] }

// Validate holds the rules every note obeys, whether it is being sent now or was written
// by hand a week ago. It does NOT check the Id line: a note without one is legacy and is
// addressed by path, which is the whole of the compatibility promise.
func (h Header) Validate(c *Config) error {
	if h.From == "" {
		return fmt.Errorf("no %s line", KeyFrom)
	}
	if _, ok := c.ResolveOne(h.From); !ok {
		return fmt.Errorf("%s: %q names no one at this table (known: %s)", KeyFrom, h.From, strings.Join(c.KnownNames(), "; "))
	}
	if strings.TrimSpace(h.To) == "" {
		return fmt.Errorf("no %s line", KeyTo)
	}
	to, unknown := c.ResolveList(h.To)
	if len(unknown) > 0 {
		return fmt.Errorf("%s: %s names no one at this table (known: %s)", KeyTo, quoteAll(UnknownNames(unknown)), strings.Join(c.KnownNames(), "; "))
	}
	if len(to) == 0 {
		return fmt.Errorf("%s: no recipients", KeyTo)
	}
	if h.Cc != "" {
		if _, unknownCc := c.ResolveList(h.Cc); len(unknownCc) > 0 {
			return fmt.Errorf("%s: %s names no one at this table (known: %s)", KeyCc, quoteAll(UnknownNames(unknownCc)), strings.Join(c.KnownNames(), "; "))
		}
	}
	if strings.TrimSpace(h.Subject) == "" {
		return fmt.Errorf("no %s line, or an empty one", KeySubject)
	}
	if h.Kind != "" && h.Kind != KindReceipt && h.Kind != KindNote {
		return fmt.Errorf("%s: %q is neither %q nor %q", KeyKind, h.Kind, KindReceipt, KindNote)
	}
	if h.ID != "" {
		if err := ValidID(h.ID); err != nil {
			return fmt.Errorf("%s: %w", KeyID, err)
		}
	}
	return nil
}

// Recipients is every name in To and Cc, resolved, with which line named them.
func (h Header) Recipients(c *Config) (to, cc []string) {
	to, _ = c.ResolveList(h.To)
	if h.Cc != "" {
		cc, _ = c.ResolveList(h.Cc)
	}
	return to, cc
}

// ValidID is the id's shape: a lane slug, a hyphen, and twelve lower-case hex digits.
func ValidID(id string) error {
	if len(id) < idHexLen+2 {
		return fmt.Errorf("%q is not <sender>-<%d hex>", id, idHexLen)
	}
	slug, hexPart := id[:len(id)-idHexLen-1], id[len(id)-idHexLen:]
	if id[len(id)-idHexLen-1] != '-' {
		return fmt.Errorf("%q is not <sender>-<%d hex>", id, idHexLen)
	}
	if err := validLane("from-" + slug); err != nil {
		return fmt.Errorf("%q: the sender half is not a lane slug", id)
	}
	for _, r := range hexPart {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return fmt.Errorf("%q: the hash half is not %d lower-case hex digits", id, idHexLen)
	}
	return nil
}

// SlugOfID is the sender half of an id.
func SlugOfID(id string) string {
	if ValidID(id) != nil {
		return ""
	}
	return id[:len(id)-idHexLen-1]
}

// AssignID computes the id for a note about to be sent.
//
// THE ID SCHEME, and why this one. The id is the sender's lane slug, a hyphen, and the
// first twelve hex digits of a sha256 over a canonical rendering of the note: the sender,
// the date the tool is about to write, the resolved recipients, the Re targets, the
// subject, the kind, and the normalized body.
//
// Why a hash and not a counter. A counter is shared state on a table whose whole problem
// is shared state: two senders writing in the same second would read the same counter and
// assign the same number, which is the collision this id exists to remove, and resolving
// it would need exactly the lock the table does not have. A hash is computed with no
// knowledge of anyone else's notes, so two lines racing cannot collide, and the id can be
// assigned before the first fetch.
//
// Why the whole canonical note and not the body alone. A body alone would give one sender
// writing "Heard, thank you" twice the same id -- which is a real event on a table of
// receipts, and would make the second note unsendable rather than merely unremarkable.
// The date is in the preimage at second granularity, so the same sender saying the same
// thing in two different seconds gets two ids, and a genuine collision means the same
// sender sent the same note to the same people in the same second, which is one note.
//
// Why the recipients are RESOLVED before hashing: so that a note whose To line says
// "Rowan Claude" and one whose To line says "Rowan a1b2c3d4" are not accidentally
// different notes at the id layer while being the same note to every reader.
//
// The id is rename-proof by construction: it is written into the file at send and is
// never recomputed. Renaming the file, moving it, or fixing its slug changes nothing a
// Re line depends on -- which is the failure this replaces, where a slug typo orphaned an
// answer.
func AssignID(c *Config, sender Participant, h Header, body string, date string) (string, error) {
	if sender.Lane == "" {
		return "", fmt.Errorf("%q has no lane, so cannot send", sender.Name)
	}
	to, cc := h.Recipients(c)
	sum := sha256.Sum256([]byte(canonical(sender.Name, date, to, cc, h.Re, h.Subject, h.Kind, body)))
	return sender.Slug() + "-" + hex.EncodeToString(sum[:])[:idHexLen], nil
}

// canonical is the id's preimage, written out so a reader can recompute an id by hand and
// so a change to it is a change a diff shows.
func canonical(from, date string, to, cc, re []string, subject, kind, body string) string {
	var b strings.Builder
	b.WriteString("nova-bus id v1\n")
	b.WriteString("from: " + from + "\n")
	b.WriteString("date: " + date + "\n")
	b.WriteString("to: " + strings.Join(to, "; ") + "\n")
	b.WriteString("cc: " + strings.Join(cc, "; ") + "\n")
	b.WriteString("re: " + strings.Join(re, "; ") + "\n")
	b.WriteString("subject: " + strings.TrimSpace(subject) + "\n")
	b.WriteString("kind: " + kind + "\n")
	b.WriteString("body:\n")
	b.WriteString(NormalizeBody(body))
	return b.String()
}

// NormalizeBody is the body as the id sees it: CRLF folded, trailing whitespace off every
// line, and no trailing blank lines. Editors add and remove exactly these, and an id that
// changed when an editor saved the file would not be an id.
func NormalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// Render writes a note back out in the canonical header order: From, To, Cc, Date, Id,
// Re..., Subject, a blank line, the body. The author's own text is preserved in every
// line but Date and Id.
func (n Note) Render() string {
	var b strings.Builder
	h := n.Header
	b.WriteString(KeyFrom + ": " + h.From + "\n")
	b.WriteString(KeyTo + ": " + h.To + "\n")
	if h.Cc != "" {
		b.WriteString(KeyCc + ": " + h.Cc + "\n")
	}
	b.WriteString(KeyDate + ": " + h.Date + "\n")
	b.WriteString(KeyID + ": " + h.ID + "\n")
	for _, re := range h.Re {
		b.WriteString(KeyRe + ": " + re + "\n")
	}
	if h.Kind != "" {
		b.WriteString(KeyKind + ": " + h.Kind + "\n")
	}
	b.WriteString(KeySubject + ": " + h.Subject + "\n")
	b.WriteString("\n")
	b.WriteString(NormalizeBody(n.Body))
	b.WriteString("\n")
	return b.String()
}

// FileName is the note's filename: the UTC minute, the slug, and the hash half of the id.
//
// The minute is the table's existing convention and is for people. The id's hash half is
// appended because the minute alone collided -- one sender writing twice inside one
// minute overwrote their own note -- and because a name carrying the id lets a person
// find a note by the id they were given without opening anything.
func FileName(now time.Time, slug, id string) string {
	hexPart := id
	if i := strings.LastIndexByte(id, '-'); i >= 0 {
		hexPart = id[i+1:]
	}
	return now.UTC().Format(FileTimeLayout) + "-" + slug + "-" + hexPart + ".md"
}

// Slugify turns a subject into the filename's human half. It is deterministic and lossy on
// purpose: the slug is for people, and the id is for machines.
func Slugify(subject string, max int) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(subject) {
		switch {
		case unicode.IsLetter(r) && r < 128, unicode.IsDigit(r) && r < 128:
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if max > 0 && len(s) > max {
		s = s[:max]
		if i := strings.LastIndexByte(s, '-'); i > 0 {
			s = s[:i]
		}
	}
	if s == "" {
		s = "note"
	}
	return s
}

// receiptWords are the words a bare acknowledgement is built from.
var receiptWords = []string{"heard", "received", "receipt", "ack", "acked", "acknowledged", "acknowledge", "noted"}

// IsReceipt decides whether a note only acknowledges.
//
// THE HEURISTIC, stated so it can be argued with: a note is a receipt when its Kind line
// says so; a note is a note when its Kind line says so; and with no Kind line, a note is a
// receipt when its body is UNDER maxWords words, contains one of the acknowledgement words
// (heard, received, receipt, ack, acked, acknowledged, acknowledge, noted), and contains
// no question mark. The word count comes from the caller because it is a property of how a
// table writes, not of this tool -- a table of two-line notes and a table of essays do not
// share a threshold, and a number this tool supplied would make a guess look like a
// measurement.
//
// It is a heuristic and it is wrong sometimes, in both directions. That is why the Kind
// line exists and why it wins: the override is one line in the header, costs the writer
// nothing, and is the only thing here that is not a guess.
func IsReceipt(n Note, maxWords int) bool {
	switch n.Header.Kind {
	case KindReceipt:
		return true
	case KindNote:
		return false
	}
	body := NormalizeBody(n.Body)
	if strings.Contains(body, "?") {
		return false
	}
	if len(strings.Fields(body)) >= maxWords {
		return false
	}
	low := strings.ToLower(body)
	for _, w := range receiptWords {
		if containsWord(low, w) {
			return true
		}
	}
	return false
}

// containsWord looks for a whole word, so "unreceived" and "hearing" are not receipts.
func containsWord(s, word string) bool {
	for i := 0; i+len(word) <= len(s); i++ {
		if s[i:i+len(word)] != word {
			continue
		}
		if i > 0 && isWordByte(s[i-1]) {
			continue
		}
		if j := i + len(word); j < len(s) && isWordByte(s[j]) {
			continue
		}
		return true
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// truncate shortens s to at most max BYTES on a rune boundary, marking that it did. It is
// never shortened to nothing: a reason a person cannot read is not a record.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func quoteAll(ss []string) string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, fmt.Sprintf("%q", s))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// noteParses counts every call to ParseNote, and NoteParses reads it.
//
// It is INSTRUMENTATION, and the only thing that reads it is a test. It is here rather
// than in a test file because the property it measures is a property of this package and
// is asserted from another one: `inbox` parses the notes that are new plus the notes this
// reader has open, and NO OTHER NOTE, whatever the table's history holds. That claim is
// about work not done, and work not done leaves no output to assert on -- so the only
// honest proof is a count taken at the one place the work happens. The alternative, timing
// two runs, is a flake on a shared runner and proves nothing on a fast enough machine.
//
// The cost is one atomic add per note parsed, against a file read and a header walk.
var noteParses atomic.Int64

// NoteParses is how many notes this process has parsed. Tests take it before and after a
// run and assert on the difference; nothing else reads it and nothing branches on it.
func NoteParses() int64 { return noteParses.Load() }
