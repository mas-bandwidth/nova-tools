package bus

import (
	"context"
	"errors"
	"io/fs"
)

// Position is where a reader stands in the bus's history: one commit, opaque to every
// caller. The empty Position is the beginning of history, before any note.
//
// It is a named string rather than a raw commit sha so that the Store interface, and every
// caller written against it, never grows a git argument or a hex-shaped assumption. See
// tla/BusCursor.tla: a cursor is a position, and the advance action moves it forward one
// commit at a time.
type Position string

// ErrConflict is what a publish returns when a note is already on the bus under the same
// path with different bytes. The bytes on the bus are the record; a publish that would
// rewrite them is refused rather than merged.
var ErrConflict = errors.New("the note already exists at that path with different bytes")

// ErrUnknownPosition is what Since returns when a position is not an ancestor of the head:
// after a history rewrite a cursor can name a commit that is gone, and a diff taken from it
// would report changes that are not changes. See tla/BusCursor.tla (BrokenNearerCursorWins).
var ErrUnknownPosition = errors.New("the position is not on this bus's history")

// Change is one publish's whole content: the notes, catalogues and bookkeeping one author
// asks the bus to accept as a single commit.
//
// It is fields and not paths because the bus is the layer, and the file layout is what the
// transport decides. Every note is create-only: the same bytes again is a retry, other
// bytes are ErrConflict.
type Change struct {
	// Author is whose lane the change lands in and whose identity authors the commit.
	Author Participant
	// Notes is the note files to add, keyed by repo-relative path. Paths outside the
	// author's lane are refused before anything is written.
	Notes map[string][]byte
	// Index is the lines this change adds to the author lane's INDEX.
	Index []IndexEntry
	// Receipts is the receipt lines this change appends to the author lane's RECEIPTS.
	Receipts []string
	// Cursor, when set, replaces the author lane's CURSOR. See tla/BusCursor.tla.
	Cursor *Cursor
	// Open, when set, replaces the author lane's OPEN list.
	Open *[]OpenEntry
	// Message is the commit message.
	Message string
	// Local, when set, commits without pushing: the publish is recorded on this checkout
	// and is not on the bus.
	Local bool
}

// Published is what a publish did.
type Published struct {
	// At is the commit the change landed on.
	At Position
	// Attempts is how many push attempts it took, from the transport's own count.
	Attempts int
	// Already is set when the change's bytes were already on the bus, so nothing was
	// written and nothing was pushed. A retry after a lost reply is Already.
	Already bool
}

// Store is the bus, apart from git. Every method is the bus's own verb; the transport that
// holds the bytes is an implementation detail. The design is
// LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a; the state machine is tla/BusCursor.tla.
type Store interface {
	// Roster is the bus's participants and their lanes.
	Roster(ctx context.Context) (*Config, error)
	// Refresh brings the checkout level with the remote, and reports whether it moved.
	Refresh(ctx context.Context) (head Position, moved bool, err error)
	// Head is the position the checkout is at now.
	Head(ctx context.Context) (Position, error)
	// Since lists the lane paths added or modified from one position to another, capped by
	// limit. See ChangedSince and CommitsSinceBounded.
	Since(ctx context.Context, from, to Position, limit int) (paths []string, capped bool, err error)
	// Read is the tree at one position: the checkout's files at the head, or the files git
	// held at an earlier commit. See tla/BusCursor.tla.
	Read(ctx context.Context, at Position) (fs.FS, error)
	// Publish lands one Change as a single commit, or reports it Already there.
	Publish(ctx context.Context, change Change) (Published, error)
	// Find answers whether an id is on the bus, with the note's bytes and its INDEX entry.
	Find(ctx context.Context, id string) (note []byte, index IndexEntry, found bool, err error)
}
