package codexdesktop

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// maxHeader bounds the first line of a rollout file: the session_meta header,
// which carries the chat's base instructions.
const maxHeader = 4 << 20

// desktopOriginator is the originator the Codex desktop app stamps on the
// chats it starts (ChatGPT.app 26.1007.30912; the rollout's session_meta and
// the threads table of state_5.sqlite say "Codex Desktop").
const desktopOriginator = "Codex Desktop"

// Chat is one Codex desktop chat, as its rollout's session_meta header names
// it.
type Chat struct {
	ID      string
	Cwd     string
	Created time.Time
}

// DesktopChats reads the header of every rollout under sessions/ of a Codex
// home (CODEX_HOME, ~/.codex by default) and keeps the chats a person or a
// friend started in the desktop app: originator "Codex Desktop", thread_source
// "user". Subagent threads, exec runs and archived chats (moved out of
// sessions/ by the app) are not chats a friend lives in. Only the first line
// of each file is read.
func DesktopChats(home fs.FS) ([]Chat, error) {
	var chats []Chat
	err := fs.WalkDir(home, "sessions", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path.Ext(name) != ".jsonl" {
			return nil
		}
		chat, ok, err := readHeader(home, name)
		if err != nil {
			return err
		}
		if ok {
			chats = append(chats, chat)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("codex saved chats: %w", err)
	}
	return chats, nil
}

func readHeader(home fs.FS, name string) (Chat, bool, error) {
	f, err := home.Open(name)
	if err != nil {
		return Chat{}, false, err
	}
	defer f.Close() // ignored: a read-only file, nothing was written through it
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64<<10), maxHeader)
	if !scan.Scan() {
		if err := scan.Err(); err != nil {
			return Chat{}, false, fmt.Errorf("codex chat header %s: %w", name, err)
		}
		return Chat{}, false, nil // an empty rollout: a chat still being written
	}
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			ID           string          `json:"id"`
			Cwd          string          `json:"cwd"`
			Timestamp    time.Time       `json:"timestamp"`
			Originator   string          `json:"originator"`
			ThreadSource string          `json:"thread_source"`
			Source       json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
		return Chat{}, false, fmt.Errorf("codex chat header %s: %w", name, err)
	}
	p := row.Payload
	if row.Type != "session_meta" || p.Originator != desktopOriginator || p.ThreadSource != "user" || !safeID(p.ID) || p.Cwd == "" {
		return Chat{}, false, nil
	}
	return Chat{ID: p.ID, Cwd: p.Cwd, Created: p.Timestamp}, true, nil
}

// FriendWorkspace reports whether cwd is a workspace of friend: its base name,
// case folded, is the friend's name, or the name with the copy suffix macOS
// gives a second folder of that name (" 2", " 3", ...): a friend named ada
// may live in "~/Documents/ChatGPT/ada 2".
func FriendWorkspace(cwd, friend string) bool {
	if friend == "" {
		return false
	}
	base := filepath.Base(filepath.Clean(cwd))
	if head, tail, ok := strings.Cut(base, " "); ok && tail != "" && strings.IndexFunc(tail, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
		base = head
	}
	return strings.EqualFold(base, friend)
}

// CurrentChat is the friend's current chat: the newest desktop chat started in
// one of the friend's workspaces. A friend who moves to a new chat starts it
// in the same workspace, so the newest is the one the friend is in. Ties go
// to the greater ID (Codex IDs are time ordered).
func CurrentChat(chats []Chat, friend string) (Chat, error) {
	var best Chat
	for _, chat := range chats {
		if !FriendWorkspace(chat.Cwd, friend) {
			continue
		}
		if best.ID == "" || chat.Created.After(best.Created) || (chat.Created.Equal(best.Created) && chat.ID > best.ID) {
			best = chat
		}
	}
	if best.ID == "" {
		return Chat{}, fmt.Errorf("no Codex desktop chat in a workspace named %s; start one there, or name the chat with --codex-thread", friend)
	}
	return best, nil
}

// ValidThread reports whether id has the grammar of a thread a route may name.
func ValidThread(id string) bool { return safeID(id) }
