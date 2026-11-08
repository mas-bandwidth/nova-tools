//go:build linux || darwin

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCloseInheritedFDsRefusesAnUnlistableDirectory: a descriptor list that cannot be
// read leaves every inherited descriptor open, so it is an error, never a quiet return.
func TestCloseInheritedFDsRefusesAnUnlistableDirectory(t *testing.T) {
	t.Parallel()
	assert.Error(t, closeInheritedFDs(filepath.Join(t.TempDir(), "missing")))
}
