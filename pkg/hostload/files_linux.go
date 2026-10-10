//go:build linux

package hostload

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcHolders is every process under root (/proc) with its open descriptors: the
// entries of <pid>/fd, its command from <pid>/comm and its user from the owner of <pid>.
// A process this user cannot read, or that ended during the walk, is skipped; a walk
// still going at the deadline stops and says so.
func ProcHolders(root string, deadline time.Time) ([]Holder, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	users := map[uint32]string{}
	var out []Holder
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		if time.Now().After(deadline) {
			return nil, HoldersTimedOut("the walk of " + root)
		}
		dir := filepath.Join(root, e.Name())
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil || len(fds) == 0 {
			continue
		}
		h := Holder{PID: pid, Open: len(fds)}
		if b, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
			h.Command = strings.TrimSpace(string(b))
		}
		if fi, err := os.Stat(dir); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				name, seen := users[st.Uid]
				if !seen {
					name = strconv.FormatUint(uint64(st.Uid), 10)
					if u, err := user.LookupId(name); err == nil {
						name = u.Username
					}
					users[st.Uid] = name
				}
				h.User = name
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// ParseFileNr is Linux's /proc/sys/fs/file-nr: the handles allocated, the unused (0
// since 2.6) and the maximum.
func ParseFileNr(s string) (open, limit int, ok bool) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(f[0])
	m, err2 := strconv.Atoi(f[2])
	if err1 != nil || err2 != nil || a < 0 || m < 0 {
		return 0, 0, false
	}
	return a, m, true
}
