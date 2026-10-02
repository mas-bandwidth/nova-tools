package bus

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigReadsTheRoster(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	c, err := LoadConfig(root)
	require.NoError(t, err)
	{
		got := c.Senders()
		require.False(t, len(got) != 2 || got[0] != "Ada" || got[1] != "Bo", "Senders() = %v, want [Ada Bo] -- Dana has no lane and is not a sender", got)
	}
	ada := mustParticipant(t, c, "the archivist")
	require.False(t, ada.Name != "Ada" || ada.Slug() != "ada", "an alias resolved to %+v, want Ada with slug ada", ada)
	_, ok := c.Lookup("Adda")
	require.False(t, ok, "a misspelling resolved; the whole point of the roster is that it does not")
}

// Every refusal here is a roster a bus could otherwise run on for weeks before two names
// collided at the wrong moment.
func TestLoadConfigRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, json, want string
	}{
		{"an unknown field", `{"participants":[{"name":"Ada","lane":"from-ada","aliass":["x"],"git_name":"R","git_email":"r@x"}]}`, "aliass"},
		{"no participants", `{"participants":[]}`, "no participants"},
		{"an empty name", `{"participants":[{"name":"  ","lane":"from-a","git_name":"R","git_email":"r@x"}]}`, "empty name"},
		{"one name for two people", `{"participants":[
			{"name":"Ada","lane":"from-ada","git_name":"R","git_email":"r@x"},
			{"name":"ada","lane":"from-other","git_name":"O","git_email":"o@x"}]}`, "names both"},
		{"an alias colliding with a name", `{"participants":[
			{"name":"Ada","lane":"from-ada","git_name":"R","git_email":"r@x"},
			{"name":"Bo","lane":"from-bo","aliases":["ADA"],"git_name":"S","git_email":"s@x"}]}`, "names both"},
		{"two people in one lane", `{"participants":[
			{"name":"Ada","lane":"from-ada","git_name":"R","git_email":"r@x"},
			{"name":"Bo","lane":"from-ada","git_name":"S","git_email":"s@x"}]}`, "belongs to both"},
		{"a lane that is not from-<slug>", `{"participants":[{"name":"R","lane":"notes","git_name":"R","git_email":"r@x"}]}`, "a lane is from-<slug>"},
		{"a lane escaping the bus", `{"participants":[{"name":"R","lane":"from-../../etc","git_name":"R","git_email":"r@x"}]}`, "lower-case letters"},
		{"a lane with no git identity", `{"participants":[{"name":"R","lane":"from-r"}]}`, "needs git_name and git_email"},
		{"a git identity with no lane", `{"participants":[{"name":"R","git_name":"R","git_email":"r@x"}]}`, "no lane"},
		{"a group naming a stranger", `{"participants":[{"name":"R","lane":"from-r","git_name":"R","git_email":"r@x"}],
			"groups":[{"name":"All","members":["R","Nobody"]}]}`, `member "Nobody"`},
		{"a group that is also a person", `{"participants":[{"name":"R","lane":"from-r","git_name":"R","git_email":"r@x"}],
			"groups":[{"name":"r","members":["R"]}]}`, "is also participant"},
		{"an empty group", `{"participants":[{"name":"R","lane":"from-r","git_name":"R","git_email":"r@x"}],
			"groups":[{"name":"All","members":[]}]}`, "no members"},
		{"two JSON values", `{"participants":[{"name":"R","lane":"from-r","git_name":"R","git_email":"r@x"}]} {"participants":[]}`, "more than one JSON value"},
		{"a duplicate key", `{"participants":[{"name":"Ada","name":"Eve","lane":"from-ada","git_name":"R","git_email":"r@x"}]}`, "duplicate key"},
		{"a name with a newline", `{"participants":[{"name":"Ada\nBo","lane":"from-a","git_name":"R","git_email":"r@x"}]}`, "one line"},
		{"an alias with a newline", `{"participants":[{"name":"Ada","lane":"from-a","aliases":["E\nvil"],"git_name":"R","git_email":"r@x"}]}`, "one line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeBus(t, map[string]string{ConfigName: tc.json})
			_, err := LoadConfig(root)
			require.Error(t, err, "want a refusal, got none")
			require.Contains(t, err.Error(), tc.want, "refusal %q does not name %q", err, tc.want)
		})
	}
}

func TestLoadConfigRefusesAMissingRoster(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := LoadConfig(root)
	require.Error(t, err, "a bus with no roster loaded")
	_, err = LoadConfig("")
	require.Error(t, err, "an empty bus root loaded")
}

// A roster a cold reader got wrong is refused with the shape it should have: a missing
// file, an array where the object goes, and an empty roster each carry the one-line
// example and the door to the help, where the Go decoder's own words said only that an
// array could not be unmarshalled.
func TestLoadConfigRefusalsCarryTheRosterShape(t *testing.T) {
	t.Parallel()
	for name, files := range map[string]map[string]string{
		"no file":        {},
		"an array":       {ConfigName: `[{"name":"Ada"}]`},
		"not json":       {ConfigName: `participants: Ada`},
		"no participant": {ConfigName: `{"participants":[]}`},
	} {
		root := t.TempDir()
		for n, body := range files {
			write(t, root, n, body)
		}
		_, err := LoadConfig(root)
		if err == nil {
			assert.Failf(t, "assertion failed", "%s: loaded", name)
			continue
		}
		for _, want := range []string{`"participants":[{"name":"Ada","lane":"from-ada"`, "git_email", "ROSTER AND LANES"} {
			assert.Contains(t, err.Error(), want, "%s: the refusal %q does not carry %q", name, err, want)
		}
	}
}

// Onboarding point 2: malformed user input names the expected shape, without Go types.
func TestRosterShapeErrorsNameJSONInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, want string }{
		{"array", `[1,2]`, "participants.json must be one JSON object"},
		{"null", `null`, "participants.json must be one JSON object"},
		{"field", `{"participants":"Ada"}`, `field "participants" has the wrong JSON value type`},
		{"name", `{"participants":[{"name":1}]}`, `field "participants.name" has the wrong JSON value type`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadConfig(writeBus(t, map[string]string{ConfigName: tc.raw}))
			require.Error(t, err)
			// Go 1.27 includes the array index in UnmarshalTypeError.Field;
			// earlier supported toolchains identify the same field without it.
			assert.Contains(t, strings.ReplaceAll(err.Error(), "participants.0.name", "participants.name"), tc.want)
			assert.NotContains(t, err.Error(), "Go value")
			assert.NotContains(t, err.Error(), "bus.Config")
		})
	}
}
