//go:build !linux && !darwin

package pg

import "os/exec"

type watchdog struct{ group int }

func startWatchdog() (*watchdog, error) { return &watchdog{}, nil }
func (w *watchdog) alive() error        { return nil }
func (w *watchdog) close()              {}
func joinWatchdog(_ *exec.Cmd, _ int)   {}
