package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type hygieneDiskFake struct {
	freeGB int
	sizeGB int
	free   string
}

func (d hygieneDiskFake) FreeGB() int       { return d.freeGB }
func (d hygieneDiskFake) Free() string      { return d.free }
func (d hygieneDiskFake) SizeGB(string) int { return d.sizeGB }

func hygieneWriteAt(t *testing.T, path, body string, when time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !when.IsZero() {
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
}

func hygieneExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return err == nil
}

func hygieneRun(t *testing.T, in HygieneInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	if in.Disk == nil {
		in.Disk = hygieneDiskFake{freeGB: 40, free: "40G"}
	}
	return Hygiene(in), out.String(), errb.String()
}

// 1fbb5e20: reap used to Stat through a planted <job>/repo symlink and remove
// the foreign scratch because the bound was "under a hygiene root". The bound
// is "still inside this job".
func TestHygieneReapRefusesAJobRepoSymlinkToAnotherTree(t *testing.T) {
	now := time.Date(2026, 9, 19, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()
	root := filepath.Join(home, "rowan-swarm-root")

	certify := filepath.Join(root, "certify-tree")
	hygieneWriteAt(t, filepath.Join(certify, "scratch", "corpus"), "the corpus\n", time.Time{})

	victimJob := filepath.Join(root, "2", "jobs", "card-victim")
	hygieneWriteAt(t, filepath.Join(victimJob, "scratch", "work"), "the victim's work\n", time.Time{})

	evil := filepath.Join(root, "1", "jobs", "card-evil")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(certify, filepath.Join(evil, "repo")); err != nil {
		t.Skipf("this filesystem does not do symlinks: %v", err)
	}
	if err := os.Symlink(victimJob, filepath.Join(root, "1", "tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(evil, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, _, errb := hygieneRun(t, HygieneInput{
		Verb: "reap", Slot: "1", Home: home, Now: func() time.Time { return now },
	})
	if code != 0 {
		t.Fatalf("reap exit = %d, stderr=%s", code, errb)
	}
	for _, keep := range []string{
		filepath.Join(certify, "scratch"), filepath.Join(certify, "scratch", "corpus"),
		filepath.Join(victimJob, "scratch"), filepath.Join(victimJob, "scratch", "work"),
	} {
		if !hygieneExists(t, keep) {
			t.Errorf("reap deleted %s, which is not inside the job it was reaping", keep)
		}
	}
	if hygieneExists(t, filepath.Join(evil, "scratch")) {
		t.Errorf("reap left the job's own scratch")
	}
	if !strings.Contains(errb, "symlink") {
		t.Errorf("reap said nothing about the planted link; stderr=%q", errb)
	}
}

// 1af36d0e: a directory without <slot>/jobs is not a slot; a live lease keeps
// the go-build cache (cache=kept-lease); an unleased job quiet past MinAgeHours
// with no RESULT.md is counted as dead=.
func TestHygieneRunSkipsUnshapedTreesAndKeepsALeasedCache(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-8 * time.Hour)
	home := t.TempDir()
	root := filepath.Join(home, "rowan-swarm-root")

	bare := filepath.Join(root, "certify-tree")
	hygieneWriteAt(t, filepath.Join(bare, "corpus", "data.txt"), "the corpus\n", old)

	leased := filepath.Join(root, "slot-leased", "jobs", "job-quiet")
	hygieneWriteAt(t, filepath.Join(leased, "harness-output.log"), "a long model call\n", old)
	host, _ := os.Hostname()
	hygieneWriteAt(t, filepath.Join(leased, ".lease"),
		"pid="+strconv.Itoa(os.Getpid())+"\nhost="+host+"\nlabel=job-quiet\nstarted=2026-09-19T04:00:00Z\n", now)

	deadJob := filepath.Join(root, "slot-old", "jobs", "job-old")
	hygieneWriteAt(t, filepath.Join(deadJob, "harness-output.log"), "crashed\n", old)
	for _, p := range []string{deadJob, filepath.Join(root, "slot-old", "jobs"), filepath.Join(root, "slot-old")} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	cache := filepath.Join(home, ".cache", "go-build", "trim.txt.d", "x")
	hygieneWriteAt(t, cache, "x\n", time.Time{})

	if err := os.Chtimes(filepath.Join(root, "slot-leased"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bare, old, old); err != nil {
		t.Fatal(err)
	}

	code, out, errb := hygieneRun(t, HygieneInput{
		Verb: "run", Home: home, Hostname: "bench", Now: func() time.Time { return now },
		Disk: hygieneDiskFake{freeGB: 1, free: "1G", sizeGB: 0},
	})
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	if !hygieneExists(t, filepath.Join(bare, "corpus", "data.txt")) {
		t.Fatal("run deleted a directory that is not a slot")
	}
	if !hygieneExists(t, cache) {
		t.Fatal("run dropped the build cache while a lease was live")
	}
	if hygieneExists(t, deadJob) {
		t.Fatal("run left an unleased job quiet past MinAgeHours with no RESULT.md")
	}
	if !strings.Contains(out, "dead=1") {
		t.Errorf("HYGIENE line missing dead=1: %s", strings.TrimSpace(out))
	}
	if !strings.Contains(out, "cache=kept-lease") {
		t.Errorf("HYGIENE line missing cache=kept-lease: %s", strings.TrimSpace(out))
	}
}
