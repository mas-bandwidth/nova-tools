package main

// Unit-tier coverage of the three in-process helpers main.go keeps beside the
// dispatcher: stringSlice.Set, the flag value behind exec's repeatable --require;
// friendWidthName, the token grammar of the sprint table's working column key
// friend:<name>:width; and refusedWidthHandWrite, the one command-line check exec
// runs before the store is opened (nova-tools#2676, #3447). Every case calls the
// helper or the run seam directly: no subprocess, no store, no sops, no Redis.

import (
	"bytes"
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMainCoverStringSliceSetAccumulatesInFlagOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		vals       []string
		want       stringSlice
		wantString string
	}{
		{"one value is held", []string{"GH_TOKEN"}, stringSlice{"GH_TOKEN"}, "GH_TOKEN"},
		{"repeated Sets keep call order", []string{"GH_TOKEN", "API_KEY"}, stringSlice{"GH_TOKEN", "API_KEY"}, "GH_TOKEN,API_KEY"},
		{"an empty value is still a value: Set refuses nothing", []string{""}, stringSlice{""}, ""},
		{"no Set at all leaves the slice empty", nil, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s stringSlice
			for _, v := range tt.vals {
				require.NoError(t, s.Set(v))
			}
			assert.Equal(t, tt.want, s)
			assert.Equal(t, tt.wantString, s.String())
		})
	}
}

func TestMainCoverStringSliceSetBacksTheRequireFlagThroughTheParser(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		want    stringSlice
		wantErr string
	}{
		{"two --require flags reach Set in order", []string{"--require", "GH_TOKEN", "--require", "API_KEY"}, stringSlice{"GH_TOKEN", "API_KEY"}, ""},
		{"a --require named once holds one name", []string{"--require", "GH_TOKEN"}, stringSlice{"GH_TOKEN"}, ""},
		{"a --require with no value is refused by the parser, never by Set", []string{"--require"}, nil, "flag needs an argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := flag.NewFlagSet("exec", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			var requireFlags stringSlice
			fs.Var(&requireFlags, "require", "a key `NAME` that must be in the seat's file")
			err := fs.Parse(tt.args)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, requireFlags)
		})
	}
}

func TestMainCoverFriendWidthNameAcceptsOnlyTheWidthColumnKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tok  string
		want string
	}{
		{"the width key names its friend", "friend:ada:width", "ada"},
		{"a seat name may hold - and _", "friend:ada-lace_v2:width", "ada-lace_v2"},
		{"an ordinary secret name is not the column", "GH_TOKEN", ""},
		{"another table's width key is not the column", "task:ada:width", ""},
		{"a friend row's other column is not the column", "friend:ada:leased", ""},
		{"a prefix alone is not the column", "friend:", ""},
		{"the empty friend is no seat", "friend::width", ""},
		{"a name no seat can hold is refused", "friend:ada.lab:width", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, friendWidthName(tt.tok))
		})
	}
}

func TestMainCoverRefusedWidthHandWriteJudgesTheLineNotThePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cmdArgs  []string
		wantErrs []string
	}{
		{
			"a hand-write of the width column is refused",
			[]string{"/usr/local/bin/redis-cli", "SET", "friend:ada:width", "3"},
			[]string{"redis-cli writing friend:ada:width by hand through nova-secrets exec is refused", "written only by the friend row loop", "nova-tools #3447"},
		},
		{"the write verb is taken in any case", []string{"redis-cli", "mset", "friend:ada:width", "1"}, []string{"friend:ada:width"}},
		{"removing the column is a write too", []string{"redis-cli", "DEL", "friend:ada:width"}, []string{"friend:ada:width"}},
		{"redis-cli.exe is the same tool", []string{"redis-cli.exe", "SETNX", "friend:bo:width", "1"}, []string{"friend:bo:width"}},
		{"a read of the column still runs", []string{"redis-cli", "GET", "friend:ada:width"}, nil},
		{"a write of another key still runs", []string{"redis-cli", "SET", "other:key", "1"}, nil},
		{"another command may hold the key", []string{"gh", "api", "friend:ada:width"}, nil},
		{"a width token with no write verb still runs", []string{"redis-cli", "KEYS", "friend:ada:width"}, nil},
		{"a bare redis-cli still runs", []string{"redis-cli"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := refusedWidthHandWrite(tt.cmdArgs)
			if len(tt.wantErrs) == 0 {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tt.wantErrs {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestMainCoverExecRefusesWidthHandWriteBeforeAnyStoreOpens(t *testing.T) {
	t.Parallel()
	td := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"exec",
		"--store", filepath.Join(td, "no-such-store"),
		"--as", "ada",
		"--key", filepath.Join(td, "no-such.key"),
		"--sops", filepath.Join(td, "no-sops"),
		"--only", "REDISCLI_AUTH",
		"--require", "REDISCLI_AUTH",
		"--", "redis-cli", "SET", "friend:ada:width", "3",
	}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 125, code, "exec's refusals stand apart from the command's statuses; stderr: %s", stderr.String())
	assert.Empty(t, stdout.String(), "a refusal prints nothing on stdout")
	assert.Equal(t, 1, strings.Count(stderr.String(), "\n"), "one refusal line: %q", stderr.String())
	assert.Contains(t, stderr.String(), "SECRETS EXEC REFUSED: redis-cli writing friend:ada:width by hand through nova-secrets exec is refused")
	assert.Contains(t, stderr.String(), "nova-tools #3447")
}
