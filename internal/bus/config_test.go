package bus

import (
	"strings"
	"testing"
)

func TestLoadConfigReadsTheRoster(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Senders(); len(got) != 2 || got[0] != "Ada" || got[1] != "Bo" {
		t.Fatalf("Senders() = %v, want [Ada Bo] -- Dana has no lane and is not a sender", got)
	}
	ada := mustParticipant(t, c, "the archivist")
	if ada.Name != "Ada" || ada.Slug() != "ada" {
		t.Fatalf("an alias resolved to %+v, want Ada with slug ada", ada)
	}
	if _, ok := c.Lookup("Adda"); ok {
		t.Fatal("a misspelling resolved; the whole point of the roster is that it does not")
	}
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeBus(t, map[string]string{ConfigName: tc.json})
			_, err := LoadConfig(root)
			if err == nil {
				t.Fatal("want a refusal, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestLoadConfigRefusesAMissingRoster(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := LoadConfig(root); err == nil {
		t.Fatal("a bus with no roster loaded")
	}
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("an empty bus root loaded")
	}
}
