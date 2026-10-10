package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// defaultBriefRecord is the coordinator's record of brief decisions beside the
// home directory, <home>/nova-sprint/decide/brief.jsonl; a home that cannot be
// found is the fault it returns, the one briefGate says on a NOTE line.
func TestBriefdecideCoverDefaultBriefRecord(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, c := range []struct {
		name    string
		home    func() (string, error)
		want    string
		wantErr string
	}{
		{
			name: "beside the home",
			home: func() (string, error) { return home, nil },
			want: filepath.Join(home, "nova-sprint", "decide", "brief.jsonl"),
		},
		{
			name:    "a home that cannot be found",
			home:    func() (string, error) { return "", errors.New("$HOME is not defined") },
			wantErr: "$HOME is not defined",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.a.home = c.home
			record, err := ta.a.defaultBriefRecord()
			if c.wantErr != "" {
				require.Error(t, err)
				assert.EqualError(t, err, c.wantErr)
				assert.Empty(t, record, "a home that cannot be found names no record")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, record)
		})
	}
}

// readBriefBar is the sprint row's decide_brief_bar, read through withConfig: a
// config store that is not named (no --pg, no DSN) and one whose address is no
// DSN are the faults it returns, before anything is dialled. The read itself
// needs nova-config's Postgres (withConfig opens it over config.OpenPG and no
// app seam carries a config.Store), so the row-found path is covered through
// the a.briefBar seam the tests inject (briefdecide_test.go), not here.
func TestBriefdecideCoverReadBriefBar(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, dsn, want string }{
		{name: "no config store named", dsn: "", want: "--pg is required"},
		{name: "an address that is no DSN", dsn: "postgres://user@example.invalid:box/nova", want: config.EnvPG + " could not be parsed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.a.getenv = func(k string) string {
				if k == config.EnvPG {
					return c.dsn
				}
				return ""
			}
			bar, err := ta.a.readBriefBar(context.Background())
			require.Error(t, err)
			assert.Empty(t, bar, "no store answered: no bar")
			assert.Contains(t, err.Error(), c.want)
		})
	}
}
