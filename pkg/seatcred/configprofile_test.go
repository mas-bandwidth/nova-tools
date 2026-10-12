package seatcred

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfigProfileReadsARowSkippingBlanksCommentsAndCRLF: blank lines and
// # lines are skipped, a CRLF ending is trimmed, and a two- and a three-column
// row both parse.
func TestLoadConfigProfileReadsARowSkippingBlanksCommentsAndCRLF(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "seats.tsv")
	body := "# the fleet play\n\n   \ncoordinator\tpostgres://coordinator@db.invalid:5432/nova\tNOVA_PG_PASSWORD\r\nbench\tpostgres://bench@db.invalid:5432/nova\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	coord, err := LoadConfigProfile(path, "coordinator")
	require.NoError(t, err, "LoadConfigProfile = %+v %v; want the three-column row", coord, err)
	require.Equal(t, ConfigProfile{Name: "coordinator", DSN: "postgres://coordinator@db.invalid:5432/nova", PasswordEnv: "NOVA_PG_PASSWORD"}, coord,
		"LoadConfigProfile = %+v %v; want the three-column row", coord, err)

	bench, err := LoadConfigProfile(path, "bench")
	require.NoError(t, err, "LoadConfigProfile = %+v %v; want the two-column row", bench, err)
	require.Equal(t, ConfigProfile{Name: "bench", DSN: "postgres://bench@db.invalid:5432/nova"}, bench,
		"LoadConfigProfile = %+v %v; want the two-column row", bench, err)
}

// TestLoadConfigProfileMissingFileWrapsErrNoProfileRow: no file is
// ErrNoProfileRow, naming the path and the columns.
func TestLoadConfigProfileMissingFileWrapsErrNoProfileRow(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "seats.tsv")
	_, err := LoadConfigProfile(path, "coordinator")
	require.ErrorIs(t, err, ErrNoProfileRow, "LoadConfigProfile of a missing file = %v; want ErrNoProfileRow naming %s", err, path)
	require.Contains(t, err.Error(), path, "LoadConfigProfile of a missing file = %v; want %s", err, path)
	require.Contains(t, err.Error(), ConfigProfileColumns, "LoadConfigProfile of a missing file = %v; want the columns", err)
}

// TestLoadConfigProfileEmptyFileWrapsErrNoProfileRow: an existing file with no
// row is ErrNoProfileRow too.
func TestLoadConfigProfileEmptyFileWrapsErrNoProfileRow(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "seats.tsv")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	_, err := LoadConfigProfile(path, "coordinator")
	require.ErrorIs(t, err, ErrNoProfileRow, "LoadConfigProfile of an empty file = %v; want ErrNoProfileRow naming %s", err, path)
	require.Contains(t, err.Error(), path, "LoadConfigProfile of an empty file = %v; want %s", err, path)
}

// TestLoadConfigProfileRefusesMalformedRows: one and four columns, a bad
// password env, a bad seat name and an empty DSN are refused, each naming the
// file and the physical line number.
func TestLoadConfigProfileRefusesMalformedRows(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ name, row, why string }{
		{name: "one column", row: "coordinator", why: "columns"},
		{name: "four columns", row: "a\tb\tc\td", why: "columns"},
		{name: "bad password env", row: "studio\tpostgres://u@h:1/db\tlower", why: "password env"},
		{name: "bad seat name", row: "bad/name\tpostgres://u@h:1/db", why: "name"},
		{name: "empty dsn", row: "studio\t", why: "dsn"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "seats.tsv")
			require.NoError(t, os.WriteFile(path, []byte("# x\n"+c.row+"\n"), 0o600))
			_, err := LoadConfigProfile(path, "anything")
			require.Error(t, err, "row %q = %v; want a refusal naming %s:2 and %q", c.row, err, path, c.why)
			require.NotErrorIs(t, err, ErrNoProfileRow, "row %q = %v; want a malformed-row refusal", c.row, err)
			require.Contains(t, err.Error(), path+":2", "row %q = %v; want the line number %s:2", c.row, err, path)
			require.Contains(t, err.Error(), c.why, "row %q = %v; want %q", c.row, err, c.why)
		})
	}
}

// TestLoadConfigProfileRefusesASecondRowForASeat: two rows for the seat are
// refused, naming the second line.
func TestLoadConfigProfileRefusesASecondRowForASeat(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "seats.tsv")
	body := "studio\tpostgres://u@h:1/db\nstudio\tpostgres://u@h:2/db\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	_, err := LoadConfigProfile(path, "studio")
	require.Error(t, err, "a second row for a seat = %v; want a refusal naming %s:2", err, path)
	require.Contains(t, err.Error(), path+":2", "a second row for a seat = %v; want %s:2", err, path)
	require.Contains(t, err.Error(), "second row", "a second row for a seat = %v; want it named", err)
}

// TestLoadConfigProfileNamesTheKnownSeatsForAnUnknownSeat: a seat the file
// does not hold is refused, naming every seat it does hold.
func TestLoadConfigProfileNamesTheKnownSeatsForAnUnknownSeat(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "seats.tsv")
	body := "alpha\tpostgres://u@h:1/a\nbeta\tpostgres://u@h:1/b\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	_, err := LoadConfigProfile(path, "ghost")
	require.Error(t, err, "an unknown seat = %v; want a refusal naming the known seats", err)
	require.Contains(t, err.Error(), "ghost", "an unknown seat = %v; want the seat named", err)
	require.Contains(t, err.Error(), "alpha", "an unknown seat = %v; want the known seats named", err)
	require.Contains(t, err.Error(), "beta", "an unknown seat = %v; want the known seats named", err)
}

// TestParseConfigProfileRowMatchesTheFormat pins the row grammar directly: two
// and three columns parse, one and four are refused, and the field checks hold.
func TestParseConfigProfileRowMatchesTheFormat(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		row     string
		want    ConfigProfile
		wantErr string
	}{
		{name: "two columns", row: "studio\tpostgres://u@h:1/db", want: ConfigProfile{Name: "studio", DSN: "postgres://u@h:1/db"}},
		{name: "three columns", row: "studio\tpostgres://u@h:1/db\tNOVA_PG_PASSWORD", want: ConfigProfile{Name: "studio", DSN: "postgres://u@h:1/db", PasswordEnv: "NOVA_PG_PASSWORD"}},
		{name: "spaces are trimmed", row: " studio \t postgres://u@h:1/db \t NOVA_PG_PASSWORD ", want: ConfigProfile{Name: "studio", DSN: "postgres://u@h:1/db", PasswordEnv: "NOVA_PG_PASSWORD"}},
		{name: "one column", row: "studio", wantErr: "columns"},
		{name: "four columns", row: "a\tb\tc\td", wantErr: "columns"},
		{name: "bad password env", row: "studio\tpostgres://u@h:1/db\tlower", wantErr: "password env"},
		{name: "bad seat name", row: "studio air\tpostgres://u@h:1/db", wantErr: "name"},
		{name: "empty dsn", row: "studio\t", wantErr: "dsn"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseConfigProfileRow(c.row)
			if c.wantErr != "" {
				require.Error(t, err, "parseConfigProfileRow(%q) = %+v %v; want %q", c.row, got, err, c.wantErr)
				assert.Contains(t, err.Error(), c.wantErr, "parseConfigProfileRow(%q) = %v; want %q", c.row, err, c.wantErr)
				return
			}
			require.NoError(t, err, "parseConfigProfileRow(%q) = %+v %v; want %+v", c.row, got, err, c.want)
			assert.Equal(t, c.want, got, "parseConfigProfileRow(%q) = %+v %v; want %+v", c.row, got, err, c.want)
		})
	}
}
