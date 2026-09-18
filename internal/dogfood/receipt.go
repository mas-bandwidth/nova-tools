package dogfood

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Receipt is one person saying: I ran this verb, on this day, on real work, and
// here is how it went. It is the whole record — there is no separate state, and
// nothing here is derived, because a dogfood claim nobody wrote down is a
// dogfood claim nobody can check.
type Receipt struct {
	Tool  string `json:"tool"`
	Verb  string `json:"verb"`
	By    string `json:"by"`
	At    string `json:"at"` // RFC3339, UTC
	OK    bool   `json:"ok"` // did the verb do what the run needed
	Notes string `json:"notes"`
	Issue int    `json:"issue,omitempty"` // the edge filed, when there is one

	// File is where this receipt was read from. It is not part of the record —
	// a receipt does not know its own path — and it is here so a strand can be
	// NAMED: the dogfood pass of 2026-09-18 was told nine receipts matched
	// nothing and which nine was left to the reader to work out.
	File string `json:"-"`
}

// Key is the verb this receipt is about, in the ledger's identity. `--verb -`
// is the tool's bare invocation and normalizes to the empty verb, so a receipt
// can name a tool that has no verbs at all.
func (r Receipt) Key() string { return NormalizeKey(r.Tool, r.Verb) }

// edgeMarker is how the family writes an edge in a note: the word, then a
// colon. The 2026-09-18 pass wrote "Edges: (1) … (2) …" in six receipts and
// every one of them was recorded with --ok, because the verb DID work and the
// edges were beside it. Counting only ok=false said open-edges=0 about a bench
// that had found six things.
var edgeMarker = regexp.MustCompile(`(?i)(^|[^a-z])edges?:`)

// RecordsAnEdge reports whether this receipt found something: the verb did not
// do what the run needed, or the notes name an edge in the shape the family
// writes them. The prose is not read any further than that marker — what the
// edge IS stays a human's to read.
func (r Receipt) RecordsAnEdge() bool {
	return !r.OK || edgeMarker.MatchString(r.Notes)
}

// Filed reports whether the edge this receipt records has an issue somebody
// can act on.
func (r Receipt) Filed() bool { return r.Issue > 0 }

// Spelling is the verb as a line prints it, with the bare marker for a tool
// that has no verbs.
func (r Receipt) Spelling() string {
	v := strings.TrimSpace(r.Verb)
	if v == "" || v == BareVerb {
		return BareVerb
	}
	return v
}

// Time parses At. A receipt that has been through Validate always parses.
func (r Receipt) Time() time.Time {
	t, err := time.Parse(time.RFC3339, r.At)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// Failure is one named NO: the subject that failed and why. A failure means the
// records themselves are wrong, which is exit 1 at the CLI, never a silently
// shorter ledger.
type Failure struct {
	Subject string
	Reason  string
}

// MissingFieldError names one field a receipt cannot be written without, and
// the remedy: the flag that supplies it. One field, one line — the CLI prints
// every one of them in a single run, so a caller who omitted three learns
// about three.
type MissingFieldError struct {
	Field  string
	Remedy string
}

func (e *MissingFieldError) Error() string {
	return fmt.Sprintf("%s is required; refusing to guess: %s", e.Field, e.Remedy)
}

// Validate reports every field a receipt is missing or cannot hold, in a fixed
// order. A receipt is the evidence; evidence with a blank in it is not
// evidence, so nothing is defaulted here — not the clock, not the name.
func (r Receipt) Validate() []error {
	var errs []error
	miss := func(field, remedy string) {
		errs = append(errs, &MissingFieldError{Field: field, Remedy: remedy})
	}
	if strings.TrimSpace(r.Tool) == "" {
		miss("tool", "--tool <t> is the binary you ran, as docs/CLI.md spells it (nova-check)")
	}
	if strings.TrimSpace(r.Verb) == "" {
		miss("verb", "--verb <v> is the verb you ran, as docs/CLI.md spells it (links, or `lift quarantine`)")
	}
	if strings.TrimSpace(r.By) == "" {
		miss("by", "--by <name> is who ran it; a receipt with no name cannot be read as a non-author's")
	}
	if strings.TrimSpace(r.Notes) == "" {
		miss("notes", "--notes <text> is the real work you ran it on, in one line; it is what makes this a dogfood run and not a smoke test")
	}
	if strings.TrimSpace(r.At) == "" {
		miss("at", "the clock supplies at=; a receipt written by hand carries an RFC3339 UTC stamp")
	} else if _, err := time.Parse(time.RFC3339, r.At); err != nil {
		errs = append(errs, fmt.Errorf("at %q is not an RFC3339 UTC stamp: %v", r.At, err))
	}
	if r.Issue < 0 {
		errs = append(errs, fmt.Errorf("issue %d is not an issue number; leave it out when no edge was filed", r.Issue))
	}
	for _, f := range []struct{ name, value string }{
		{"tool", r.Tool}, {"verb", r.Verb}, {"by", r.By}, {"notes", r.Notes},
	} {
		if hasControl(f.value) {
			errs = append(errs, fmt.Errorf("%s holds a control character; a receipt is one line of record", f.name))
		}
	}
	return errs
}

func hasControl(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Record appends one receipt to the receipts directory and returns the file it
// wrote. The append is a new file per receipt, written to a temporary name in
// the same directory and renamed into place, because two benches recording at
// once must not interleave halves of two JSON lines into one file — the
// directory is the append-only log, and each file is one atomic entry.
func Record(dir string, r Receipt) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("receipts: no directory given; refusing to guess")
	}
	if errs := r.Validate(); len(errs) > 0 {
		return "", errs[0]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("receipts: %w", err)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("receipts: %w", err)
	}
	line = append(line, '\n')

	sum := sha256.Sum256(line)
	name := fmt.Sprintf("%s-%s-%s-%s-%s.json",
		strings.ReplaceAll(r.Time().Format("20060102T150405Z"), ":", ""),
		slug(r.Tool), slug(r.Verb), slug(r.By), hex.EncodeToString(sum[:])[:8])
	final := filepath.Join(dir, name)

	tmp, err := os.CreateTemp(dir, ".receipt-*.tmp")
	if err != nil {
		return "", fmt.Errorf("receipts: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has taken it away
	if _, err := tmp.Write(line); err != nil {
		tmp.Close()
		return "", fmt.Errorf("receipts: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("receipts: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("receipts: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return "", fmt.Errorf("receipts: %w", err)
	}
	return final, nil
}

// slug makes one filename-safe word out of a name that may hold spaces (a
// two-word verb) or characters a filesystem argues about. It never returns "".
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "x"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// ReadReceipts reads every receipt in a directory. A file holds one JSON
// object per line, so a receipts directory written by hand — or appended to by
// an older tool — reads the same as one Record wrote. Blank lines are skipped;
// anything else that will not parse, or will not validate, comes back as a
// failure naming the file and the line, and never as a quietly shorter ledger.
//
// The directory is read shallowly: subdirectories are somebody else's records.
func ReadReceipts(dir string) ([]Receipt, []Failure, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, errors.New("receipts: no directory given; refusing to guess")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("receipts: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("receipts: %q is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("receipts: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // the order is the filenames', so two runs read the same

	var (
		receipts []Receipt
		failures []Failure
	)
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, Failure{Subject: path, Reason: fmt.Sprintf("unreadable: %v", err)})
			continue
		}
		for i, raw := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			subject := fmt.Sprintf("%s:%d", path, i+1)
			var r Receipt
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&r); err != nil {
				failures = append(failures, Failure{Subject: subject, Reason: fmt.Sprintf("not a receipt: %v", err)})
				continue
			}
			if errs := r.Validate(); len(errs) > 0 {
				failures = append(failures, Failure{Subject: subject, Reason: errs[0].Error()})
				continue
			}
			r.File = subject
			receipts = append(receipts, r)
		}
	}
	return receipts, failures, nil
}

// RecordLine is what `record` prints once the receipt is on disk: the record
// as the ledger will read it, and the file it was written to.
func (r Receipt) RecordLine(path string) string {
	issue := "-"
	if r.Issue > 0 {
		issue = fmt.Sprintf("%d", r.Issue)
	}
	ok := "no"
	if r.OK {
		ok = "yes"
	}
	return fmt.Sprintf("DOGFOOD RECORD OK tool=%s verb=%s by=%s at=%s ok=%s issue=%s file=%s",
		r.Tool, r.Verb, r.By, r.At, ok, issue, path)
}
