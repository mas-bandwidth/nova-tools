//go:build unix

package fleet

import (
	"fmt"
	"os"
	"os/user"
	"syscall"
	"testing"
)

func init() {
	loopSecretOwnerMatch = loopSecretOwnerMatchUnix
}

func loopSecretOwnerMatchUnix(t *testing.T, check string, info os.FileInfo, login string) bool {
	t.Helper()
	return fileOwnerName(info) == login
}

func fileOwnerName(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if owner, err := user.LookupId(fmt.Sprint(stat.Uid)); err == nil {
			return owner.Username
		}
	}
	return ""
}
