// Unit coverage for the two entry points the unit tier's per-function table
// shows at zero: DeployACLs and aclArgs. The store behind DeployACLs is a
// fake: a go-redis client whose connections are the near ends of net.Pipes,
// answered by an in-process RESP server, the way connect_trips_test.go
// speaks. No socket, no clock, no subprocess, no Redis or Postgres.
package store

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aclFake is a Dialer for a go-redis client whose store is an in-process
// net.Pipe server: it answers HELLO (helloAccepted, as
// TestConnectIsHelloAlone) and every further command with reply(cmd),
// recording each command it is sent. No socket is opened and nothing waits
// on a clock.
type aclFake struct {
	mu    sync.Mutex
	cmds  [][]string
	reply func(cmd []string) string
}

func (f *aclFake) dial(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }() // ignored: a test fixture's connection ends when its client hangs up
		r := bufio.NewReader(server)
		for {
			cmd, err := readCommand(r)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.cmds = append(f.cmds, cmd)
			f.mu.Unlock()
			resp := f.reply(cmd)
			if strings.EqualFold(cmd[0], "hello") {
				resp = helloAccepted
			}
			if _, err := io.WriteString(server, resp); err != nil {
				return
			}
		}
	}()
	return client, nil
}

// aclCmds is the commands the fake was sent without a connection's setup:
// handshake(cmd) of trips.go marks the commands go-redis sends while it
// sets up a connection (hello, auth, client, select, readonly), so only
// what DeployACLs itself sends comes back.
func (f *aclFake) aclCmds() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, cmd := range f.cmds {
		switch strings.ToLower(cmd[0]) {
		case "hello", "auth", "client", "select", "readonly":
		default:
			out = append(out, cmd)
		}
	}
	return out
}

// aclCoverClient is a client whose store is the fake: the Dialer seam means
// nothing dials a host.
func aclCoverClient(t *testing.T, f *aclFake) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "store.test:6379", Dialer: f.dial, DisableIdentity: true})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// wantAclCmd is the ACL SETUSER one rule must arrive as: the user's name,
// on, then the rule's own arguments with a selector whole.
func wantAclCmd(rule string) []string {
	parts := aclArgs(rule)
	return append([]string{"ACL", "SETUSER", parts[0], "on"}, parts[1:]...)
}

// TestAclCoverDeployACLs pins DeployACLs over a fake store: the main path
// sends one ACL SETUSER per rule, in ACLRules order, each turning its user
// on with the rule's own arguments and never a password, and a store that
// refuses one user is answered: the refusal comes back naming the user and
// no rule after it is sent.
func TestAclCoverDeployACLs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		reply func(cmd []string) string
		scene func(t *testing.T, f *aclFake, err error)
	}{
		{
			name:  "main path: one SETUSER per rule, in order, on, with no password",
			reply: func([]string) string { return "+OK\r\n" },
			scene: func(t *testing.T, f *aclFake, err error) {
				require.NoError(t, err)
				sent := f.aclCmds()
				require.Len(t, sent, len(ACLRules), "one command per rule")
				for i, rule := range ACLRules {
					assert.Equal(t, wantAclCmd(rule), sent[i], "rule %d arrives as its own SETUSER, in order", i)
					assert.Equal(t, strings.Fields(rule)[0], sent[i][2], "the command's user is the rule's first word")
				}
				for _, cmd := range sent {
					for _, a := range cmd {
						assert.False(t, strings.HasPrefix(a, ">"), "%q carries a password; the deploy leaves passwords unchanged", cmd[2])
					}
				}
			},
		},
		{
			name: "refusal: a store that refuses one user stops there and names it",
			reply: func(cmd []string) string {
				if len(cmd) > 2 && cmd[2] == "ns-reconciler" {
					return "-NOPERM ns-reconciler is not allowed\r\n"
				}
				return "+OK\r\n"
			},
			scene: func(t *testing.T, f *aclFake, err error) {
				require.Error(t, err)
				assert.ErrorContains(t, err, "NOPERM")
				assert.ErrorContains(t, err, "ns-reconciler")
				sent := f.aclCmds()
				require.Len(t, sent, 3, "the rules after the refused user are not sent")
				assert.Equal(t, "ns-reconciler", sent[2][2], "the refusal is the third rule's user")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &aclFake{reply: tc.reply}
			err := DeployACLs(context.Background(), aclCoverClient(t, f))
			tc.scene(t, f, err)
		})
	}
}

// TestAclCoverAclArgsSplitsRule pins aclArgs's split of a rule: plain words
// split on spaces, a parenthesised selector stays one argument, a nested
// selector too, repeated and edge spaces collapse, and an empty rule sends
// nothing. aclArgs has no refusing path; an empty rule is its zero case.
func TestAclCoverAclArgsSplitsRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rule string
		want []string
	}{
		{"main path: a plain rule splits on its spaces", "ns-a on +ping ~s:*", []string{"ns-a", "on", "+ping", "~s:*"}},
		{"a selector stays one argument", "ns-b (+get +set ~bench:*:owner)", []string{"ns-b", "(+get +set ~bench:*:owner)"}},
		{"a nested selector stays one argument", "ns-c (a (b c)) d", []string{"ns-c", "(a (b c))", "d"}},
		{"repeated and edge spaces collapse", "  ns-d   on  ", []string{"ns-d", "on"}},
		{"an empty rule sends nothing", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, aclArgs(tc.rule), "aclArgs(%q)", tc.rule)
		})
	}
}

// TestAclCoverAclArgsSplitsEveryLiveRule pins aclArgs over the live rules:
// every rule of ACLRules splits with its user as the first argument, no
// argument empty, and only a selector carrying a space.
func TestAclCoverAclArgsSplitsEveryLiveRule(t *testing.T) {
	t.Parallel()
	require.NotEmpty(t, ACLRules)
	for _, rule := range ACLRules {
		t.Run(strings.Fields(rule)[0], func(t *testing.T) {
			t.Parallel()
			parts := aclArgs(rule)
			require.NotEmpty(t, parts)
			assert.True(t, strings.HasPrefix(parts[0], "ns-"), "the first argument is the rule's user, got %q", parts[0])
			for i, p := range parts {
				assert.NotEmpty(t, p, "argument %d is empty", i)
				if strings.Contains(p, " ") {
					assert.True(t, strings.HasPrefix(p, "("), "only a selector holds a space: argument %d is %q", i, p)
				}
			}
		})
	}
}
