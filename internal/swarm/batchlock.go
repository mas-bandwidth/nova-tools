package swarm

// The slot lock: one batch holds one slot, from allocation to slot end (issue #457).
//
// Two batches that auto-allocated at the same moment took slot 1 twice, each wrote into the
// same job directory, and both cards were lost. A slot is now held by a file the holder's
// own pid dies with: <root>/<slot>/BATCH carrying `id=<batch> pid=<n> at=<stamp>`. A live
// lock refuses that slot for the card that named it -- that card alone, per issue #529 --
// and a stale lock, whose pid no process holds, is taken over once and said out loud.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// slotLockName is the lock file's name inside a slot directory.
const slotLockName = "BATCH"

// slotLock is one slot's lock as it is written and read back: the batch that holds the
// slot, the pid the lock dies with, and the stamp it was taken at.
type slotLock struct {
	id  string
	pid int
	at  string
}

// slotLockPath is the lock of the slot this card resolved to: <root>/<slot>/BATCH, and
// <root>/<bench>-<slot>/BATCH for a card on a bench.
func slotLockPath(root string, c batchCard) string {
	return filepath.Join(root, scratchName(c), slotLockName)
}

// slotLockBody is the one line a lock carries.
func slotLockBody(id string, pid int) string {
	return fmt.Sprintf("id=%s pid=%d at=%s\n", id, pid, time.Now().UTC().Format(time.RFC3339))
}

// parseSlotLock reads a lock's one line. A lock whose text does not parse is not a live
// holder: it has no pid to be alive, so it is stale and its id is a dash.
func parseSlotLock(raw string) slotLock {
	lk := slotLock{id: "-"}
	for _, field := range strings.Fields(strings.TrimSpace(raw)) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "id":
			if value != "" {
				lk.id = value
			}
		case "pid":
			if n, err := strconv.Atoi(value); err == nil {
				lk.pid = n
			}
		case "at":
			lk.at = value
		}
	}
	return lk
}

// slotHeldBy reports the lock holding slot <n> under the root, and whether it is live: a
// lock file whose pid is alive. A missing, unreadable or stale lock is not a holder.
func slotHeldBy(root string, n int) (slotLock, bool) {
	raw, err := os.ReadFile(filepath.Join(slotDir(root, n), slotLockName))
	if err != nil {
		return slotLock{}, false
	}
	lk := parseSlotLock(string(raw))
	return lk, Alive(lk.pid, "")
}

// takeSlotLock takes this card's slot for the batch. It returns why the slot is refused
// when another batch holds it live -- `slot=<n> held-by=<id> pid=<n>`, the card's own
// admission reason -- and the one BATCH NOTE line when a stale lock was taken over. An
// error is the filesystem refusing, which is the batch's own exit 2.
func takeSlotLock(root string, c batchCard, batchID string) (why, note string, err error) {
	path := slotLockPath(root, c)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	body := slotLockBody(batchID, os.Getpid())
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		_, werr := f.WriteString(body)
		cerr := f.Close()
		if werr != nil {
			return "", "", werr
		}
		return "", "", cerr
	}
	if !os.IsExist(err) {
		return "", "", err
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		return "", "", rerr
	}
	lk := parseSlotLock(string(raw))
	if Alive(lk.pid, "") {
		return fmt.Sprintf("slot=%d held-by=%s pid=%d", c.slot, lk.id, lk.pid), "", nil
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", "", err
	}
	return "", fmt.Sprintf("BATCH NOTE slot=%d stale-lock id=%s taken", c.slot, lk.id), nil
}
