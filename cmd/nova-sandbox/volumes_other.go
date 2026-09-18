//go:build !darwin

// Every platform that is not darwin has no disposable-volume body, and this file is that
// absence stated rather than stubbed. The refusal a caller sees is run.go's, printed
// before anything is attempted and naming where the disposable place is on that platform
// instead; the manager here exists so the verb's LOGIC — which is platform-independent and
// unit-tested with a fake — still compiles everywhere.
package main

import (
	"fmt"
	"runtime"
)

type noVolumes struct{}

func newPlatformVolumes() volumeManager { return noVolumes{} }

func noBody() error {
	return fmt.Errorf("the disposable APFS volume is darwin's and %s has no body for it", runtime.GOOS)
}

func (noVolumes) Container() (string, error)  { return "", noBody() }
func (noVolumes) Exists(string) (bool, error) { return false, noBody() }
func (noVolumes) List() ([]diskVolume, error) { return nil, noBody() }
func (noVolumes) Create(string, string, string) (diskVolume, error) {
	return diskVolume{}, noBody()
}
func (noVolumes) Used(string) (int64, error) { return 0, noBody() }
func (noVolumes) Delete(string) error        { return noBody() }
