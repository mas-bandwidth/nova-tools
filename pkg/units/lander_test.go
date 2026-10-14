package units

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckUnitsHoldsTheSprintToOneLander pins the lander rule carried from nova-sprint
// 1ae32b3e (tla/OneLander.tla there): units --check marks a server unit that names
// neither --land nor --land=false, since whether it lands would be the binary's default,
// and marks any other unit that lands; a server unit that says either way is installed.
// It reads unit files written under a temporary directory only: nothing is loaded.
func TestCheckUnitsHoldsTheSprintToOneLander(t *testing.T) {
	t.Parallel()
	server, ok := UnitKindOf("server")
	require.True(t, ok)
	table, ok := UnitKindOf("table")
	require.True(t, ok)
	for _, goos := range []string{"darwin", "linux"} {
		for _, tc := range []struct {
			name string
			kind UnitKind
			args []string
			why  string // "" for installed
		}{
			{"server lands", server, []string{"--land"}, ""},
			{"server lands, spelled =true", server, []string{"--land=true"}, ""},
			{"server does not land", server, []string{"--land=false"}, ""},
			{"server says nothing", server, nil, "it names neither --land nor --land=false: whether the server lands would be the binary's default; run: nova-sprint install server --listen <address:port>"},
			{"another unit lands", table, []string{"--land"}, "it lands (--land), a second lander beside the server: only the server unit lands"},
			{"another unit lands, spelled =1", table, []string{"--land=1"}, "it lands (--land), a second lander beside the server: only the server unit lands"},
			{"another unit says it does not land", table, []string{"--land=false"}, ""},
			{"another unit says nothing", table, nil, ""},
		} {
			t.Run(goos+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				line := append(append([]string{"/opt/nova/bin/" + tc.kind.Tool}, tc.kind.Verb...), "127.0.0.1:7000")
				if tc.kind.Kind != "server" {
					line = append([]string{"/opt/nova/bin/" + tc.kind.Tool}, tc.kind.Verb...)
				}
				line = append(line, tc.args...)
				text, err := ServiceUnit{Kind: tc.kind, OS: goos, Args: line, Log: filepath.Join(dir, "log")}.Text()
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, tc.kind.File(goos)), []byte(text), 0o600))
				got, err := CheckUnits(dir, goos, []UnitKind{tc.kind})
				require.NoError(t, err)
				require.Len(t, got, 1)
				if tc.why == "" {
					assert.Equal(t, UnitInstalled, got[0].State, "why: %s", got[0].Why)
					return
				}
				assert.Equal(t, UnitDifferent, got[0].State)
				assert.Equal(t, tc.why, got[0].Why)
			})
		}
	}
}
