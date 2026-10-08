//go:build windows

package friend

import "sync"

var laneMutex sync.Mutex

func withLaneLock(_ string, action func() (string, error)) (string, error) {
	laneMutex.Lock()
	defer laneMutex.Unlock()
	return action()
}
