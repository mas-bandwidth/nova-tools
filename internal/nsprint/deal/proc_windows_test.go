//go:build windows

package deal

import "os"

// processAlive on Windows: FindProcess succeeds only for a live pid.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
