//go:build !windows

// Every platform that is not windows has no Job Object and no per-run scratch, and this
// file is that absence STATED rather than stubbed. The refusal a caller sees on linux is
// run.go's noDisposableBody, printed before anything is attempted; on darwin the verb never
// reaches this placer at all, because runVerb dispatches on runGOOS. The placer here exists
// so the windows half's LOGIC -- which is platform-independent and unit-tested with a fake
// in runwin_test.go -- still compiles everywhere.
//
// It does NOT reach for WSL, for a container, or for an ordinary directory. W11: a build
// that reached for WSL when AppContainer, the Job Object or Windows Sandbox was unavailable
// must refuse instead, and the same holds for a host that is not windows at all.
package main

import (
	"fmt"
	"runtime"
	"time"
)

type noWinPlace struct{}

func newPlatformWinPlace() winPlacer { return noWinPlace{} }

// winWallAvailable off windows is a question with one answer: there is no AppContainer on a
// machine that is not windows, so there is never a windows wall here. The windows body of
// this function asks internal/sandbox, which is where the answer will change when the
// AppContainer body lands.
func winWallAvailable() (string, bool) { return "appcontainer", false }

func noWinBody() error {
	return fmt.Errorf("the Job Object and the per-run scratch are windows's and %s has no body for them", runtime.GOOS)
}

func (noWinPlace) Exists(string) (bool, error)         { return false, noWinBody() }
func (noWinPlace) MakeScratch(string) error            { return noWinBody() }
func (noWinPlace) CreateJob(winLimits) (winJob, error) { return nil, noWinBody() }
func (noWinPlace) Start(winJob, winStartSpec) (winStarted, error) {
	return winStarted{}, noWinBody()
}
func (noWinPlace) CloseJob(winJob) error                          { return noWinBody() }
func (noWinPlace) Used(string) (int64, error)                     { return 0, noWinBody() }
func (noWinPlace) RemoveTree(string, string, time.Duration) error { return noWinBody() }
func (noWinPlace) WSBAvailable() (string, bool, error)            { return "", false, noWinBody() }
func (noWinPlace) WSBRunning() (string, bool, error)              { return "", false, noWinBody() }
func (noWinPlace) StartWSB(string, string) error                  { return noWinBody() }
