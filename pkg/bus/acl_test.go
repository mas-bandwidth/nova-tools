package bus

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friendACL is the least set per friend as SPEC-BUS.md writes it: the fenced
// line that starts "ACL SETUSER <f>", its lines joined.
func friendACL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../docs/SPEC-BUS.md")
	require.NoError(t, err)
	doc := string(raw)
	at := strings.Index(doc, "```\nACL SETUSER <f> ")
	require.GreaterOrEqual(t, at, 0, "SPEC-BUS.md writes the friend's ACL line")
	block := doc[at+4:]
	block = block[:strings.Index(block, "```")]
	return strings.Join(strings.Fields(block), " ")
}

// aclRules is an ACL SETUSER line split into the root's rules and each
// selector's, a selector being the rules inside one pair of parentheses.
func aclRules(line string) (root []string, selectors [][]string) {
	var sel []string
	in := false
	for _, w := range strings.Fields(line) {
		switch {
		case !in && strings.HasPrefix(w, "("):
			in, sel = true, nil
			w = strings.TrimPrefix(w, "(")
		}
		if !in {
			root = append(root, w)
			continue
		}
		end := strings.HasSuffix(w, ")")
		if w = strings.TrimSuffix(w, ")"); w != "" {
			sel = append(sel, w)
		}
		if end {
			selectors = append(selectors, sel)
			in = false
		}
	}
	return root, selectors
}

// overwrites are the commands that read or replace a key of any type by its
// name alone; a send needs them on its token's record and nowhere else.
var overwrites = map[string]bool{"+get": true, "+set": true, "+getset": true, "+setex": true, "+psetex": true, "+getdel": true, "+del": true, "+unlink": true, "+rename": true, "+copy": true, "+restore": true, "+@all": true, "+@write": true, "+@string": true, "+@keyspace": true, "allcommands": true}

// aclBreaches says where a friend's ACL line lets a friend read or overwrite
// a key that is no token record of its own: an overwriting command on the
// root (which reaches every key pattern the user has), or in a selector
// whose keys are wider than ~bus2:sent:<f>:*.
func aclBreaches(line string) []string {
	var out []string
	root, selectors := aclRules(line)
	var keys []string
	for _, r := range root {
		if strings.HasPrefix(r, "~") {
			keys = append(keys, r)
		}
	}
	for _, r := range root {
		if overwrites[r] {
			out = append(out, r+" on the root reaches "+strings.Join(keys, " "))
		}
	}
	for _, sel := range selectors {
		var cmds, wide []string
		for _, r := range sel {
			switch {
			case overwrites[r]:
				cmds = append(cmds, r)
			case r == "~bus2:sent:<f>:*":
			case strings.HasPrefix(r, "~") || strings.HasPrefix(r, "%") || r == "allkeys":
				wide = append(wide, r)
			}
		}
		if len(cmds) > 0 && len(wide) > 0 {
			out = append(out, strings.Join(cmds, " ")+" in a selector reaches "+strings.Join(wide, " "))
		}
	}
	return out
}

// The least set per friend in SPEC-BUS.md gives GET and SET on the sender's
// own token records and on nothing else, so no friend can SET friends or
// bus2:log; the send-once script's keys are still on the root, so EVAL runs.
// The reversed witnesses are the line attempt 2 wrote (+get +set on the root)
// and a selector wider than the records: each is a breach.
func TestTheFriendACLSetsOnlyItsOwnTokenRecords(t *testing.T) {
	t.Parallel()
	line := friendACL(t)
	assert.Empty(t, aclBreaches(line), line)

	root, selectors := aclRules(line)
	assert.Contains(t, root, "~bus2:sent:<f>:*", "EVAL's declared keys pass on the root")
	assert.Contains(t, root, "+eval")
	assert.Contains(t, root, "+evalsha")
	assert.Contains(t, selectors, []string{"~bus2:sent:<f>:*", "+get", "+set"})

	t.Run("reversed: +get +set on the root", func(t *testing.T) {
		wide := strings.Replace(line, " (~bus2:sent:<f>:* +get +set)", " +get +set", 1)
		require.NotEqual(t, line, wide)
		got := aclBreaches(wide)
		require.Len(t, got, 2)
		assert.Contains(t, got[0], "+get on the root reaches")
		assert.Contains(t, got[1], "~friends")
		assert.Contains(t, got[1], "~bus2:log")
	})
	t.Run("reversed: a selector wider than the records", func(t *testing.T) {
		got := aclBreaches(strings.Replace(line, "(~bus2:sent:<f>:* +get +set)", "(~bus2:sent:<f>:* ~friends +get +set)", 1))
		assert.Equal(t, []string{"+get +set in a selector reaches ~friends"}, got)
	})
}
