// Package functionalrun implements the container-sandboxed functional test runner.
// For the lifecycle model, see tla/ContainerRun.tla.
package functionalrun

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"time"
)

// runIDRE is the shape of every run id this tool makes.
var runIDRE = regexp.MustCompile(`^[0-9]{8}t[0-9]{6}-[0-9a-f]{8}(-mod)?$`)

// newRunID is a run's id: the start time, and random hex.
func newRunID(start time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return start.UTC().Format("20060102t150405") + "-" + hex.EncodeToString(b[:])
}
