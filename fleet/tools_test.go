package fleet

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestRestartStepPresent(t *testing.T) {
	data, err := os.ReadFile("tools.yml")
	assert.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "bootout")
	assert.Contains(t, content, "bootstrap")
}

func TestVersionCheckPresent(t *testing.T) {
	data, err := os.ReadFile("tools.yml")
	assert.NoError(t, err)

	// This is the "red" test: it expects a version check that doesn't exist yet.
	assert.Contains(t, string(data), "VERSION CHECK: ensure each restarted member, reader and friend runs the new version")
}
