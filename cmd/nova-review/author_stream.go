package main

import (
	"encoding/base64"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

// authorIntentResult distinguishes a source which could not be read or decoded
// (so getAuthorIntent follows its existing fallback) from a complete source
// whose required, quoted data cannot fit in the caller's packet budget.
type authorIntentResult struct {
	title string
	body  string
	err   error
}

type authorIntentTooLarge struct{ maxBytes int }

func (e authorIntentTooLarge) Error() string {
	return fmt.Sprintf("author title and body cannot fit --max-bytes %d", e.maxBytes)
}

// ghAuthorJQ turns the two JSON strings into two newline-free scalar fields.
// Missing and null match json.Unmarshal into Go strings; other JSON types make
// gh fail, which preserves getAuthorIntent's existing Git fallback.
const ghAuthorJQ = `[.title,.body]|map(if . == null then "" elif type == "string" then @base64 else error("title/body must be strings") end)|join("\n")`

// trimmedText streams the byte result of strings.TrimSpace. Leading whitespace
// is dropped, and trailing whitespace is deferred until a later non-space rune
// proves it is internal. pending retains at most limit bytes; a larger deferred
// run is harmless at EOF but makes the field too large if text follows it.
type trimmedText struct {
	limit int

	out              []byte
	pending          []byte
	partial          []byte // at most utf8.UTFMax-1 bytes of an incomplete rune
	started          bool
	pendingOverLimit bool
	tooLarge         bool
}

func newTrimmedText(limit int) *trimmedText { return &trimmedText{limit: limit} }

func (t *trimmedText) Write(data []byte) (int, error) {
	original := len(data)
	for len(data) > 0 {
		if len(t.partial) != 0 {
			var joined [utf8.UTFMax]byte
			n := copy(joined[:], t.partial)
			take := utf8.UTFMax - n
			if take > len(data) {
				take = len(data)
			}
			n += copy(joined[n:], data[:take])
			if !utf8.FullRune(joined[:n]) {
				t.partial = append(t.partial, data[:take]...)
				data = data[take:]
				continue
			}
			r, width := utf8.DecodeRune(joined[:n])
			t.accept(joined[:width], r)
			fromPartial := width
			if fromPartial > len(t.partial) {
				fromPartial = len(t.partial)
			}
			t.partial = t.partial[fromPartial:]
			data = data[width-fromPartial:]
			continue
		}
		if !utf8.FullRune(data) {
			t.partial = append(t.partial, data...)
			break
		}
		r, width := utf8.DecodeRune(data)
		t.accept(data[:width], r)
		data = data[width:]
	}
	return original, nil
}

func (t *trimmedText) accept(raw []byte, r rune) {
	space := unicode.IsSpace(r)
	if !t.started {
		if space {
			return
		}
		t.started = true
	}
	if space {
		if t.tooLarge {
			return
		}
		if t.pendingOverLimit || len(raw) > t.limit-len(t.out)-len(t.pending) {
			t.pendingOverLimit = true
			return
		}
		t.pending = append(t.pending, raw...)
		return
	}
	if t.tooLarge || t.pendingOverLimit || len(raw) > t.limit-len(t.out)-len(t.pending) {
		t.tooLarge = true
		t.pending = t.pending[:0]
		return
	}
	t.out = append(t.out, t.pending...)
	t.pending = t.pending[:0]
	t.out = append(t.out, raw...)
}

func (t *trimmedText) Finish() {
	for len(t.partial) != 0 {
		r, width := utf8.DecodeRune(t.partial)
		t.accept(t.partial[:width], r)
		t.partial = t.partial[width:]
	}
	// Pending whitespace is terminal and strings.TrimSpace removes it, including
	// an over-limit run which was never retained.
	t.pending = t.pending[:0]
	t.pendingOverLimit = false
}

func (t *trimmedText) String() string { return string(t.out) }

// base64Text accepts one jq @base64 scalar a quartet at a time, without ever
// retaining its encoded line. jq's standard encoding has no newline bytes.
type base64Text struct {
	text    *trimmedText
	pending []byte
	padded  bool
	err     error
}

func newBase64Text(limit int) *base64Text {
	return &base64Text{text: newTrimmedText(limit)}
}

func (b *base64Text) byte(c byte) {
	if b.err != nil {
		return
	}
	if !base64ScalarByte(c) {
		b.err = fmt.Errorf("base64 scalar contains byte 0x%02x outside its alphabet", c)
		return
	}
	if b.padded {
		b.err = fmt.Errorf("base64 data follows padding")
		return
	}
	b.pending = append(b.pending, c)
	if len(b.pending) != 4 {
		return
	}
	var decoded [3]byte
	n, err := base64.StdEncoding.Strict().Decode(decoded[:], b.pending)
	if err != nil {
		b.err = err
		return
	}
	for _, c := range b.pending {
		if c == '=' {
			b.padded = true
			break
		}
	}
	_, _ = b.text.Write(decoded[:n])
	b.pending = b.pending[:0]
}

func base64ScalarByte(c byte) bool {
	return c >= 'A' && c <= 'Z' ||
		c >= 'a' && c <= 'z' ||
		c >= '0' && c <= '9' ||
		c == '+' || c == '/' || c == '='
}

func (b *base64Text) finish() error {
	if b.err != nil {
		return b.err
	}
	if len(b.pending) != 0 {
		return fmt.Errorf("base64 scalar ends mid-quartet")
	}
	b.text.Finish()
	return nil
}

// ghAuthorStream expects two raw jq base64 fields, title then body, separated
// by LF and optionally followed by gh's final LF. It records framing errors but
// continues draining stdout so a command failure still takes the existing Git
// fallback rather than a premature size/framing result.
type ghAuthorStream struct {
	fields [2]*base64Text
	field  int
	err    error
}

func newGHAuthorStream(limit int) *ghAuthorStream {
	return &ghAuthorStream{fields: [2]*base64Text{newBase64Text(limit), newBase64Text(limit)}}
}

func (s *ghAuthorStream) Write(data []byte) (int, error) {
	for _, c := range data {
		if s.field == 2 {
			if s.err == nil {
				s.err = fmt.Errorf("gh author scalar has data after its final line")
			}
			continue
		}
		if c == '\n' {
			if err := s.fields[s.field].finish(); err != nil && s.err == nil {
				s.err = err
			}
			s.field++
			continue
		}
		s.fields[s.field].byte(c)
	}
	return len(data), nil
}

func (s *ghAuthorStream) finish() error {
	if s.field == 0 {
		return fmt.Errorf("gh author scalar has no title/body separator")
	}
	if s.field == 1 {
		if err := s.fields[1].finish(); err != nil && s.err == nil {
			s.err = err
		}
		s.field++
	}
	if s.err != nil {
		return s.err
	}
	return nil
}

// gitAuthorStream splits precisely where strings.Cut(out, "\n\n") did. It
// deliberately treats every other byte, including invalid UTF-8 and CR, as
// current Git output does before the two independent TrimSpace calls.
type gitAuthorStream struct {
	title, body *trimmedText
	inBody      bool
	oneNewline  bool
}

func newGitAuthorStream(limit int) *gitAuthorStream {
	return &gitAuthorStream{title: newTrimmedText(limit), body: newTrimmedText(limit)}
}

func (s *gitAuthorStream) Write(data []byte) (int, error) {
	for _, c := range data {
		if s.inBody {
			_, _ = s.body.Write([]byte{c})
			continue
		}
		if s.oneNewline {
			if c == '\n' {
				s.inBody = true
				s.oneNewline = false
				continue
			}
			_, _ = s.title.Write([]byte{'\n'})
			s.oneNewline = false
		}
		if c == '\n' {
			s.oneNewline = true
			continue
		}
		_, _ = s.title.Write([]byte{c})
	}
	return len(data), nil
}

func (s *gitAuthorStream) finish() {
	if !s.inBody && s.oneNewline {
		_, _ = s.title.Write([]byte{'\n'})
	}
	s.title.Finish()
	s.body.Finish()
}

func materializeAuthor(title, body *trimmedText, maxBytes int) authorIntentResult {
	if title.tooLarge || body.tooLarge {
		return authorIntentResult{err: authorIntentTooLarge{maxBytes: maxBytes}}
	}
	t, b := title.String(), body.String()
	if len(formatThisHead(t, b)) > maxBytes {
		return authorIntentResult{err: authorIntentTooLarge{maxBytes: maxBytes}}
	}
	return authorIntentResult{title: t, body: b}
}

func streamGHAuthorIntent(timeout time.Duration, hostRepo string, pr, maxBytes int) (authorIntentResult, bool) {
	stream := newGHAuthorStream(maxBytes)
	err := sourceOutputTo(timeout, "", packetGHBinary, stream,
		"pr", "view", fmt.Sprint(pr), "--repo", hostRepo, "--json", "title,body", "--jq",
		ghAuthorJQ)
	if err != nil {
		return authorIntentResult{}, false
	}
	if err := stream.finish(); err != nil {
		return authorIntentResult{}, false
	}
	return materializeAuthor(stream.fields[0].text, stream.fields[1].text, maxBytes), true
}

func streamGitAuthorIntent(timeout time.Duration, repo, head string, maxBytes int) (authorIntentResult, bool) {
	stream := newGitAuthorStream(maxBytes)
	if err := sourceOutputTo(timeout, repo, packetGitBinary, stream, "log", "-1", "--format=%s%n%n%b", head); err != nil {
		return authorIntentResult{}, false
	}
	stream.finish()
	return materializeAuthor(stream.title, stream.body, maxBytes), true
}
