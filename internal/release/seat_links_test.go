package release

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seat's links (install.go, RepointSeatLinks, StaleSeatBinaries;
// adopt.go, SeatVersionWarning): the paths a seat reaches its nova binaries
// through are recorded by seat install, repointed by the install adopt runs on
// the seat, and any nova binary beside them that no link names is stale. The
// tests build the paths in a temp directory and write the binaries as files, so
// nothing here opens a socket, starts a server or touches the shared temp.

// TestAdoptRepointsARecordedSeatLink pins the whole of a repoint: a recorded
// link to an old pinned binary ends up naming the release's installed binary,
// and a link whose tool the release does not carry is refused by name.
func TestAdoptRepointsARecordedSeatLink(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nova-sprint"), []byte("the installed build"), 0o755))
	seat := t.TempDir()
	pinned := filepath.Join(seat, "nova-sprint.pinned")
	require.NoError(t, os.WriteFile(pinned, []byte("the pinned build"), 0o755))
	link := filepath.Join(seat, "nova-sprint")
	require.NoError(t, os.Symlink(pinned, link))

	r := SeatLinks{Paths: []string{seat}, Links: []SeatLink{{Tool: "nova-sprint", Path: link}}}
	repointed, refused := RepointSeatLinks(r, bin, "linux")
	require.Empty(t, refused, "a link the release carries is repointed, not refused")
	assert.Equal(t, []string{link}, repointed)

	got, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(bin, "nova-sprint"), got, "the wrapper's binary path names the adopted release")

	// the tool the release does not carry is refused, naming the link, and the
	// link is left as it was
	absent := filepath.Join(seat, "nova-bus")
	require.NoError(t, os.Symlink(pinned, absent))
	_, refused = RepointSeatLinks(SeatLinks{Links: []SeatLink{{Tool: "nova-bus", Path: absent}}}, bin, "linux")
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0], absent)
	assert.Contains(t, refused[0], "nova-bus")
	target, err := os.Readlink(absent)
	require.NoError(t, err)
	assert.Equal(t, pinned, target, "a refused link is not moved")
}

// TestAdoptNamesAnUnrecordedSeatBinaryStale pins the stale report: a nova binary
// on the seat's path that no recorded link names is named, while the recorded
// link and the installed directory's own tools are not.
func TestAdoptNamesAnUnrecordedSeatBinaryStale(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nova-sprint"), []byte("the installed build"), 0o755))
	seat := t.TempDir()
	link := filepath.Join(seat, "nova-sprint")
	require.NoError(t, os.Symlink(filepath.Join(bin, "nova-sprint"), link))
	pinned := filepath.Join(seat, "nova-sprint.pinned")
	require.NoError(t, os.WriteFile(pinned, []byte("the pinned build"), 0o755))

	r := SeatLinks{Paths: []string{seat, bin}, Links: []SeatLink{{Tool: "nova-sprint", Path: link}}}
	stale := StaleSeatBinaries(r, bin, "linux")
	assert.Equal(t, []string{pinned}, stale, "the recorded link and the installed directory are not stale; the unrecorded copy is")
}

// TestVersionFromAnOlderSeatBinaryWarnsOnSkew pins the one-directional warning:
// an older seat build warns, naming both versions and the remedy; a seat at or
// ahead of the server, or either side a version that cannot be read, is silent.
func TestVersionFromAnOlderSeatBinaryWarnsOnSkew(t *testing.T) {
	t.Parallel()

	warn := SeatVersionWarning("v1.2.0", "v1.3.0")
	require.NotEmpty(t, warn, "a seat behind its server warns")
	assert.Contains(t, warn, "v1.2.0", "the warning names the seat's build")
	assert.Contains(t, warn, "v1.3.0", "the warning names the server's build")
	assert.Contains(t, warn, "adopt", "the warning names the remedy")

	// the version out of a whole version line reads too
	assert.NotEmpty(t, SeatVersionWarning("nova-sprint v1.2.0 linux/amd64 go1.26.1", "nova-sprint v1.3.0 linux/amd64 go1.26.1"))

	for _, row := range []struct {
		name, self, server string
	}{
		{"a seat ahead of the server", "v1.3.0", "v1.2.0"},
		{"a seat at the server", "v1.2.0", "v1.2.0"},
		{"an unstamped seat", "", "v1.3.0"},
		{"a server with no version", "v1.2.0", ""},
		{"a server whose version is not one", "v1.2.0", "not-a-version"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, SeatVersionWarning(row.self, row.server))
		})
	}
}

// TestSeatLinksRecordRoundTrips pins the record on disk: what seat install wrote
// reads back field for field, and a record that is not there is no links at all,
// never an error.
func TestSeatLinksRecordRoundTrips(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), SeatLinksFile)
	want := SeatLinks{
		Paths:  []string{filepath.Join("a", "seat")},
		Links:  []SeatLink{{Tool: "nova-sprint", Path: filepath.Join("a", "seat", "nova-sprint")}},
		Server: "v1.2.3",
	}
	require.NoError(t, WriteSeatLinks(path, want))
	got, err := ReadSeatLinks(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	missing, err := ReadSeatLinks(filepath.Join(t.TempDir(), SeatLinksFile))
	require.NoError(t, err, "a seat with no record has no links, not an error")
	assert.Empty(t, missing.Links)
	assert.Empty(t, missing.Paths)
}
