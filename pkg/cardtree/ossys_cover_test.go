package cardtree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// absentProgram is a name no machine resolves: osExec refuses it at the start, before a
// child exists, so OSSys's seams are reachable in the unit tier with no subprocess.
const absentProgram = "cardtree-ossys-cover-absent"

// theEnvTail is what buildEnv appends after ScrubEnv, in order (ossys.go, buildEnv): the
// toolchain builds with no credential, no cgo, no module fetched, no inherited GOFLAGS.
func theEnvTail() []string {
	return []string{"HOME=" + os.Getenv("HOME"), "CGO_ENABLED=0", "GOFLAGS=", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local"}
}

func TestOssysCoverLastLineKeepsTheCommandsOwnLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, want string
	}{
		{"the last line of the output", "first\nsecond\n", "second"},
		{"skips the wall's receipt", "go vet: b.go:2:1: boom\nSANDBOX wrote 1 file\n", "go vet: b.go:2:1: boom"},
		{"trims the padding and the blanks", "  padded  \n\n", "padded"},
		{"nothing but receipts", "SANDBOX one\nSANDBOX two\n", ""},
		{"no output at all", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, lastLine(tc.in))
		})
	}
}

func TestOssysCoverBuildEnvStripsCredentialsAndOverridesTheToolchain(t *testing.T) {
	t.Parallel()
	tail := theEnvTail()
	for _, tc := range []struct {
		name      string
		in        []string
		kept      []string
		overrides bool
	}{
		{"carries the variables that hold no credential",
			[]string{"PATH=/bin", "GIT_AUTHOR_NAME=n"},
			[]string{"PATH=/bin", "GIT_AUTHOR_NAME=n"},
			false},
		{"refuses a credential by name, a URL's password and the HOME the step's wall resets",
			[]string{"PATH=/bin", "API_TOKEN=k", "RELAY=redis://:pw@db.invalid:6379", "HOME=/u"},
			[]string{"PATH=/bin"},
			false},
		{"keeps a caller's GOFLAGS in the list, where the appended empty one wins for a child",
			[]string{"GOFLAGS=-toolexec=/x"},
			[]string{"GOFLAGS=-toolexec=/x"},
			true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildEnv(tc.in)
			assert.Equal(t, append(append([]string{}, tc.kept...), tail...), got)
			if tc.overrides {
				assert.Equal(t, "GOFLAGS=", got[len(got)-4], "no inherited GOFLAGS: a -toolexec there would run anything")
			}
		})
	}
}

func TestOssysCoverOSExecRefusesWithNoChildStarted(t *testing.T) {
	t.Parallel()
	empty := t.TempDir()
	for _, tc := range []struct {
		name       string
		env, argv  []string
		wantErrSub string
	}{
		{"an empty argv names no command", nil, nil, "no command"},
		{"a program off the PATH does not start", []string{"PATH=" + empty}, []string{absentProgram}, "executable file not found in $PATH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, code, err := osExec(empty, tc.env, tc.argv)
			assert.Empty(t, out)
			assert.Equal(t, -1, code)
			assert.ErrorContains(t, err, tc.wantErrSub)
		})
	}
}

func TestOssysCoverOSSysSeamsRefuseWithoutAChild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		wall func(tmp string) Wall
		call func(t *testing.T, sys Sys, tmp string)
	}{
		{"Build runs the toolchain outside the wall, and refuses a program off the PATH",
			func(tmp string) Wall { return Wall{Tmp: tmp} },
			func(t *testing.T, sys Sys, tmp string) {
				assert.ErrorContains(t, sys.Build(tmp, absentProgram), "executable file not found in $PATH")
			}},
		{"Run in the wall refuses at the wall binary that is not there",
			func(tmp string) Wall {
				return Wall{Bin: filepath.Join(tmp, "absent-wall"), Tmp: tmp, Read: []string{"--read", tmp}}
			},
			func(t *testing.T, sys Sys, tmp string) {
				assert.ErrorContains(t, sys.Run(tmp, "any-tool"), "no such file or directory")
				assert.DirExists(t, filepath.Join(tmp, "home"), "the wall's HOME is made before the child")
			}},
		{"Run with no wall runs native, and refuses at the program itself",
			func(tmp string) Wall { return Wall{Tmp: tmp} },
			func(t *testing.T, sys Sys, tmp string) {
				assert.ErrorContains(t, sys.Run(tmp, absentProgram), "executable file not found in $PATH")
			}},
		{"Commit refuses before git ever runs, and commits nothing",
			func(tmp string) Wall { return Wall{Bin: filepath.Join(tmp, "absent-wall"), Tmp: tmp} },
			func(t *testing.T, sys Sys, tmp string) {
				sha, err := sys.Commit(tmp, []string{"a.go"}, "step: one")
				assert.ErrorContains(t, err, "no such file or directory")
				assert.Empty(t, sha)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			sys := OSSys(tmp, tc.wall(tmp))
			require.Equal(t, tmp, sys.Work, "Work is where the programs are written and built")
			tc.call(t, sys, tmp)
		})
	}
}

func TestOssysCoverOSSysRefusesWhenTheWallTempIsNotADirectory(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "plain-file")
	require.NoError(t, os.WriteFile(file, []byte("not a directory\n"), 0o644))
	sys := OSSys(t.TempDir(), Wall{Tmp: file})
	err := sys.Run(t.TempDir(), absentProgram)
	assert.ErrorContains(t, err, "mkdir "+file+": not a directory", "the wall's HOME cannot be made in a file")
}
