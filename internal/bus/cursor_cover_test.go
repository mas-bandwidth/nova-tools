package bus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OpenPresent, CursorPresent and laneFilePresent each answer a question about a FILE and
// not about what it holds: a zero-length OPEN is present, a CURSOR this tool cannot parse
// is present, and a directory is present on disk and is still not a lane file. The cases
// below pin the present answer and the refusal for each, so the first-advance guard and
// the OPEN-count check rest on a tested predicate rather than on os.Stat read by hand.

func TestCursorCoverOpenPresent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{
			name:  "a lane with an OPEN file",
			files: map[string]string{"from-ada/OPEN": OpenHeader + "\n"},
			want:  true,
		},
		{
			name:  "a zero-length OPEN file is still present",
			files: map[string]string{"from-ada/OPEN": ""},
			want:  true,
		},
		{
			name: "a lane with no OPEN file",
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeBus(t, tc.files)
			assert.Equal(t, tc.want, OpenPresent(root, "from-ada"), "OpenPresent(root, %q)", "from-ada")
		})
	}
}

func TestCursorCoverCursorPresent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{
			name:  "a lane with a CURSOR file",
			files: map[string]string{"from-ada/CURSOR": "0123456789abcdef0123456789abcdef01234567 2026-09-09T12:34:56Z open=0\n"},
			want:  true,
		},
		{
			name:  "a CURSOR this tool cannot parse is present",
			files: map[string]string{"from-ada/CURSOR": "not a commit at all\n"},
			want:  true,
		},
		{
			name: "a lane with no CURSOR file",
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeBus(t, tc.files)
			assert.Equal(t, tc.want, CursorPresent(root, "from-ada"), "CursorPresent(root, %q)", "from-ada")
		})
	}
}

func TestCursorCoverLaneFilePresent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		path  string
		setup func(t *testing.T, root string)
		want  bool
	}{
		{
			name: "a regular file is present",
			path: "from-ada/OPEN",
			setup: func(t *testing.T, root string) {
				write(t, root, "from-ada/OPEN", OpenHeader+"\n")
			},
			want: true,
		},
		{
			name: "an absent path is not present",
			path: "from-ada/OPEN",
			want: false,
		},
		{
			name: "a directory is present on disk and is not a lane file",
			path: "from-ada/OPEN",
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash("from-ada/OPEN")), 0o755))
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeBus(t, nil)
			if tc.setup != nil {
				tc.setup(t, root)
			}
			assert.Equal(t, tc.want, laneFilePresent(root, tc.path), "laneFilePresent(root, %q)", tc.path)
		})
	}
}
