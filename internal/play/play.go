// Package play implements shared reading annotations.
// Notes are anchored to passages in a source text, with separate authors,
// timestamps, and threaded replies. A changed source produces an explicit
// anchor conflict rather than silently moving notes.
package play

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Note is a single annotation anchored to a passage.
type Note struct {
	ID        string    // sha12 of author+passage+note at creation
	Author    string    // who wrote this
	Passage   string    // the anchored passage text
	Note      string    // the annotation text
	CreatedAt time.Time // when this was written
	Replies   []Reply   // threaded replies
}

// Reply is a response to a Note.
type Reply struct {
	ID        string // sha12 of author+note at creation
	Author    string
	Note      string
	CreatedAt time.Time
}

// Anchor holds the source hash used to detect stale anchors.
type Anchor struct {
	SourcePath string // path to the source text
	SourceHash string // sha256 of the source at annotation time
}

// Store is the on-disk format for annotations.
type Store struct {
	Anchor  Anchor `json:"anchor"`
	Notes   []Note `json:"notes"`
	Version int    `json:"version"`
}

// HashSource returns the sha256 hex of the source file contents.
func HashSource(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// NoteFile returns the annotation file path for a given source.
func NoteFile(source string) string {
	dir := filepath.Dir(source)
	base := filepath.Base(source)
	return filepath.Join(dir, base+".notes")
}

// LoadStore reads the annotation store for a source.
// Returns an empty store if the file does not exist.
func LoadStore(source string) (*Store, error) {
	nf := NoteFile(source)
	b, err := os.ReadFile(nf)
	if os.IsNotExist(err) {
		return &Store{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}

	content := strings.ReplaceAll(string(b), "\r\n", "\n")
	if strings.HasSuffix(content, "\n") {
		content = content[:len(content)-1]
	}
	lines := strings.Split(content, "\n")
	s := &Store{Version: 1}
	if len(lines) < 1 || !strings.HasPrefix(lines[0], "ANCHOR ") {
		return s, nil
	}
	rest := strings.TrimPrefix(lines[0], "ANCHOR ")
	if idx := strings.LastIndex(rest, " "); idx >= 0 {
		s.Anchor.SourcePath = rest[:idx]
		s.Anchor.SourceHash = rest[idx+1:]
	} else {
		s.Anchor.SourcePath = rest
	}

	var currentNote *Note
	var currentReply *Reply
	var currentField *string
	hasPassage := false
	hasBody := false
	hasReplyBody := false

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if currentField == nil && (line == "" || strings.HasPrefix(line, "#")) {
			continue
		}
		switch {
		case strings.HasPrefix(line, "NOTE "):
			n := parseNoteLine(line)
			s.Notes = append(s.Notes, n)
			currentNote = &s.Notes[len(s.Notes)-1]
			currentReply = nil
			currentField = nil
			hasPassage = false
			hasBody = false
			hasReplyBody = false
		case !hasPassage && (line == "PASSAGE" || strings.HasPrefix(line, "PASSAGE ")) && currentNote != nil:
			if len(line) > len("PASSAGE") {
				currentNote.Passage = line[len("PASSAGE")+1:]
			} else {
				currentNote.Passage = ""
			}
			currentField = &currentNote.Passage
			hasPassage = true
		case !hasBody && (line == "BODY" || strings.HasPrefix(line, "BODY ")) && currentNote != nil:
			if len(line) > len("BODY") {
				currentNote.Note = line[len("BODY")+1:]
			} else {
				currentNote.Note = ""
			}
			currentField = &currentNote.Note
			hasBody = true
		case strings.HasPrefix(line, "REPLY ") && currentNote != nil:
			r := parseReplyLine(line)
			currentNote.Replies = append(currentNote.Replies, r)
			currentReply = &currentNote.Replies[len(currentNote.Replies)-1]
			currentField = nil
			hasReplyBody = false
		case !hasReplyBody && (line == "REPLY_BODY" || strings.HasPrefix(line, "REPLY_BODY ")) && currentReply != nil:
			if len(line) > len("REPLY_BODY") {
				currentReply.Note = line[len("REPLY_BODY")+1:]
			} else {
				currentReply.Note = ""
			}
			currentField = &currentReply.Note
			hasReplyBody = true
		default:
			if currentField != nil {
				*currentField += "\n" + line
			}
		}
	}
	return s, nil
}

// SaveStore writes the store to the annotation file.
func SaveStore(source string, s *Store) error {
	nf := NoteFile(source)
	var b strings.Builder
	fmt.Fprintf(&b, "ANCHOR %s %s\n", s.Anchor.SourcePath, s.Anchor.SourceHash)
	for _, n := range s.Notes {
		writeNote(&b, n)
	}
	return os.WriteFile(nf, []byte(b.String()), 0o644)
}

func parseNoteLine(line string) Note {
	// NOTE <id> author=<author> passage=<passage_sha12> created=<ts>
	rest := strings.TrimPrefix(line, "NOTE ")
	n := Note{}
	for _, part := range splitFields(rest) {
		if strings.HasPrefix(part, "id=") {
			n.ID = strings.TrimPrefix(part, "id=")
		} else if strings.HasPrefix(part, "author=") {
			n.Author = strings.TrimPrefix(part, "author=")
		} else if strings.HasPrefix(part, "created=") {
			t, _ := time.Parse(time.RFC3339, strings.TrimPrefix(part, "created="))
			n.CreatedAt = t
		}
	}
	return n
}

func parseReplyLine(line string) Reply {
	rest := strings.TrimPrefix(line, "REPLY ")
	r := Reply{}
	for _, part := range splitFields(rest) {
		if strings.HasPrefix(part, "id=") {
			r.ID = strings.TrimPrefix(part, "id=")
		} else if strings.HasPrefix(part, "author=") {
			r.Author = strings.TrimPrefix(part, "author=")
		} else if strings.HasPrefix(part, "created=") {
			t, _ := time.Parse(time.RFC3339, strings.TrimPrefix(part, "created="))
			r.CreatedAt = t
		}
	}
	return r
}

func splitFields(s string) []string {
	var fields []string
	var current strings.Builder
	inQuote := false
	for _, r := range s {
		switch r {
		case '"':
			inQuote = !inQuote
			current.WriteRune(r)
		case ' ':
			if inQuote {
				current.WriteRune(r)
			} else {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields
}

func writeNote(b *strings.Builder, n Note) {
	fmt.Fprintf(b, "NOTE id=%s author=%s created=%s\n",
		n.ID, n.Author, n.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(b, "PASSAGE %s\n", n.Passage)
	fmt.Fprintf(b, "BODY %s\n", n.Note)
	for _, r := range n.Replies {
		fmt.Fprintf(b, "REPLY id=%s author=%s created=%s\n",
			r.ID, r.Author, r.CreatedAt.Format(time.RFC3339))
		fmt.Fprintf(b, "REPLY_BODY %s\n", r.Note)
	}
}

// sha12 returns the first 12 hex chars of sha256(data).
func sha12(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:6])
}

// Annotate adds a note to the store. It verifies the passage exists in the
// source and records the source hash for stale-anchor detection.
func Annotate(source, author, passage, note string) (Note, error) {
	// Verify passage is in the source.
	src, err := os.ReadFile(source)
	if err != nil {
		return Note{}, fmt.Errorf("source: %w", err)
	}
	if !strings.Contains(string(src), passage) {
		return Note{}, fmt.Errorf("passage not found in source: %q", passage)
	}

	s, err := LoadStore(source)
	if err != nil {
		return Note{}, err
	}

	// Check anchor consistency.
	if s.Anchor.SourceHash != "" {
		currentHash, err := HashSource(source)
		if err != nil {
			return Note{}, err
		}
		if currentHash != s.Anchor.SourceHash {
			return Note{}, fmt.Errorf("ANCHOR STALE source=%s stored=%s current=%s (edit the source since last annotation)",
				source, s.Anchor.SourceHash, currentHash)
		}
	}

	// Record/update anchor.
	hash, err := HashSource(source)
	if err != nil {
		return Note{}, err
	}
	s.Anchor.SourcePath = source
	s.Anchor.SourceHash = hash

	now := time.Now().UTC()
	n := Note{
		ID:        sha12(author + passage + note),
		Author:    author,
		Passage:   passage,
		Note:      note,
		CreatedAt: now,
	}

	s.Notes = append(s.Notes, n)
	if err := SaveStore(source, s); err != nil {
		return Note{}, err
	}
	return n, nil
}

// ReadNotes returns all notes for a source, checking anchor consistency.
func ReadNotes(source string) ([]Note, string, error) {
	s, err := LoadStore(source)
	if err != nil {
		return nil, "", err
	}

	var anchorStatus string
	if s.Anchor.SourceHash != "" {
		currentHash, err := HashSource(source)
		if err != nil {
			return nil, "", err
		}
		if currentHash != s.Anchor.SourceHash {
			anchorStatus = fmt.Sprintf("ANCHOR STALE source=%s stored=%s current=%s",
				source, s.Anchor.SourceHash, currentHash)
		} else {
			anchorStatus = "ANCHOR OK"
		}
	}
	return s.Notes, anchorStatus, nil
}

// ReplyTo adds a reply to an existing note.
func ReplyTo(source, noteID, author, note string) (Reply, error) {
	s, err := LoadStore(source)
	if err != nil {
		return Reply{}, err
	}

	// Check anchor consistency.
	if s.Anchor.SourceHash != "" {
		currentHash, err := HashSource(source)
		if err != nil {
			return Reply{}, fmt.Errorf("source: %w", err)
		}
		if currentHash != s.Anchor.SourceHash {
			return Reply{}, fmt.Errorf("ANCHOR STALE source=%s stored=%s current=%s",
				source, s.Anchor.SourceHash, currentHash)
		}
	} else {
		if _, err := HashSource(source); err != nil {
			return Reply{}, fmt.Errorf("source: %w", err)
		}
	}

	// Find the note.
	for i, n := range s.Notes {
		if n.ID == noteID {
			now := time.Now().UTC()
			r := Reply{
				ID:        sha12(author + note),
				Author:    author,
				Note:      note,
				CreatedAt: now,
			}
			s.Notes[i].Replies = append(s.Notes[i].Replies, r)
			if err := SaveStore(source, s); err != nil {
				return Reply{}, err
			}
			return r, nil
		}
	}
	return Reply{}, fmt.Errorf("note not found: %s", noteID)
}

// Export returns a portable markdown-formatted string of all annotations for
// a source, with authors, timestamps, threads, and anchor status.
func Export(source string) (string, error) {
	notes, anchorStatus, err := ReadNotes(source)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Annotations\n\n")
	if anchorStatus != "" {
		b.WriteString(fmt.Sprintf("Anchor: %s\n\n", anchorStatus))
	}
	b.WriteString(fmt.Sprintf("Source: %s\n\n", source))

	for i, n := range notes {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		b.WriteString(fmt.Sprintf("## Note %s by %s (%s)\n\n", n.ID, n.Author, n.CreatedAt.Format(time.RFC3339)))
		b.WriteString(fmt.Sprintf("> %s\n\n", n.Passage))
		b.WriteString(fmt.Sprintf("%s\n", n.Note))
		for _, r := range n.Replies {
			b.WriteString(fmt.Sprintf("\n  **%s** (%s) id=%s: %s\n", r.Author, r.CreatedAt.Format(time.RFC3339), r.ID, r.Note))
		}
	}
	return b.String(), nil
}
