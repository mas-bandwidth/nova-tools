//go:build !linux && !darwin

package friend

import "errors"

func newToolResultWatch(string) (toolResultWatch, error) {
	return nil, errors.New("tool-result native directory notification requires Linux or Darwin; use a supported root receiver")
}
