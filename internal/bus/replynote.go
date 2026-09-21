package bus

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// PrepareReply builds the note that answers an existing one, with every header the reply
// verb fills taken from the original and NONE from the caller: From is the caller, To is
// the original's From, Re names the original by id (or path, for a note older than ids),
// and Subject is the original's subject with one `Re: ` in front, not stacked. It is the
// step that makes a reply impossible to hand-shape: a caller supplies the body and nothing
// else, so there is no header line to get wrong.
func PrepareReply(t *Bus, me Participant, original *Note, body string, now time.Time) (Prepared, error) {
	var p Prepared
	normBody := strings.TrimSpace(NormalizeBody(body))
	if normBody == "" {
		return p, errors.New("the note has no body")
	}
	if normBody == PlaceholderBody || ContainsPlaceholderBody(body) {
		return p, fmt.Errorf("the body is the unedited template placeholder (%s)", PlaceholderBody)
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
