//go:build darwin || linux

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handoff_test.go is the red-test contract of the handoff, docs/SPEC-SANDBOX.md
// "The handoff: what leaves the disposable place". The copy reads the volume
// through openat and fstat, which only the darwin and linux builds carry; the
// flag checks that hold on every platform are in handoff_flags_test.go. The copy
// is tested over two ordinary directories, and the ORDER, which is the part that
// cannot be got wrong twice, through runDisposable with a fake whose Delete
// really removes the mount: an artifact copied after the delete arrives from
// nothing, and --out would be empty.

// handoffDirs is a work directory standing in for the volume's work/ and an out
// directory standing in for --out.
func handoffDirs(t *testing.T) (work, out string) {
	t.Helper()
	work, out = t.TempDir(), t.TempDir()
	if r, err := filepath.EvalSymlinks(work); err == nil {
		work = r
	}
	return work, out
}

// copyOut over a work directory holding files, in rows. A row with refused set is a
// refusal that names each of refused; otherwise it counts files and bytes, on its OUT line
// too. arrive and absent are paths under <out>/card1 ("" is that directory itself).
func TestHandoffCopiesOnlyWhatItMayAndRefusesTheRest(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name           string
		files          map[string]string
		in             handoffInput
		refused        []string
		nfiles         int
		nbytes         int64
		arrive, absent []string
	}{
		// The default set is the card's receipt, its token row and its bundle, each
		// taken IF PRESENT: a read card writes no bundle, and that is not a failure.
		{name: "takes the defaults that are present",
			files:  map[string]string{"RESULT.md": "RESULT card1 sha=abc\nDONE\n", "usage.tsv": "in\tout\n10\t20\n", "notes.txt": "not an artifact"},
			nfiles: 2, nbytes: 26 + 13,
			arrive: []string{"RESULT.md", "usage.tsv"}, absent: []string{"notes.txt"}},
		// An artifact the CALLER named and did not write is a refusal, not a silent skip
		// (rule 5, as --read refuses a named path that is not there), and nothing is
		// written unless the whole set resolves.
		{name: "refuses a named artifact the card never wrote",
			files:   map[string]string{"RESULT.md": "x\n"},
			in:      handoffInput{Artifacts: []string{"RESULT.md", "report.json"}, Named: true},
			refused: []string{"report.json", "--artifact"}, absent: []string{""}},
		// The set is measured BEFORE a byte is written and refused over the cap: a
		// handoff is a door, not a backup, and a truncated artifact is worse than none.
		{name: "refuses over the byte cap and writes nothing",
			files: map[string]string{"RESULT.md": strings.Repeat("x", 4096)}, in: handoffInput{MaxBytes: 1024},
			refused: []string{"--out-max-bytes"}, absent: []string{"RESULT.md"}},
		// The default ceiling is 64 MiB and the same set goes under it.
		{name: "the same set goes under the default cap",
			files: map[string]string{"RESULT.md": strings.Repeat("x", 4096)}, nfiles: 1, nbytes: 4096},
		// A directory named as an artifact is taken whole, one row per regular file,
		// with its shape kept under <out>/<name>/.
		{name: "takes a directory whole",
			files:  map[string]string{"art/one.txt": "1", "art/deep/two.txt": "22"},
			in:     handoffInput{Artifacts: []string{"art"}, Named: true},
			nfiles: 2, nbytes: 3, arrive: []string{"art/deep/two.txt"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			work, out := handoffDirs(t)
			testkit.Tree(t, work, c.files)
			c.in.Work, c.in.Out, c.in.Name = work, out, "card1"
			res, err := copyOut(c.in)
			if c.refused != nil {
				require.Error(t, err)
				for _, want := range c.refused {
					assert.Contains(t, err.Error(), want)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, c.nfiles, res.Files)
				assert.Equal(t, c.nbytes, res.Bytes)
				assert.Equal(t, fmt.Sprintf("SANDBOX OUT name=card1 files=%d bytes=%d", c.nfiles, c.nbytes), res.Line())
			}
			for _, rel := range c.arrive {
				assert.NoError(t, statErr(filepath.Join(out, "card1", rel)), "%s did not arrive", rel)
			}
			for _, rel := range c.absent {
				assert.Error(t, statErr(filepath.Join(out, "card1", rel)), "%q is in --out; only named artifacts of a set that resolves leave", rel)
			}
		})
	}
}

// Nothing escapes the volume. An absolute path, a `..` and a symlink pointing off the
// volume are each refused by shape, before any copy.
func TestHandoffRefusesAnythingThatWouldReachOffTheVolume(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	testkit.WriteFile(t, filepath.Join(work, "RESULT.md"), "x\n")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	testkit.WriteFile(t, outside, "not yours", 0o600)
	if err := os.Symlink(outside, filepath.Join(work, "link.txt")); err != nil {
		t.Skipf("this filesystem has no symlinks: %v", err)
	}
	for _, rel := range []string{"/etc/passwd", "../secret.txt", "a/../../b", "link.txt", "", "."} {
		_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", Artifacts: []string{rel}, Named: true})
		assert.Error(t, err, "--artifact %q was not refused", rel)
	}
}

// deletingVolumes is the disk seam with a Delete that REALLY removes the mount,
// so the order of the handoff and the delete is proved rather than asserted.
type deletingVolumes struct {
	mount string
	calls []string
}

func (d *deletingVolumes) Container() (string, error)  { return "disk3", nil }
func (d *deletingVolumes) Exists(string) (bool, error) { return false, nil }
func (d *deletingVolumes) List() ([]diskVolume, error) { return nil, nil }
func (d *deletingVolumes) Create(_, name, _ string) (diskVolume, error) {
	d.calls = append(d.calls, "create")
	return diskVolume{Name: name, Disk: "disk3s9", Mount: d.mount}, nil
}
func (d *deletingVolumes) Used(string) (int64, error) { return 4096, nil }
func (d *deletingVolumes) Delete(string) error {
	d.calls = append(d.calls, "delete")
	return os.RemoveAll(d.mount)
}

// Through the whole verb, whose command writes the card's receipt and bundle on the
// volume (a real card's last steps) or writes nothing. The artifacts leave BEFORE the
// volume is deleted, the OUT line is printed before the DONE line, and the command's own
// status is what the verb returns. A handoff that fails after a command that SUCCEEDED
// turns the run into a refusal -- a zero exit would tell the caller the artifacts are in
// --out when they are not -- and the volume is still deleted.
func TestRunCopiesTheArtifactsOutBeforeTheVolumeIsDeleted(t *testing.T) {
	for _, c := range []struct {
		name   string
		writes bool
		extra  []string
		code   int
		stderr string
	}{
		{"the artifacts leave before the delete", true, nil, 0, "SANDBOX OUT name=card1 files=2 bytes=31"},
		{"a failed handoff after a clean command is a refusal", false, []string{"--artifact", "RESULT.md"}, sandbox.ExitRefused, "reason=out_failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			mount, out := handoffDirs(t)
			vols := &deletingVolumes{mount: mount}
			swap[volumeManager](t, &runVolumes, vols)
			swap(t, &runSignals, func() (<-chan os.Signal, func()) { return make(chan os.Signal), func() {} })
			swap(t, &runExec, func(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (startedRun, error) {
				if c.writes {
					testkit.WriteFile(t, filepath.Join(mount, "work", "RESULT.md"), "RESULT card1 sha=abc\nDONE\n")
					testkit.WriteFile(t, filepath.Join(mount, "work", "repo.bundle"), "PACK\n")
				}
				done := make(chan int, 1)
				done <- 0
				return startedRun{done: done, kill: func(syscall.Signal) {}, pid: 4242}, nil
			})
			args := append(append([]string{"--name", "card1", "--size", "64m", "--out", out}, c.extra...), "--")
			r := disposable(t, 0, append(args, shellOf(t)...)...).ExitErr(c.code, c.stderr)
			assert.Equal(t, "create delete", strings.Join(vols.calls, " "))
			if !c.writes {
				return
			}
			assert.LessOrEqual(t, strings.Index(r.Stderr, "SANDBOX OUT"), strings.Index(r.Stderr, "SANDBOX DONE"), "the copy must happen before the delete:\n%s", r.Stderr)
			// The proof: the volume is gone and the artifacts are not.
			assert.Error(t, statErr(filepath.Join(mount, "work", "RESULT.md")), "the volume survived the run; this test proves nothing")
			for _, name := range []string{"RESULT.md", "repo.bundle"} {
				assert.NoError(t, statErr(filepath.Join(out, "card1", name)), "%s did not leave the volume", name)
			}
		})
	}
}
