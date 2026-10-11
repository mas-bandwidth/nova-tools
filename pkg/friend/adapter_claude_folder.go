package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// SessionCheckFilePrefix names the file a claude friend's session check is
// written as by the folder adapter: <dir>/inbox/SESSION-CHECK-<nonce>, the
// shape the seat's own folder adapter uses (internal/sprint, PROOF-<nonce>).
const SessionCheckFilePrefix = "SESSION-CHECK-"

// SessionCheckFile is the path a check carrying nonce is written to for the
// friend whose directory is dir.
func SessionCheckFile(dir, nonce string) string {
	return filepath.Join(dir, "inbox", SessionCheckFilePrefix+nonce)
}

// FolderCheck is the claude harness's session-check adapter (docs/SPEC-FRIEND.md,
// The push proof; the owner, 2026-10-08: "why not, can we fix the harness to
// do this?"). Claude Code has no command that puts a turn into a running
// session from outside, and a claude friend's cards run as processes of their
// own; what a live session can do is watch a folder. So the daemon's SESSION
// CHECK goes in as one file, <Dir>/inbox/SESSION-CHECK-<nonce>, holding the
// check's text (the pong command the session runs: nova-friend pong --as
// <friend> --nonce <nonce>), and the session that answers it proves the push
// as any session does: its pong on the bus brings the presence up, the proof
// on bus2:push follows, and the daemon's next beat carries the pong, so the
// sprint records the session proof natively. One check stands at a time: the
// file of an earlier nonce is removed when the next is written, so a session
// in a long turn finds one check, never a pile. The adapter is not passive
// (the check does go somewhere a session reads) and never ReadOnReturn (a
// file written is not a file read), so an unanswered check is asked again
// with the same nonce on the daemon's cadence. A friend with no live session
// (per-card lanes only, mode batch) answers nothing: her presence reads down
// with the check's nonce, her cards' finishes stand for her at the server,
// and nothing refuses her messages on it. The write holds no turn of the
// session, so the check goes in in place (InPlace; SessionCheck.ask): the
// file is on disk before the beat ever says the check.
type FolderCheck struct {
	Friend, Dir string
}

// InPlace marks a Deliverer whose delivery holds no turn of the session (a
// file write): SessionCheck.ask runs it in place, never on its Go scheduler,
// so the check is delivered before ask returns and before any beat names it.
type InPlace interface {
	InPlace()
}

// InPlace: a file write holds no turn; see InPlace.
func (*FolderCheck) InPlace() {}

// Deliver writes the check text as <Dir>/inbox/SESSION-CHECK-<nonce>, the
// nonce read off the text's first line (SessionCheckPrefix), removing the
// file of any earlier check. A text that is no session check is refused.
func (f *FolderCheck) Deliver(_ context.Context, text string) (int, error) {
	first, _, _ := strings.Cut(text, "\n")
	nonce, ok := strings.CutPrefix(strings.TrimSpace(first), SessionCheckPrefix)
	nonce = strings.TrimSpace(nonce)
	if !ok || nonce == "" || strings.ContainsAny(nonce, "/\\ ") {
		return 1, fmt.Errorf("the folder adapter takes only a session check (%s<nonce> first); got %q", SessionCheckPrefix, first)
	}
	path := SessionCheckFile(f.Dir, nonce)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 1, err
	}
	if err := atomicfile.WriteFile(path, []byte(strings.TrimSuffix(text, "\n")+"\n"), 0o644); err != nil {
		return 1, err
	}
	old, err := filepath.Glob(filepath.Join(filepath.Dir(path), SessionCheckFilePrefix+"*"))
	if err != nil {
		return 0, nil // ignored: a pattern that cannot be globbed leaves the earlier files; the check is in
	}
	for _, p := range old {
		if p != path {
			if rerr := os.Remove(p); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				return 0, nil // ignored: an earlier check that cannot be removed is a stale nonce nobody counts
			}
		}
	}
	return 0, nil
}

// FolderCheckLine is the NOTE install and run print for a claude friend: where
// her session check lands, and what a live session runs to answer it.
func FolderCheckLine(friend, dir, state string) string {
	return "the session check: the daemon writes " + SessionCheckFile(dir, "<nonce>") + " (one at a time); a live session watches that folder and answers the file it shows: nova-friend pong --as " + friend + " --nonce <nonce> --state-dir " + state + "; with no live session her cards' finishes are her presence, and nova-bus still delivers to her"
}
