package notice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
)

// The Claude Code session's own hooks (tla/ClaudeHookNotice.tla). The Monitor
// on the watcher is the push, into a running turn and waking an idle session;
// three hooks the route installs keep a challenge from being lost when it is
// not:
//
//   - PostToolUse puts an unproved pending challenge into the running turn
//     once (Tool in the model).
//   - Stop blocks the end of a turn once for an unproved pending challenge,
//     naming it, then once for a missing watcher; otherwise the turn ends and
//     the generation advances (SessionStop).
//   - SessionStart, after a restart, resume or compaction, puts the arm line
//     and the pending notice in front of the model (Start).
//
// They read and write only files in the claude state directory, never the
// store, and never prove: only the model runs nova-hook prove.
const (
	claudePending  = "pending"  // the saved notice file of the pushed notice, while it is pending
	claudeProved   = "proved"   // the newest nonce the model proved (nova-hook prove writes it)
	claudeBlocked  = "blocked"  // the newest nonce a Stop was blocked for
	claudeSurfaced = "surfaced" // the newest nonce PostToolUse put into a turn
	claudeNagged   = "nagged"   // a Stop was blocked once for a missing watcher

	// ClaudeWatchLock is the watcher's lock: held while a Monitor runs it.
	ClaudeWatchLock = "watch.lock"
)

// ClaudeEvent is the part of a Claude Code hook's input the route reads.
type ClaudeEvent struct {
	Name   string `json:"hook_event_name"`
	Source string `json:"source"` // SessionStart: startup, resume, clear or compact
}

// ReadClaudeEvent reads a hook's input whole: a PostToolUse input carries the
// tool's response, of any size, and only the event's name is decoded. Empty
// input is a Stop, the hook as this route first installed it and as a hand run
// calls it.
func ReadClaudeEvent(r io.Reader) (ClaudeEvent, error) {
	var e ClaudeEvent
	if r == nil {
		return ClaudeEvent{Name: "Stop"}, nil
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return e, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return ClaudeEvent{Name: "Stop"}, nil
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, fmt.Errorf("claude hook input is not JSON: %w", err)
	}
	if e.Name == "" {
		e.Name = "Stop"
	}
	return e, nil
}

// ClaudePending is the challenge the pending notice holds, as the hooks tell it.
type ClaudePending struct {
	Path, Nonce, As, Redis string
}

// prove is the one command the model runs for p's challenge. The store's
// address stays in the notice file (local_receiver.redis) and the session's
// NOVA_BUS_REDIS; no hook text carries it.
func (p ClaudePending) prove() string {
	return "nova-hook prove --as " + p.As + " --nonce " + p.Nonce + " (with --redis from the notice when NOVA_BUS_REDIS is unset)"
}

// ReadClaudePending reads the pending notice: its file and the challenge it
// holds (Nonce "" when it holds none). No notice pending is the zero value.
func ReadClaudePending(state string) (ClaudePending, error) {
	path, err := readWord(filepath.Join(state, claudePending))
	if err != nil || path == "" {
		return ClaudePending{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ClaudePending{}, nil // consumed and pruned under us
	}
	if err != nil {
		return ClaudePending{}, err
	}
	if len(raw) > MaxSaved {
		return ClaudePending{}, fmt.Errorf("saved notice %s is over %d bytes", path, MaxSaved)
	}
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return ClaudePending{}, fmt.Errorf("saved notice %s: %w", path, err)
	}
	p := ClaudePending{Path: path, As: e.LocalReceiver.Identity, Redis: e.LocalReceiver.Redis}
	if e.Challenge != nil {
		if nonce, ok := Canonical(*e.Challenge, p.As); ok {
			p.Nonce = nonce
		}
	}
	return p, nil
}

// ClaudeProved records the nonce the model just proved: nova-hook prove's
// local note, read by the hooks so a proved challenge is never named again.
func ClaudeProved(state, nonce string) error {
	return writeWord(filepath.Join(state, claudeProved), nonce)
}

// ClaudeWatched reports whether a watcher holds the watch lock.
func ClaudeWatched(state string) (bool, error) {
	l, err := filelock.TryLock(filepath.Join(state, ClaudeWatchLock), "claude-stop-check")
	if err == nil {
		return false, l.Unlock()
	}
	if _, held := filelock.AsHeldError(err); held {
		return true, nil
	}
	return false, err
}

// ClaudeWatch takes the watch lock for a watcher and clears a missing-watcher
// block, so the next one is told again. Unlock it when the watcher ends.
func ClaudeWatch(state string) (*filelock.FileLock, error) {
	l, err := filelock.TryLock(filepath.Join(state, ClaudeWatchLock), "claude-watch")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(state, claudeNagged)); err != nil && !errors.Is(err, os.ErrNotExist) {
		// ignored: the Remove error is the one returned; the lock is the kernel's flock and goes with its descriptor
		_ = l.Unlock()
		return nil, err
	}
	return l, nil
}

// ClaudeStopDecision is what the Stop hook does: Block (with the reason the
// model reads) or end the turn, advancing the generation.
type ClaudeStopDecision struct {
	Block      bool
	Reason     string
	Generation int
}

// ClaudeStop is the session's Stop hook (SessionStop): block once for an
// unproved pending challenge, then once for a missing watcher, else end the
// turn. watch is the line that arms the watcher.
func ClaudeStop(state, watch string) (ClaudeStopDecision, error) {
	p, err := ReadClaudePending(state)
	if err != nil {
		return ClaudeStopDecision{}, err
	}
	if p.Nonce != "" {
		proved, err := readWord(filepath.Join(state, claudeProved))
		if err != nil {
			return ClaudeStopDecision{}, err
		}
		blocked, err := readWord(filepath.Join(state, claudeBlocked))
		if err != nil {
			return ClaudeStopDecision{}, err
		}
		if p.Nonce != proved && p.Nonce != blocked {
			if err := writeWord(filepath.Join(state, claudeBlocked), p.Nonce); err != nil {
				return ClaudeStopDecision{}, err
			}
			return ClaudeStopDecision{Block: true, Reason: "nova-hook: hook challenge " + p.Nonce + " is pending and not proved. Before this turn ends, run: " + p.prove() + " (the notice: " + p.Path + ")"}, nil
		}
	}
	watched, err := ClaudeWatched(state)
	if err != nil {
		return ClaudeStopDecision{}, err
	}
	if !watched {
		nagged, err := exists(filepath.Join(state, claudeNagged))
		if err != nil {
			return ClaudeStopDecision{}, err
		}
		if !nagged {
			if err := writeWord(filepath.Join(state, claudeNagged), "1"); err != nil {
				return ClaudeStopDecision{}, err
			}
			reason := "nova-hook: no watcher is armed, so a push cannot wake this session once it is idle. Before this turn ends, arm it with the Monitor tool (the longest timeout; re-arm it each time it ends): " + watch
			if p.Path != "" {
				// ending the turn consumes the pending notice, which no watcher showed
				reason += ". A notice is pending: " + Notice{Path: p.Path}.Text() + "."
			}
			return ClaudeStopDecision{Block: true, Reason: reason}, nil
		}
	}
	n, err := ClaudeBoundary(state)
	return ClaudeStopDecision{Generation: n}, err
}

// ClaudeToolUse is the PostToolUse hook (Tool): an unproved pending challenge
// not yet surfaced is put into the running turn, once. "" says nothing.
func ClaudeToolUse(state string) (string, error) {
	p, err := ReadClaudePending(state)
	if err != nil || p.Nonce == "" {
		return "", err
	}
	proved, err := readWord(filepath.Join(state, claudeProved))
	if err != nil {
		return "", err
	}
	surfaced, err := readWord(filepath.Join(state, claudeSurfaced))
	if err != nil {
		return "", err
	}
	if p.Nonce == proved || p.Nonce == surfaced {
		return "", nil
	}
	if err := writeWord(filepath.Join(state, claudeSurfaced), p.Nonce); err != nil {
		return "", err
	}
	return "nova-hook: hook challenge " + p.Nonce + " is pending. Answer it before anything else: " + p.prove() + " (the notice: " + p.Path + ")", nil
}

// ClaudeStart is the SessionStart hook (Start): after a restart the watchers
// died with the old process, so the model is told to arm one (after a
// compaction one may still run, and it is told so), and told the pending
// notice.
func ClaudeStart(state, watch string) (string, error) {
	if err := os.Remove(filepath.Join(state, claudeNagged)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	watched, err := ClaudeWatched(state)
	if err != nil {
		return "", err
	}
	text := "nova-hook (the claude adapter): arm the watcher now with the Monitor tool (the longest timeout; re-arm it each time it ends), so pushes reach this session busy or idle: " + watch
	if watched {
		// a compaction or clear keeps the session's Monitors running
		text = "nova-hook (the claude adapter): a watcher is armed (" + watch + "); re-arm it with the Monitor tool each time it ends."
	}
	p, err := ReadClaudePending(state)
	if err != nil {
		return "", err
	}
	if p.Path != "" {
		text += "\nA notice is pending: " + Notice{Path: p.Path}.Text() + "."
		if p.Nonce != "" {
			text += " It holds hook challenge " + p.Nonce + ": " + p.prove()
		}
	}
	return text, nil
}

func readWord(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func writeWord(path, word string) error {
	return atomicfile.Write(path, []byte(word+"\n"), 0o600, atomicfile.ExactMode())
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
