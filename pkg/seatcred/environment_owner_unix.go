//go:build unix

package seatcred

import (
	"os"
	"syscall"
)

func seatEnvironmentOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
