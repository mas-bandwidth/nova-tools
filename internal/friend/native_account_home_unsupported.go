//go:build (!linux && !darwin) || (darwin && osusergo)

package friend

import "errors"

func nativeAccountHome() (string, error) {
	return "", errors.New("native control has no trusted account lookup on this build; no HOME fallback")
}
