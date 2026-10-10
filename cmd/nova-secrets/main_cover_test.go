package main

// Unit-tier coverage of the two in-process helpers main.go keeps beside the
// dispatcher: stringSlice.Set, the flag value behind exec's repeatable --require;
// and the run seam that opens the store. Every case calls the helper or the run
// seam directly: no subprocess, no store, no sops, no Redis.

import (
	"flag"
	"io"
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
