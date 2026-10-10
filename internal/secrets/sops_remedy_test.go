package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSopsRemedyDarwinNamesBrew checks that darwin returns the brew remedy.
func TestSopsRemedyDarwinNamesBrew(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "brew install sops", remedy("darwin", "sops", "install", "3.13.3"))
	assert.Equal(t, "brew upgrade sops", remedy("darwin", "sops", "upgrade", "3.13.3"))
	assert.Equal(t, "brew install age", remedy("darwin", "age", "install", "1.3.2"))
	assert.Equal(t, "brew upgrade age", remedy("darwin", "age", "upgrade", "1.3.2"))
}

// TestSopsRemedyLinuxNamesNoBrew checks that linux names no brew command.
func TestSopsRemedyLinuxNamesNoBrew(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "install sops 3.13.3 or newer from the tool's release binaries into ~/.local/bin", remedy("linux", "sops", "install", "3.13.3"))
	assert.Equal(t, "upgrade sops to 3.13.3 or newer from the tool's release binaries into ~/.local/bin", remedy("linux", "sops", "upgrade", "3.13.3"))
	assert.Equal(t, "install age 1.3.2 or newer from the tool's release binaries into ~/.local/bin", remedy("linux", "age", "install", "1.3.2"))
	assert.Equal(t, "upgrade age to 1.3.2 or newer from the tool's release binaries into ~/.local/bin", remedy("linux", "age", "upgrade", "1.3.2"))
}

// TestSopsRemedyWindowsNamesNoBrew checks that windows names no brew command.
func TestSopsRemedyWindowsNamesNoBrew(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "install sops 3.13.3 or newer from the tool's release binaries into ~/.local/bin", remedy("windows", "sops", "install", "3.13.3"))
	assert.Equal(t, "upgrade sops to 3.13.3 or newer from the tool's release binaries into ~/.local/bin", remedy("windows", "sops", "upgrade", "3.13.3"))
	assert.Equal(t, "install age 1.3.2 or newer from the tool's release binaries into ~/.local/bin", remedy("windows", "age", "install", "1.3.2"))
	assert.Equal(t, "upgrade age to 1.3.2 or newer from the tool's release binaries into ~/.local/bin", remedy("windows", "age", "upgrade", "1.3.2"))
}

// TestSopsRemedyNamesTheMinimumVersion checks that the minimum version is named in the remedy.
func TestSopsRemedyNamesTheMinimumVersion(t *testing.T) {
	t.Parallel()
	assert.Contains(t, remedy("linux", "sops", "install", "3.13.3"), "3.13.3")
	assert.Contains(t, remedy("linux", "age", "install", "1.3.2"), "1.3.2")
}
