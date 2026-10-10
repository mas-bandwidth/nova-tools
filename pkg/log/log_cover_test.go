package log

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ProcessGUID, bootID and processStart are the three functions of this package a test
// reaches by CALLING them: SPEC-LOGS.md Part 1 fixes the production guid as
// `<boot>-<pid>-<start>`, and every other test here injects a Clock and a GUIDSource
// instead of reading the host. The cases below therefore pin the SHAPE the spec fixes and
// the absence each read names, and never a host's value, so the same rows pass on a host
// without /proc, where the spec says `noboot` and `nostart` stand in. No sleep, no clock,
// no socket, no child, no store.

// a-process-guid-is-boot-dash-pid-dash-start: the joined guid is the spec's three parts in
// the spec's order, this process's pid in the middle, and one field of it -- no whitespace
// anywhere -- because `guid` is fixed vocabulary that Write neither escapes nor redacts.
func TestLogCoverProcessGUIDJoinsBootPidAndStart(t *testing.T) {
	t.Parallel()

	boot, start := bootID(), processStart()
	pid := strconv.Itoa(os.Getpid())
	guid := ProcessGUID()

	cases := []struct{ name, have, want string }{
		{"the join the spec fixes", guid, boot + "-" + pid + "-" + start},
		{"a second call answers the same guid", guid, ProcessGUID()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.have, "%s: ProcessGUID() = %q, want %q", c.name, c.have, c.want)
		})
	}
	assert.NotEmpty(t, guid, "ProcessGUID answered the empty string")
	assert.Contains(t, guid, "-"+pid+"-", "the guid does not carry this process's pid: %q", guid)
	assert.False(t, strings.ContainsAny(guid, " \t\n"), "a part of the guid holds whitespace, so it is not one field: %q", guid)
}

// a-proc-read-names-its-absence-rather-than-answering-empty: each of the two reads hands
// back either the kernel's own value, built from the alphabet named in its row, or the
// absence the function names. An empty answer is what a LogQL query cannot tell from
// "not this scope", so naming the absence is the refusal these functions are allowed.
func TestLogCoverBootIDAndProcessStartAnswerOrNameTheirAbsence(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, have, absent, letters string }{
		{"bootID", bootID(), "noboot", "0123456789abcdef-"},
		{"processStart", processStart(), "nostart", "0123456789"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.NotEmpty(t, c.have, "%s answered the empty string instead of naming its absence", c.name)
			assert.False(t, strings.ContainsAny(c.have, " \t\n"), "%s did not trim what it read: %q", c.name, c.have)
			if c.have == c.absent {
				t.Logf("%s names its absence on this host: the read failed, so %s stands in", c.name, c.absent)
				return
			}
			assert.Empty(t, strings.Trim(c.have, c.letters),
				"%s = %q, want the kernel's value built from %q, or the absence %q", c.name, c.have, c.letters, c.absent)
		})
	}
}

// the-production-guid-is-an-injectable-source-and-a-run-shares-it: ProcessGUID is a
// GUIDSource, so a caller hands it to New exactly as pkg/fleet's certify does; the
// guid it answers is the one the written line carries, untouched, and every line of the
// run carries the same one, which is what makes a guid a run's identity.
func TestLogCoverProcessGUIDIsAnInjectableSourceSharedByEveryLine(t *testing.T) {
	t.Parallel()

	var guid GUIDSource = ProcessGUID
	first := New(fixedClock(), guid, "nova-pulse")
	require.NotEmpty(t, first.GUID, "New with ProcessGUID left the guid field empty")

	var buf bytes.Buffer
	err := first.Write(&buf)
	require.NoError(t, err, "Write: %v", err)
	raw := buf.String()
	require.True(t, strings.Count(raw, "\n") == 1 && strings.HasSuffix(raw, "\n"),
		"the process guid split the JSON line: %q", raw)

	var got struct {
		GUID string `json:"guid"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &got), "not one JSON object: %s", raw)
	assert.Equal(t, first.GUID, got.GUID, "the written guid = %q, want the process guid %q", got.GUID, first.GUID)

	second := New(fixedClock(), guid, "nova-pulse")
	assert.Equal(t, first.GUID, second.GUID, "two lines of one run carried different guids: %q and %q", first.GUID, second.GUID)
}
