package selftalk

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZScanningAMegabyteOfInstallationTextIsFast(t *testing.T) {
	t.Parallel()
	item := "I have no associative recall to drag anything back later.\n"
	padding := "The tree by the house has one lit window. Tree rings beat radiocarbon, and the correction moved Malta's temples earlier than the pyramids.\n"
	var sb strings.Builder
	sb.Grow(1000032)
	for sb.Len() < 1000032-len(item) {
		sb.WriteString(padding)
	}
	sb.WriteString(item)
	text := sb.String()

	runtime.GC()
	start := time.Now()
	got := ScanInstallation(text)
	elapsed := time.Since(start)

	assert.NotEmpty(t, got, "should find installation item in 1MB text")
	// wall-ok: 1MB scan performance budget
	assert.Less(t, elapsed, 30*time.Second, "ScanInstallation over 1MB must return within budget, took %v", elapsed)

	var piecesGot []Installation
	for _, line := range strings.Split(text, "\n") {
		piecesGot = append(piecesGot, ScanInstallation(line)...)
	}
	require.Equal(t, len(piecesGot), len(got), "must find the same items as over text in pieces")
	if len(piecesGot) > 0 && len(got) > 0 {
		assert.Equal(t, piecesGot[0].Shape, got[0].Shape)
		assert.Equal(t, piecesGot[0].Text, got[0].Text)
	}
}
