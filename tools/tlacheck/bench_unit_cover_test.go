package main

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
	"github.com/stretchr/testify/require"
)

type fakeRemote struct {
	err  error
	data []byte
}

func (f fakeRemote) Probe(ctx context.Context, jar string) (benchProbe, error) {
	return benchProbe{}, nil
}
func (f fakeRemote) Load(ctx context.Context) (float64, error)                    { return 0, nil }
func (f fakeRemote) Stage(ctx context.Context, archive io.Reader) (string, error) { return "", nil }
func (f fakeRemote) Run(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) (int, error) {
	return 0, nil
}
func (f fakeRemote) Fetch(ctx context.Context, dir string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.data, nil
}
func (f fakeRemote) Remove(ctx context.Context, dir string) error { return nil }

func TestTlacheckBenchCoverRealBench(t *testing.T) {
	t.Parallel()
	deps := realBench()
	require.NotNil(t, deps.dial)
	require.NotNil(t, deps.machines)
	require.NotNil(t, deps.build)
	require.NotNil(t, deps.sleep)

	rem := deps.dial("testmachine")
	ssh, ok := rem.(sshRemote)
	require.True(t, ok)
	require.Equal(t, "testmachine", ssh.machine)
}

func TestTlacheckBenchCoverProbeLine(t *testing.T) {
	t.Parallel()
	jar := "/path/to/tla2tools.jar"
	line := probeLine(jar)
	require.Contains(t, line, "uname -s -m")
	require.Contains(t, line, "nproc")
	require.Contains(t, line, "/proc/loadavg")
	require.Contains(t, line, "sha256sum")
	require.Contains(t, line, "'/path/to/tla2tools.jar'")
}

func TestTlacheckBenchCoverFetchLine(t *testing.T) {
	t.Parallel()
	dir := "/a/b/c"
	line := fetchLine(dir)
	require.Contains(t, line, "find . -maxdepth 1 -type f")
	require.Contains(t, line, tlc.RunsFile)
	require.Contains(t, line, "*.log")
	require.Contains(t, line, "tar -c -f - --null -T -")
}

func TestTlacheckBenchCoverSSHRemoteCommand(t *testing.T) {
	t.Parallel()
	ssh := sshRemote{machine: "m"}
	ctx := context.Background()
	cmd := ssh.command(ctx, "ls -la")
	require.Len(t, cmd.Args, 7)
	require.Equal(t, "ssh", cmd.Args[0])
	require.Equal(t, "-o", cmd.Args[1])
	require.Equal(t, "BatchMode=yes", cmd.Args[2])
	require.Equal(t, "-o", cmd.Args[3])
	require.Equal(t, "ConnectTimeout=8", cmd.Args[4])
	require.Equal(t, "m", cmd.Args[5])
	require.Equal(t, "ls -la", cmd.Args[6])
	require.Equal(t, int64(5*1e9), cmd.WaitDelay.Nanoseconds())
}

func TestTlacheckBenchCoverSSHRemoteRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"/", "/", true},
		{"/home/u", "/home/u", true},
		{"..", "..", true},
		{"../foo", "../foo", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := removeLine(tc.dir)
			require.Error(t, err)
		})
	}
}

func TestTlacheckBenchCoverParseProbe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		out     string
		wantErr bool
	}{
		{
			name:    "fewer than three lines",
			out:     "Linux\n2",
			wantErr: true,
		},
		{
			name:    "one-word uname",
			out:     "Linux\n2\n0.5",
			wantErr: true,
		},
		{
			name:    "nproc zero",
			out:     "Linux x86_64\n0\n0.5",
			wantErr: true,
		},
		{
			name:    "nproc x",
			out:     "Linux x86_64\nx\n0.5",
			wantErr: true,
		},
		{
			name:    "bad loadavg",
			out:     "Linux x86_64\n2\nabc",
			wantErr: true,
		},
		{
			name: "unknown arch passthrough",
			out:  "Linux unknownarch\n2\n0.5",
		},
		{
			name: "ignore fourth line sha256",
			out:  "Linux x86_64\n2\n0.5\nnotashasum",
		},
		{
			name: "parse sha256",
			out:  "Linux x86_64\n2\n0.5\nabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseProbe(tc.out)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTlacheckBenchCoverParseLoad(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{"empty", "", true},
		{"negative", "-0.5", true},
		{"word", "abc", true},
		{"ok", "0.5", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseLoad(tc.line)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTlacheckBenchCoverStageArchive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		root    string
		bin     string
		wantErr bool
	}{
		{
			name:    "no tla/ directory",
			root:    t.TempDir(),
			bin:     "/fake/bin",
			wantErr: true,
		},
		{
			name: "missing bin",
			root: func() string {
				tmp := t.TempDir()
				os.MkdirAll(filepath.Join(tmp, "tla"), 0o755)
				return tmp
			}(),
			bin:     "/nonexistent/bin",
			wantErr: true,
		},
		{
			name: "two files one subdir",
			root: func() string {
				tmp := t.TempDir()
				os.MkdirAll(filepath.Join(tmp, "tla"), 0o755)
				os.MkdirAll(filepath.Join(tmp, "tla", "sub"), 0o755)
				os.WriteFile(filepath.Join(tmp, "tla", "a.tla"), []byte("a"), 0o644)
				os.WriteFile(filepath.Join(tmp, "tla", "b.tla"), []byte("b"), 0o644)
				return tmp
			}(),
			bin: func() string {
				tmp := t.TempDir()
				os.MkdirAll(filepath.Join(tmp, "bin"), 0o755)
				os.WriteFile(filepath.Join(tmp, "bin", "tlacheck"), []byte("bin"), 0o755)
				return filepath.Join(tmp, "bin", "tlacheck")
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, files, err := stageArchive(tc.root, tc.bin)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, 3, files)
			}
		})
	}
}

func TestTlacheckBenchCoverFetchRun(t *testing.T) {
	t.Parallel()
	fakeFetchError := fakeRemote{err: os.ErrNotExist}
	fakeTar := fakeRemote{}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: tlc.RunsFile, Typeflag: tar.TypeReg, Size: 3})
	tw.Write([]byte("RUN"))
	tw.WriteHeader(&tar.Header{Name: "test.log", Typeflag: tar.TypeReg, Size: 3})
	tw.Write([]byte("log"))
	tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir})
	tw.WriteHeader(&tar.Header{Name: "a/b", Typeflag: tar.TypeReg, Size: 2})
	tw.Write([]byte("ab"))
	tw.WriteHeader(&tar.Header{Name: ".", Typeflag: tar.TypeDir})
	tw.WriteHeader(&tar.Header{Name: "..", Typeflag: tar.TypeDir})
	tw.Close()
	fakeTar.data = buf.Bytes()
	localDir := t.TempDir()
	ctx := context.Background()
	err := fetchRun(ctx, fakeFetchError, "/dir", localDir)
	require.Error(t, err)
	localDir2 := t.TempDir()
	err = fetchRun(ctx, fakeTar, "/dir", localDir2)
	require.NoError(t, err)
	ents, _ := os.ReadDir(localDir2)
	require.Len(t, ents, 2)
}
