package control

import (
	"errors"
	"os"
	"testing"
	"time"
)

func restoreDurableHooks(t *testing.T) {
	t.Helper()
	ow, osf, oc, orn, osd := durableWrite, durableSyncFile, durableClose, durableRename, durableSyncDir
	t.Cleanup(func() {
		durableWrite, durableSyncFile, durableClose, durableRename, durableSyncDir = ow, osf, oc, orn, osd
	})
}

func seedRUN(t *testing.T, dir string) (*Handle, time.Time, []byte) {
	t.Helper()
	h, err := Open(dir, 30*time.Second)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	exp := now.Add(20 * time.Second)
	if _, err := h.Update(now, 0, State{Desired: DesiredRun, By: "seed", Expires: &exp}); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
	old, err := os.ReadFile(h.statePath())
	if err != nil {
		t.Fatal(err)
	}
	return h, now, old
}

func TestWriteDurableJSON_WriteFailureKeepsOldBytes(t *testing.T) {
	restoreDurableHooks(t)
	h, now, old := seedRUN(t, t.TempDir())
	durableWrite = func(*os.File, []byte) (int, error) {
		return 0, errors.New("injected write failure")
	}
	exp := now.Add(15 * time.Second)
	_, err := h.Update(now, 1, State{Desired: DesiredRun, By: "renew", Expires: &exp})
	if err == nil {
		t.Fatal("write failure must be returned")
	}
	if errors.Is(err, ErrAmbiguous) {
		t.Fatalf("pre-rename write failure is not ambiguous: %v", err)
	}
	got, err := os.ReadFile(h.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatalf("old bytes must remain before rename, got:\n%s", got)
	}
}

func TestWriteDurableJSON_FileSyncFailureKeepsOldBytes(t *testing.T) {
	restoreDurableHooks(t)
	h, now, old := seedRUN(t, t.TempDir())
	durableSyncFile = func(*os.File) error {
		return errors.New("injected file sync failure")
	}
	exp := now.Add(15 * time.Second)
	_, err := h.Update(now, 1, State{Desired: DesiredRun, By: "renew", Expires: &exp})
	if err == nil {
		t.Fatal("file-sync failure must be returned")
	}
	if errors.Is(err, ErrAmbiguous) {
		t.Fatalf("pre-rename file-sync failure is not ambiguous: %v", err)
	}
	got, err := os.ReadFile(h.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatalf("old bytes must remain before rename, got:\n%s", got)
	}
}

func TestWriteDurableJSON_RenameFailureBeforeLandingKeepsOldBytes(t *testing.T) {
	restoreDurableHooks(t)
	h, now, old := seedRUN(t, t.TempDir())
	durableRename = func(string, string) error {
		return errors.New("injected rename failure")
	}
	exp := now.Add(15 * time.Second)
	_, err := h.Update(now, 1, State{Desired: DesiredRun, By: "renew", Expires: &exp})
	if err == nil {
		t.Fatal("rename failure must be returned")
	}
	if errors.Is(err, ErrAmbiguous) {
		t.Fatalf("rename that did not land is not ambiguous: %v", err)
	}
	got, err := os.ReadFile(h.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatalf("old bytes must remain when rename does not land, got:\n%s", got)
	}
}

func TestWriteDurableJSON_RenameErrorAfterLandingIsAmbiguous(t *testing.T) {
	restoreDurableHooks(t)
	h, now, old := seedRUN(t, t.TempDir())
	durableRename = func(oldPath, newPath string) error {
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		return errors.New("injected rename error after landing")
	}
	exp := now.Add(15 * time.Second)
	_, err := h.Update(now, 1, State{Desired: DesiredRun, By: "renew", Expires: &exp})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("visible post-rename failure must be ambiguous, got %v", err)
	}
	got, err := os.ReadFile(h.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(old) {
		t.Fatal("new bytes must be visible after a landed rename")
	}
}

func TestWriteDurableJSON_DirectorySyncFailureIsAmbiguous(t *testing.T) {
	restoreDurableHooks(t)
	h, now, old := seedRUN(t, t.TempDir())
	durableSyncDir = func(string) error {
		return errors.New("injected directory sync failure")
	}
	exp := now.Add(15 * time.Second)
	_, err := h.Update(now, 1, State{Desired: DesiredRun, By: "renew", Expires: &exp})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("directory-sync failure after rename must be ambiguous, got %v", err)
	}
	got, err := os.ReadFile(h.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(old) {
		t.Fatal("new bytes must be visible after rename even when directory sync fails")
	}
}
