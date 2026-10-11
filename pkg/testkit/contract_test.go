package testkit_test

import (
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
)

// silentTool answers nothing at exit 0: it breaches every probe that wants a
// refusal or an answer.
var silentTool = testkit.Main(func([]string, io.Reader, io.Writer, io.Writer) int { return 0 })

// TestContractHoldsACompliantTool pins the passing path: a tool built on the
// skeleton (verb_test.go's demo) passes every probe in one call.
func TestContractHoldsACompliantTool(t *testing.T) {
	t.Parallel()
	testkit.Contract(t, demo, []string{"send", "version"})
}

// TestContractReportsEveryBreach pins the failing path: a tool that breaches
// the probes is reported, naming which probe broke, so a mutation that drops a
// probe turns this red.
func TestContractReportsEveryBreach(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	testkit.Contract(rec, silentTool, []string{"send"})
	assert.True(t, rec.failed, "Contract passed a tool that breaches every probe")
	assert.Contains(t, rec.msg, "bare command")
	assert.Contains(t, rec.msg, "unknown verb")
	assert.Contains(t, rec.msg, "-h: help is on stdout")
}
