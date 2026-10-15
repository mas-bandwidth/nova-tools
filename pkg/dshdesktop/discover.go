package dshdesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The DeepSeek Harness local state this package reads (Harness 0.2.0-rc.2,
// source 5e9e301dd9dc8923b2762f76dacfc5751f6ca851, read from the installed
// app.asar):
//
//   - home: $DSH_HOME, else ~/.dsh (dsh-home-paths, resolveDshHome; app
//     lib/main.js resolveDshHome).
//   - desktop profile: <home>/profiles/desktop (lib/main.js resolveDesktopPaths,
//     line 70); its user layer is cordis.patch.yml (lib/main.js line 3263).
//   - sessions: <home>/sessions/<project key>/<session id>/, each with a
//     session.lock that the one live writer holds under flock(2)
//     (dsh-session-persistence-jsonl lib/index.js sessionDir line 914,
//     LEASE_FILENAME line 643).
//   - workspaces and archived sessions: <home>/storages/workspace.json
//     (dsh-api-workspace-controller, workspaceRegistry.archivedSessionIds).
//
// Discovery only reads. It never takes a session lock: a live session is one
// whose session.lock some process holds open, which the caller's probe reports.

// Home resolves the harness home as the harness does: $DSH_HOME when it is not
// blank, else <user home>/.dsh.
func Home(getenv func(string) string) (string, error) {
	if v := strings.TrimSpace(getenv("DSH_HOME")); v != "" {
		if strings.HasPrefix(v, "~/") {
			v = filepath.Join(getenv("HOME"), v[2:])
		}
		return filepath.Clean(v), nil
	}
	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		return "", errors.New("no HOME to find the DeepSeek Harness home (~/.dsh) under")
	}
	return filepath.Join(home, ".dsh"), nil
}

// ProfileDir is the desktop app's profile directory under a harness home.
func ProfileDir(home string) string { return filepath.Join(home, "profiles", "desktop") }

// Session is one candidate harness session for a friend.
type Session struct {
	ID       string
	Dir      string
	Live     bool
	Modified time.Time
}

// HeldFunc reports which of the given session.lock paths a process holds open.
type HeldFunc func(locks []string) map[string]bool

type workspaceFile struct {
	Global struct {
		ArchivedSessionIDs []string `json:"archivedSessionIds"`
	} `json:"global"`
	Tables struct {
		Workspaces map[string]struct {
			Path       string   `json:"path"`
			Title      string   `json:"title"`
			SessionIDs []string `json:"sessionIds"`
		} `json:"workspaces"`
	} `json:"tables"`
}

// Discover finds the friend's current harness session: the friend's workspace
// is the one titled with the friend's name (or whose directory is named so);
// its sessions less the archived ones are the candidates; a live candidate
// (session.lock held) beats one that is not, and the newest session log wins
// among equals. A friend who starts a new session is followed on the next call.
func Discover(home, name string, held HeldFunc) (Session, error) {
	data, err := os.ReadFile(filepath.Join(home, "storages", "workspace.json"))
	if err != nil {
		return Session{}, fmt.Errorf("read DeepSeek Harness workspaces: %w", err)
	}
	var wf workspaceFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return Session{}, fmt.Errorf("parse DeepSeek Harness workspaces: %w", err)
	}
	archived := map[string]bool{}
	for _, id := range wf.Global.ArchivedSessionIDs {
		archived[id] = true
	}
	var ids []string
	matched := false
	for _, ws := range wf.Tables.Workspaces {
		if !strings.EqualFold(ws.Title, name) && !strings.EqualFold(filepath.Base(ws.Path), name) {
			continue
		}
		matched = true
		for _, id := range ws.SessionIDs {
			if !archived[id] && safeID(id) {
				ids = append(ids, id)
			}
		}
	}
	if !matched {
		return Session{}, fmt.Errorf("no DeepSeek Harness workspace named %s; open %s's workspace in the harness, or pass --dsh-session", name, name)
	}
	var found []Session
	var locks []string
	for _, id := range ids {
		dirs, _ := filepath.Glob(filepath.Join(home, "sessions", "*", id)) // ignored: a safe id holds no pattern characters
		for _, dir := range dirs {
			s := Session{ID: id, Dir: dir}
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !strings.HasPrefix(e.Name(), "session.v") {
					continue
				}
				if info, err := e.Info(); err == nil && info.ModTime().After(s.Modified) {
					s.Modified = info.ModTime()
				}
			}
			found = append(found, s)
			locks = append(locks, filepath.Join(dir, "session.lock"))
		}
	}
	if len(found) == 0 {
		return Session{}, fmt.Errorf("no unarchived DeepSeek Harness session in %s's workspace; start one in the harness", name)
	}
	if held != nil {
		live := held(locks)
		for i := range found {
			found[i].Live = live[locks[i]]
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Live != found[j].Live {
			return found[i].Live
		}
		if !found[i].Modified.Equal(found[j].Modified) {
			return found[i].Modified.After(found[j].Modified)
		}
		return found[i].ID < found[j].ID
	})
	return found[0], nil
}

// LsofHeld asks lsof which of the lock files a process holds open. It reads
// process tables only; it never opens or locks a session file itself. A missing
// lsof reports nothing held, and discovery falls back to the newest session.
func LsofHeld(locks []string) map[string]bool {
	held := map[string]bool{}
	if len(locks) == 0 {
		return held
	}
	out, _ := exec.Command("lsof", append([]string{"-Fn", "--"}, locks...)...).Output() // ignored: lsof exits 1 when no file is open
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			held[line[1:]] = true
		}
	}
	return held
}

// targetFile is the receiver's follow file: the session it delivers to.
const targetFile = "session.json"

// TargetPath is the follow file in an events directory.
func TargetPath(eventsDir string) string { return filepath.Join(eventsDir, targetFile) }

type target struct {
	Version   int    `json:"version"`
	SessionID string `json:"sessionId"`
}

// ReadTarget returns the session the receiver currently follows, or "".
func ReadTarget(eventsDir string) string {
	data, err := os.ReadFile(TargetPath(eventsDir))
	if err != nil {
		return ""
	}
	var t target
	if json.Unmarshal(data, &t) != nil || t.Version != 1 || !safeID(t.SessionID) {
		return ""
	}
	return t.SessionID
}

// WriteTarget points the mounted receiver at a session. The receiver reads it
// on each drain, so following a new session needs no harness restart.
func WriteTarget(eventsDir, sessionID string) (changed bool, err error) {
	if !safeID(sessionID) {
		return false, errors.New("dsh session id is invalid")
	}
	if ReadTarget(eventsDir) == sessionID {
		return false, nil
	}
	data, err := json.Marshal(target{Version: 1, SessionID: sessionID})
	if err != nil {
		return false, err
	}
	if err := writePrivate(TargetPath(eventsDir), data); err != nil {
		return false, err
	}
	return true, nil
}
