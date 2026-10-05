package main

// One lander per clone (docs/SPEC-SPRINT.md section 7, land-one-lander-now.w1). Two landers
// in one checkout leave it dirty for both: a hand land beside the server's run --land did,
// and every later pass refused every stream. So a pass of land holds a lock file beside
// each clone it lands in, from before its first git there to the end of the pass, naming
// its pid and its verb; a second pass, by hand or by the server, is refused the batch while
// the lock is held, naming the holder. A lock whose holder's process is gone is taken over
// and said on a LAND TAKEOVER line. The server's land pass also records itself under the
// land root (server.land), and a hand land is refused outright while that server lives.
// A dry run reads the store only and takes no lock.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// landHolder is what a lock file holds: the lander's process, its verb, the pass's own
// token (two passes in one process differ by it) and when it took the lock.
type landHolder struct {
	Pid     int    `json:"pid"`
	Started string `json:"started"`
	Verb    string `json:"verb"`
	Token   string `json:"token"`
	At      string `json:"at"`
}

// alive says the holder's process still runs: this process always does; another pid is
// gone when it no longer runs, or runs with a start stamp other than the one recorded (a
// pid issued again since).
func (h landHolder) alive() bool {
	if h.Pid == os.Getpid() {
		return true
	}
	if !swarm.Alive(h.Pid, h.Started) {
		return false
	}
	if h.Started != "" && h.Started != swarm.Dash {
		if now := swarm.StartStamp(h.Pid); now != swarm.Dash && now != h.Started {
			return false
		}
	}
	return true
}

// name is the holder as a refusal names it.
func (h landHolder) name() string {
	return fmt.Sprintf("pid %d (nova-sprint %s, since %s)", h.Pid, h.Verb, h.At)
}

// landLockPath is the lock file beside a clone: outside it, so the lock never makes the
// clone not clean.
func landLockPath(dir string) string { return filepath.Clean(dir) + ".land-lock" }

// landServerPath is where the server's land pass records itself, under the land root.
func landServerPath(root string) string { return filepath.Join(root, "server.land") }

// holder is this pass as a lock file records it.
func (l *lander) holder() landHolder {
	if l.token == "" {
		b := make([]byte, 8)
		// ignored: crypto/rand.Read never returns an error on a supported platform
		_, _ = rand.Read(b)
		l.token = hex.EncodeToString(b)
	}
	verb := "land"
	if l.a.landLazy {
		verb = "run --land"
	}
	pid := os.Getpid()
	return landHolder{Pid: pid, Started: swarm.StartStamp(pid), Verb: verb, Token: l.token, At: l.a.now().UTC().Format(time.RFC3339)}
}

// writeWhole makes path holding body whole or not at all: a temporary file linked into
// place, so a reader never sees a lock half written. replace overwrites an existing path
// (a rename); otherwise an existing path fails with fs.ErrExist (a link).
func writeWhole(path string, body []byte, replace bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// ignored: the temporary file is a name only, removed once linked or renamed
	defer func() { _ = os.Remove(tmp) }()
	_, werr := f.Write(body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	if replace {
		return os.Rename(tmp, path)
	}
	return os.Link(tmp, path)
}

// lockClone takes the lock beside dir for the rest of the pass, or says why not: another
// live lander holds it (named), or the lock could not be read or written. A lock whose
// holder is gone is taken over, recorded in l.tookOver for the batch's LAND TAKEOVER line.
func (l *lander) lockClone(dir string) (why string) {
	path := landLockPath(dir)
	if _, ok := l.locks[path]; ok {
		return ""
	}
	if l.locks == nil {
		l.locks = map[string]string{}
	}
	me := l.holder()
	// ignored: a struct of strings and an int always encodes
	body, _ := json.Marshal(me)
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "the lock " + path + " could not be made: " + oneline.Err(err) + "; nothing was fetched or pushed; run: nova-sprint land again"
	}
	for range 2 {
		err := writeWhole(path, body, false)
		if err == nil {
			l.locks[path] = me.Token
			return ""
		}
		if !errors.Is(err, fs.ErrExist) {
			return "the lock " + path + " could not be made: " + oneline.Err(err) + "; nothing was fetched or pushed; run: nova-sprint land again"
		}
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue // released between the two
		}
		var h landHolder
		if err == nil {
			err = json.Unmarshal(raw, &h)
		}
		if err != nil || h.Pid <= 0 {
			return "the lock " + path + " names no lander (" + firstLine(string(raw), err) + "); nothing was fetched or pushed; run: rm " + path + " once no land runs there"
		}
		if h.alive() {
			return "the clone " + dir + " is held by another lander, " + h.name() + "; nothing was fetched or pushed; run: nova-sprint land again once pid " + fmt.Sprint(h.Pid) + " ends"
		}
		// the holder is gone: its lock is removed only while it still holds the bytes read
		// (a taker beside this one that already replaced it keeps its own)
		again, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(again, raw) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "the lock " + path + " of a lander that is gone, " + h.name() + ", could not be removed: " + oneline.Err(err) + "; run: rm " + path
		}
		l.tookOver = append(l.tookOver, h)
	}
	return "the lock " + path + " changed hands while land took it; nothing was fetched or pushed; run: nova-sprint land again"
}

// unlockClones releases every lock the pass holds, each only while it is still the pass's own.
func (l *lander) unlockClones() {
	for path, token := range l.locks {
		var h landHolder
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &h) == nil && h.Token == token {
			_ = os.Remove(path) // ignored: a lock left behind is taken over once this process ends
		}
	}
	l.locks = nil
}

// landServerWhy is the refusal of a land beside a server that lands (run --land), naming
// the server; "" when no live server has recorded its pass (markLandServer). cmdLand asks
// it for neither the server's own pass nor a dry run.
func (a *app) landServerWhy() string {
	root, err := a.landRoot()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(landServerPath(root))
	if err != nil {
		return ""
	}
	var h landHolder
	if json.Unmarshal(raw, &h) != nil || h.Pid <= 0 || !h.alive() {
		return ""
	}
	return "the server, " + h.name() + ", lands every " + LandEvery.String() + " and is the one lander; a land beside it is refused; nothing was fetched, pushed or reported"
}

// markLandServer records the server's land pass under the land root, for landServerWhy.
func (l *lander) markLandServer() error {
	root, err := l.a.landRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// ignored: a struct of strings and an int always encodes
	body, _ := json.Marshal(l.holder())
	return writeWhole(landServerPath(root), append(body, '\n'), true)
}
