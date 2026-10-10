package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE OPENCODE READER'S UNIT COVER (opencode.go).
//
// ReadJobUsageLiveWithin, queryOpenCodeWaiting, queryOpenCode and oneLine had no unit test at
// all: a read's happy path is the one subprocess this package runs (`sqlite3`,
// opencode.go), so it belonged to the functional tier, and the absences and refusals went
// uncovered with it. These tests hold what is reachable without any process: the live
// sample's absence and refusal, both query wrappers' refusals, and the one-line rendering
// of a subprocess's complaint. The folded rows themselves are the functional tier's
// (opencode_functional_test.go); the waiting's clock arithmetic is held with the test's
// own clock in opencode_test.go, through the walWait seam.

func TestOpencodeCoverOneLineKeepsAComplaintOnOneLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "every separator becomes the one space a RUN line carries",
			in:   "database is locked\nerrno: 5\tdetail: busy",
			want: "database is locked errno: 5 detail: busy",
		},
		{
			name: "a carriage return is a separator too, not a second line",
			in:   "first\rsecond",
			want: "first second",
		},
		{
			name: "the padding around the complaint is trimmed away",
			in:   "  database is locked \t\n",
			want: "database is locked",
		},
		{
			name: "an empty stream stays empty",
			in:   "",
			want: "",
		},
		{
			name: "a stream of nothing but separators collapses to empty",
			in:   " \t\n\r ",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, oneLine(tc.in), "oneLine(%q)", tc.in)
		})
	}
}

func TestOpencodeCoverReadJobUsageLiveWithoutAStore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		plant   func(t *testing.T) string
		wantErr string
	}{
		{
			name: "a database that is not there is an absence, not an error",
			plant: func(t *testing.T) string {
				return t.TempDir()
			},
		},
		{
			name: "a store that is only a directory is no store: an absence",
			plant: func(t *testing.T) string {
				dataHome := t.TempDir()
				require.NoError(t, os.MkdirAll(filepath.Join(dataHome, filepath.FromSlash(OpenCodeDB)), 0o755))
				return dataHome
			},
		},
		{
			name: "a store that cannot be read is the named refusal",
			plant: func(t *testing.T) string {
				// A file where the database's directory belongs makes the store's own
				// stat fail: the refusal findOpenCodeStore owes the caller, reached
				// before any program is needed.
				dataHome := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dataHome, "opencode"), []byte("in the way"), 0o644))
				return dataHome
			},
			wantErr: "could not be read",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			usage, err := ReadJobUsageLiveWithin(tc.plant(t), LiveSampleLimit)
			if tc.wantErr != "" {
				require.Error(t, err, "a store that cannot be read is a refusal, not an absence")
				assert.ErrorContains(t, err, tc.wantErr, "the refusal names the unreadable store: %v", err)
				return
			}
			require.NoError(t, err, "the live sample is not an error: %v", err)
			assert.False(t, usage.Observed, "nothing was observed: %+v", usage)
			assert.Equal(t, 0, usage.Turns, "no rows were folded: %+v", usage)
			assert.Equal(t, map[string]string{}, usage.Values, "an absence reports no values: %+v", usage)
		})
	}
}

func TestOpencodeCoverQueryOpenCodeRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		limit   time.Duration
		suffix  string
		wantErr string
	}{
		{
			name: "a limit that is already spent is refused before the program runs",
			// The branch a hung `sqlite3` takes at its real limit, taken here with no
			// time at all, deterministically and without any process.
			limit:   0,
			suffix:  "",
			wantErr: "did not answer within",
		},
		{
			name: "an argument the program can never receive is a refusal",
			// A name the OS cannot hand out as an exec argument fails the start before
			// any program runs.
			limit:   usageTimeout,
			suffix:  "\x00",
			wantErr: "could not be read",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := writeDB(t, t.TempDir(), "deepseek\tdeepseek-chat\t100\t50\t\t\t\n")
			rows, err := queryOpenCode(SQLiteBinary, db+tc.suffix, tc.limit)
			require.Error(t, err, "the query is refused: %v", err)
			assert.Nil(t, rows, "a refusal carries no rows: %v", rows)
			assert.ErrorContains(t, err, tc.wantErr, "the refusal says which: %v", err)
		})
	}
}

func TestOpencodeCoverQueryOpenCodeWaitingRefusesWithoutWaiting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		path    func(t *testing.T) string
		wantErr string
	}{
		{
			name: "a refusal with no write-ahead log beside it ends the read at once",
			// The path's last byte cannot survive the exec, so the first attempt is
			// refused before any program runs, and no -wal can sit beside a name the
			// OS never hands out: the read waits for nothing and returns the refusal.
			path: func(t *testing.T) string {
				db := writeDB(t, t.TempDir(), "deepseek\tdeepseek-chat\t100\t50\t\t\t\n")
				return db + "\x00"
			},
			wantErr: "could not be read",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows, err := queryOpenCodeWaiting(SQLiteBinary, tc.path(t))
			require.Error(t, err, "the read is refused: %v", err)
			assert.Nil(t, rows, "a refusal carries no rows: %v", rows)
			assert.ErrorContains(t, err, tc.wantErr, "the refusal says which: %v", err)
		})
	}
}
