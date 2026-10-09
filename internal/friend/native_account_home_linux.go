//go:build linux

package friend

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// nativeAccountHome reads the trusted UID row directly. os/user.Current and
// LookupId can reuse an environment fallback in static builds; that is never
// authority for NativeControlAuthority's persistent private namespace.
func nativeAccountHome() (string, error) {
	prior, err := os.Lstat("/etc/passwd")
	if err != nil { return "", err }
	if st, ok := prior.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 || !prior.Mode().IsRegular() || prior.Mode().Perm()&0o022 != 0 {
		return "", errors.New("native control passwd source is not a trusted OS account file")
	}
	f, err := os.Open("/etc/passwd")
	if err != nil { return "", err }
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(prior, opened) { return "", errors.New("native control account source changed during lookup") }
	return nativeAccountHomeFrom(f, os.Geteuid())
}

func nativeAccountHomeFrom(r io.Reader, uid int) (string, error) {
	scan, home := bufio.NewScanner(r), ""
	for scan.Scan() {
		fields := strings.Split(scan.Text(), ":")
		if len(fields) != 7 { continue }
		rowUID, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil || rowUID != uint64(uid) { continue }
		if home != "" || fields[5] == "" { return "", errors.New("native control account UID row is ambiguous") }
		home = fields[5]
	}
	if err := scan.Err(); err != nil { return "", err }
	if home == "" { return "", errors.New("native control account UID has no trusted passwd home; no environment fallback") }
	return home, nil
}
