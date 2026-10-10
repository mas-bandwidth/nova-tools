package main

// main_cover_test.go is the per-function cover for unconfirmed in main.go,
// the one function the unit tier's per-function coverage table held at 0.0%
// (go tool cover -func). unconfirmed reads nothing from its connection but
// String(), so the unit tier reaches it with a zero-value *redisconn.Conn
// and an error made here: no store, no dial, no subprocess. It has no
// refusal arm of its own; its one branch is errors.Unwrap, so both rows pin
// that: a cause an error wraps, and an error that wraps nothing.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverStr returns a pointer to a copy of s, for a login's flag fields.
func coverStr(s string) *string { return &s }

// TestMainCoverUnconfirmedNamesTheLostTransaction pins unconfirmed's one line
// for a transaction whose confirmation was lost: the UNCONFIRMED word at exit
// 1, the key, the store's own string with the cause, and a recall remedy that
// carries the same login and the owner and name as shell words.
func TestMainCoverUnconfirmedNamesTheLostTransaction(t *testing.T) {
	t.Parallel()

	// A zero-value Conn answers String() with the zero login's words; the
	// function reads nothing else from it, so no store is opened.
	conn := &redisconn.Conn{}
	connString := conn.String()

	cases := []struct {
		name       string
		err        error
		cause      string
		store      login
		owner      string
		keyName    string
		key        string
		wantRecall string
	}{
		{
			name:  "an error that wraps a cause names the cause",
			err:   fmt.Errorf("redis: the connection dropped: %w", errors.New("EOF")),
			cause: "EOF",
			store: login{
				addr:        coverStr("127.0.0.1:6379"),
				user:        coverStr(""),
				passwordEnv: coverStr(PasswordEnv),
				givenFn:     func(string) bool { return false },
			},
			owner:      "ada",
			keyName:    "note",
			key:        "ada:note",
			wantRecall: "nova-redis recall --redis 127.0.0.1:6379 --owner ada --name note",
		},
		{
			name:  "an error that wraps nothing names itself",
			err:   errors.New("the reply did not come"),
			cause: "the reply did not come",
			store: login{
				addr:        coverStr("127.0.0.1:6379"),
				user:        coverStr("ada"),
				passwordEnv: coverStr("STORE_PW"),
				givenFn:     func(string) bool { return true },
			},
			owner:      "a b",
			keyName:    "it's",
			key:        "a b:it's",
			wantRecall: "nova-redis recall --redis 127.0.0.1:6379 --user ada --password-env STORE_PW --owner 'a b' --name 'it'\"'\"'s'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := unconfirmed(conn, tc.store, tc.owner, tc.keyName, tc.err)

			require.NotNil(t, got)
			assert.Equal(t, tool.Failed, got.Status)
			assert.Equal(t, 1, got.Exit)
			assert.Equal(t, "UNCONFIRMED", got.Word)
			assert.Empty(t, got.Why)

			assert.Equal(t, tc.key, coverFact(t, got, "key"))
			assert.Equal(t, connString+": the transaction was sent and its reply was lost: "+tc.cause, coverFact(t, got, "err"))
			assert.Equal(t, tool.Text("confirmation was lost after the transaction was sent, so the write may have committed; read it back with the same login before spilling again: "+tc.wantRecall), coverFact(t, got, "remedy"))
		})
	}
}

// coverFact is the value of a fact in o, and fails the test when o does not
// carry it.
func coverFact(t *testing.T, o *tool.Out, key string) any {
	t.Helper()
	for _, f := range o.Facts {
		if f.K == key {
			return f.V
		}
	}
	require.Failf(t, "fact missing", "%q is not among %v", key, o.Facts)
	return nil
}
