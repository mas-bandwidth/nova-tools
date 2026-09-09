package bus

import (
	"fmt"
	"sort"
	"strings"
)

// This file is CLOSING: the half of the loop that takes a note back off a reader's open
// list. Everything else here is about getting a note onto one.
//
// It exists because of a line that could not close anything. A line on a small-context
// model read its inbox, answered every note by hand, and watched `carrying=` climb from
// nothing to seventy-four -- because a hand-written reply has no `Re:` line, and the
// answered rule is a Re line. He was answering; the bus could not tell. Nothing he could
// see said so, and every poll re-read the whole backlog to find the one note that was new.
//
// Two things close a note: a receipt, which is one command, and a reply that NAMES the
// note it answers. This file makes the second of those possible to write without going and
// looking up an id:
//
//	ReplySubject       what a subject means as a Re target, and what "the same subject" is
//	MatchOpenSubject   the open notes on a reader's list whose subject a Re line names
//	SingleOpenSender   the one correspondent a To line names, when they are waiting on you
//
// The rule they are all held to is the send-side rule in draft.go: a tolerance may only do
// what a person reading the draft would do WITHOUT GUESSING, and it must SAY what it did.
// A subject that matches an open note exactly is not an inference about what the writer
// meant; a subject that matches two is, so both are closed under one notice that names the
// id and says how to be exact. And a draft that answers nothing is never refused for it:
// plenty of notes answer nothing.

// ReplyPrefix is what a reply's subject is conventionally written with, and it is stripped
// from BOTH sides before two subjects are compared. `Re: the merge queue` names the note
// whose subject is `the merge queue`, and it also names the one whose subject is already
// `Re: the merge queue` -- which is the second turn of a thread and the case where a line
// writing by hand copies the subject it is looking at.
const ReplyPrefix = "Re: "

// ReplySubject is one subject reduced to what is compared: a leading `Re: ` removed, and
// the surrounding space with it.
//
// It is CASE-SENSITIVE past that point, on purpose and against the grain of most mail
// tools. A subject on this bus is a line of prose written by a person, `the gate` and
// `The Gate` are two different notes on a busy lane, and a tool that folded them would
// close the wrong one and say it had closed the right one. The prefix itself is matched
// case-sensitively for the same reason it is written that way everywhere else here.
func ReplySubject(s string) string {
	out := strings.TrimSpace(s)
	for {
		trimmed := strings.TrimPrefix(out, ReplyPrefix)
		if trimmed == out {
			// `Re:` with no space after it is the same word with a typo in the spacing,
			// and it is the shape a hand-written subject arrives in often enough to read.
			trimmed = strings.TrimPrefix(out, strings.TrimSpace(ReplyPrefix))
			if trimmed == out {
				return out
			}
		}
		out = strings.TrimSpace(trimmed)
	}
}

// IsReplySubject reports whether a subject is written as a reply's.
//
// This one IS case-insensitive, and it is the only thing here that is. It decides whether
// to print a NOTE -- a sentence suggesting the writer may have meant to name a note -- and
// a suggestion that fires on `RE:` as well as `Re:` costs a reader one line they can
// ignore, where missing it costs them the open note the whole file is about.
func IsReplySubject(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "re:")
}

// MatchOpenSubject is every entry on an open list whose subject the given Re target names,
// newest first.
//
// Newest FIRST because that is the one a sender means. Two notes with one subject on a
// reader's open list is a thread somebody re-raised, and the turn being answered is the
// last one; the older is named by id if it is meant. The caller closes the first and says
// so, with the count, so the choice is visible and correctable rather than silent.
func MatchOpenSubject(open []OpenEntry, target string) []OpenEntry {
	want := ReplySubject(target)
	if want == "" {
		return nil
	}
	var out []OpenEntry
	for _, e := range open {
		if e.Kind == OpenUnreadable {
			continue
		}
		if ReplySubject(e.Subject) == want {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].when(), out[j].when()
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].Path > out[j].Path
	})
	return out
}

// SingleOpenSender is the one correspondent a To line names when they are waiting on this
// reader, and "" in every other case: a To line naming two people, a To line naming
// somebody with nothing open, an unreadable To line, a group.
//
// It is the second half of the guess in `send`'s NOTE, and it is deliberately the narrow
// half. A note to one person who is holding an open note to you is, far more often than
// not, the answer to that note; a note to five people is a broadcast and says nothing about
// any of them.
func SingleOpenSender(c *Config, to string, open []OpenEntry) string {
	names, unresolved := c.ResolveList(to)
	if len(unresolved) > 0 || len(names) != 1 {
		return ""
	}
	for _, e := range open {
		if e.Kind == OpenUnreadable {
			continue
		}
		if e.From == names[0] {
			return names[0]
		}
	}
	return ""
}

// resolveReSubjects is the send-side tolerance itself, run over a draft's Re lines once the
// bus and the sender are known. It rewrites the header in place and returns its notices and
// its refusals.
//
// A Re line is tried as an id and as a path first, which is what it has always meant, so
// nothing that resolved before resolves differently now. A subject is only ever consulted
// for a target that names NOTHING on the bus, where the alternative was a refusal.
func resolveReSubjects(t *Bus, sender Participant, header *Header) (notices []string, problems []error) {
	var open []OpenEntry
	loaded := false
	for i, re := range header.Re {
		if re == "new" {
			continue
		}
		if _, found := t.Resolve(re); found {
			continue
		}
		if !loaded {
			loaded = true
			// An open list this tool cannot read is not a reason to refuse a send. It is
			// reported, loudly, by every `inbox` run this line makes; here it simply means
			// there is no list to match a subject against, and the Re line is refused for
			// naming nothing, which is what it did before this file existed.
			if sender.Lane != "" {
				if entries, err := ReadOpen(t.Root, sender.Lane); err == nil {
					open = entries
				}
			}
		}
		matches := MatchOpenSubject(open, re)
		if len(matches) == 0 {
			problems = append(problems, fmt.Errorf("%s: %q is not an id on this bus, not a note that exists, and not the subject of a note on your open list; threads are named by id, and a slug is not a thread", KeyRe, truncate(re, maxQuotedKey)))
			continue
		}
		target := matches[0].Target()
		header.Re[i] = target
		if len(matches) > 1 {
			notices = append(notices, fmt.Sprintf("%s: subject matched %d notes; closed the newest %s; name the id to be exact", KeyRe, len(matches), target))
			continue
		}
		notices = append(notices, fmt.Sprintf("%s named the subject %q rather than an id; it is the open note %s from %s, and this note closes it", KeyRe, truncate(re, maxQuotedKey), target, orDash(matches[0].From)))
	}
	return notices, problems
}

// answersNothingNotice is the sentence a draft with no Re line gets when it looks like a
// reply, and "" when it does not.
//
// WHAT IT IS FOR. Every note a line writes by hand answers nothing, because a Re line is
// not a thing anybody writes from memory. The reader on the other end therefore carries
// that note for ever: the answered rule is a Re line, and there is no other way for a
// reply to say which note it is a reply to. The line this was written for wrote
// seventy-four such answers and watched `carrying=` climb the whole time, with nothing
// anywhere telling him why.
//
// It is a NOTE and never a refusal. A note that answers nothing is an ordinary note and
// the commonest thing on the bus; what is being caught here is the narrow case where the
// draft itself says it is a reply -- its subject is written as one -- or where the only
// person it is addressed to is holding an open note from this line. Both are cheap to
// check and neither is a guess about what the writer meant, because nothing is changed.
func answersNothingNotice(c *Config, t *Bus, sender Participant, header Header) string {
	if len(header.Re) > 0 || sender.Lane == "" {
		return ""
	}
	looksLikeReply := IsReplySubject(header.Subject)
	if !looksLikeReply {
		open, err := ReadOpen(t.Root, sender.Lane)
		if err != nil {
			return ""
		}
		looksLikeReply = SingleOpenSender(c, header.To, open) != ""
	}
	if !looksLikeReply {
		return ""
	}
	return "this note answers nothing (no " + KeyRe + ": line); if it is a reply, name the note: " + KeyRe + ": <id>"
}
