package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
	"github.com/stretchr/testify/require"
)

func TestTlacheckBenchCoverRealBench(t *testing.T) {
	t.Parallel()
	d := realBench()
	require.NotNil(t, d.dial)
	require.NotNil(t, d.machines)
	require.NotNil(t, d.build)
	require.NotNil(t, d.sleep)
	r := d.dial("m")
	require.IsType(t, sshRemote{}, r)
}

func TestTlacheckBenchCoverProbeLine(t *testing.T) {
	t.Parallel()
	path := "/opt/tla/tla2tools.jar"
	line := probeLine(path)
	require.Contains(t, line, "nproc")
	require.Contains(t, line, "/proc/loadavg")
	require.Contains(t, line, "sha256sum")
}

func TestTlacheckBenchCoverFetchLine(t *testing.T) {
	t.Parallel()
	dir := "/tmp/mydir"
	line := fetchLine(dir)
	require.Contains(t, line, tlc.RunsFile)
	require.Contains(t, line, "*.log")
	require.Contains(t, line, "cd ")
}

func TestTlacheckBenchCoverSSHRemoteCommand(t *testing.T) {
	t.Parallel()
	r := sshRemote{machine: "m"}
	ctx := context.Background()
	cmd := r.command(ctx, "echo hi")
	require.Contains(t, cmd.Args[0], "ssh")
	require.Contains(t, cmd.Args, "-o")
	require.Contains(t, cmd.Args, "BatchMode=yes")
	require.Contains(t, cmd.Args, "ConnectTimeout=8")
	require.Contains(t, cmd.Args, "m")
	require.Equal(t, 5*time.Second, cmd.WaitDelay)
	require.Nil(t, cmd.ProcessState)
}

func TestTlacheckBenchCoverSSHRemoteRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		dir  string
	}{
		{"root", "/"},
		{"home", "/home/u"},
		{"dotdot", ".."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := sshRemote{machine: "m"}
			err := r.Remove(context.Background(), tt.dir)
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), "is not a directory run --bench staged"))
		})
	}
}

func TestTlacheckBenchCoverParseProbe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  string
		err  bool
	}{
		{"few_lines", "a b\nc", true},
		{"one_word_uname", "uname\nc\n0.0", true},
		{"nproc_zero", "a b\n0\n0.0", true},
		{"nproc_x", "a b\nx\n0.0", true},
		{"bad_loadavg", "a b\n2\nx", true},
		{"unknown_arch", "a b\n2\n0.5", false},
		{"extra_not_sha", "a b\n2\n0.5\nfoo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseProbe(tt.out)
			if tt.err {
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
		name string
		line string
		err  bool
	}{
		{"empty", "", true},
		{"negative", "-1.0", true},
		{"word", "hello", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseLoad(tt.line)
			if tt.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTlacheckBenchCoverStageArchive(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "tla"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "tla", "a.tla"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "tla", "b.tla"), []byte("b"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "tla", "sub"), 0o755))
	bin := filepath.Join(tmp, "bin", "tlacheck")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("bin"), 0o755))
	_, _, err := stageArchive(tmp, bin)
	require.NoError(t, err)
	_, _, err = stageArchive(filepath.Join(tmp, "nosub"), bin)
	require.Error(t, err)
	_, _, err = stageArchive(tmp, filepath.Join(tmp, "noinstall"))
	require.Error(t, err)
}

type fakeRemote struct {
	fetchErr error
	fetchTar []byte
}

func (f *fakeRemote) Probe(ctx context.Context, jar string) (benchProbe, error) {
	return benchProbe{}, nil
}

func (f *fakeRemote) Load(ctx context.Context) (float64, error) {
	return 0, nil
}

func (f *fakeRemote) Stage(ctx context.Context, archive io.Reader) (string, error) {
	return "", nil
}

func (f *fakeRemote) Run(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) (int, error) {
	return 0, nil
}

func (f *fakeRemote) Fetch(ctx context.Context, dir string) ([]byte, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.fetchTar, nil
}

func (f *fakeRemote) Remove(ctx context.Context, dir string) error {
	return nil
}

func TestTlacheckBenchCoverFetchRun(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	local := filepath.Join(tmp, "local")
	fake := &fakeRemote{fetchErr: errors.New("fetch error")}
	err := fetchRun(context.Background(), fake, "/remote", local)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetch error")
	b := &bytes.Buffer{}
	w := tar.NewWriter(b)
	require.NoError(t, w.WriteHeader(&tar.Header{Name: tlc.RunsFile, Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}))
	_, _ = w.Write([]byte{1})
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "test.log", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}))
	_, _ = w.Write([]byte{2})
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "dir", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "a/b", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, w.WriteHeader(&tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, w.WriteHeader(&tar.Header{Name: "..", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, w.Close())
	fake2 := &fakeRemote{fetchTar: b.Bytes()}
	require.NoError(t, fetchRun(context.Background(), fake2, "/remote", local))
	files, _ := os.ReadDir(local)
	require.Len(t, files, 2)
}
