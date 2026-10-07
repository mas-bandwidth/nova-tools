//go:build darwin || linux

package main

import (
	"fmt"
	"syscall"
)

// probeNonceOnFD reads the parent's one-time value off the inherited descriptor, and it
// does it with raw syscalls rather than os.NewFile(probeNonceFD, ...) ON PURPOSE:
// os.NewFile TAKES OWNERSHIP of the descriptor, so the *os.File's finalizer closes fd 3
// when it is collected. The verb also runs IN PROCESS under `go test`, where fd 3 is the
// test binary's own testlog — measured while writing this: the first form closed it and
// every package that ran the verb died with `can't write .../testlog.txt: bad file
// descriptor`. A guard that reaches for a descriptor must not take it.
//
// What it insists on, in order: fd 3 is open; it is a PIPE (a regular file or a terminal
// is something a caller can hand a process from a shell, a pipe with the parent's bytes
// already in it is not); it carries exactly probeNonceLen bytes; and it carries no more.
func probeNonceOnFD() ([probeNonceLen]byte, error) {
	var got [probeNonceLen]byte
	var st syscall.Stat_t
	if err := syscall.Fstat(probeNonceFD, &st); err != nil {
		return got, fmt.Errorf("fd %d is not open", probeNonceFD)
	}
	if uint32(st.Mode)&uint32(syscall.S_IFMT) != uint32(syscall.S_IFIFO) {
		return got, fmt.Errorf("fd %d is not a pipe", probeNonceFD)
	}
	read := 0
	for read < len(got) {
		n, err := syscall.Read(probeNonceFD, got[read:])
		if err == syscall.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			return got, fmt.Errorf("fd %d carried no one-time value", probeNonceFD)
		}
		read += n
	}
	var extra [1]byte
	if n, err := syscall.Read(probeNonceFD, extra[:]); err == nil && n != 0 {
		return got, fmt.Errorf("fd %d carried more than the one-time value", probeNonceFD)
	}
	return got, nil
}
