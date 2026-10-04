package bus

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// PrepareReplyFrom is PrepareReply with the machine the reply is posted from. An empty
// host writes no Host line at all, which is the reply this tool has always written, byte
// for byte; PrepareReply is that call and is kept so a caller with no host reads as one.
func PrepareReplyFrom(t *Bus, me Participant, original *Note, body string, now time.Time, host string) (Prepared, error) {
	var p Prepared
	normBody := strings.TrimSpace(NormalizeBody(body))
	if normBody == "" {
		return p, errors.New("the note has no body")
	}
	if normBody == PlaceholderBody || ContainsPlaceholderBody(body) {
		return p, fmt.Errorf("the body is the unedited template placeholder (%s)", PlaceholderBody)
	}
	if host != "" {
		if err := ValidHost(host); err != nil {
			return p, err
		}
	}
	if me.Lane == "" {
		return p, fmt.Errorf("%q has no lane on this bus, so has nowhere to send from", me.Name)
	}
	target := original.Header.ID
	if target == "" {
		target = original.Path
	}
	subject := original.Header.Subject
	if !IsReplySubject(subject) {
		subject = ReplyPrefix + subject
	}
	h := Header{
		From:    me.Name,
		Host:    host,
		To:      original.Header.From,
		Re:      []string{target},
		Subject: subject,
		Date:    now.UTC().Format(DateLayout),
	}
	n := Note{Header: h, Body: body, Lane: me.Lane}
	id, err := AssignID(t.Config, me, n.Header, n.Body, n.Header.Date)
	if err != nil {
		return p, err
	}
	if other, clash := t.NoteByID(id); clash {
		return p, fmt.Errorf("the id %q is already on this bus, on %s", id, other.Path)
	}
	n.Header.ID = id
	n.Path = me.Lane + "/" + FileName(now, Slugify(n.Header.Subject, SlugMax), id)
	if _, taken := t.NoteByPath(n.Path); taken {
		return p, fmt.Errorf("%s already exists", n.Path)
	}
	return Prepared{
		Note:    n,
		Sender:  me,
		Path:    n.Path,
		Index:   IndexEntryFor(t.Config, n),
		Message: me.Slug() + ": " + n.Header.Subject,
	}, nil
}
