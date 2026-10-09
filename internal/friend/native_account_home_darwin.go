//go:build darwin && !osusergo

package friend

import (
	"errors"
	"os"
	"os/user"
	"strconv"
)

// Go's Darwin native getpwuid path is used even with CGO_ENABLED=0. Builds
// with osusergo are refused separately because Current can use HOME there.
func nativeAccountHome() (string, error) {
	u, err := user.Current()
	if err != nil || u.Uid != strconv.Itoa(os.Geteuid()) || u.HomeDir == "" {
		return "", errors.New("native control cannot verify the OS account home")
	}
	return u.HomeDir, nil
}
