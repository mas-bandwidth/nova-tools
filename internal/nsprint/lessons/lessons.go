// Package lessons appends and supersedes reviewed operational lessons in a
// repository's bounded docs/LESSONS.md active view (nova-tools#2498 S9).
package lessons

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	RelativePath        = "docs/LESSONS.md"
	ArchiveRelativePath = "docs/LESSONS-ARCHIVE.md"
	MaxLines            = 40
	lockWait            = 5 * time.Second
	lockStaleAfter      = 30 * time.Second
)

const archiveHeader = `# Superseded operational lessons

Rows moved from docs/LESSONS.md by nova-sprint lesson supersede. They remain evidence and are not loaded into every card.

| id | component | card kind | observed failure | preventive action | evidence | status | reviewed by |
| --- | --- | --- | --- | --- | --- | --- | --- |
`

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Lesson is one reviewed, evidence-backed operational lesson.
type Lesson struct {
	ID, Component, Kind, Failure, Prevention, Evidence, Status, ReviewedBy string
}

// Result describes whether Append added a row or found an identical retry.
type Result struct {
	Path     string
	Lines    int
	Appended bool
}

// SupersedeResult describes whether Supersede moved an active row or found an
// already completed retry.
type SupersedeResult struct {
	Path        string
	ArchivePath string
	Lines       int
	Moved       bool
}

// Append admits one lesson to repo/docs/LESSONS.md. The full read/check/write
// transaction is serialized, so successful callers cannot overwrite each other.
func Append(repo string, lesson Lesson) (Result, error) {
	if strings.TrimSpace(repo) == "" {
		return Result{}, errors.New("--repo <dir> is required")
	}
	if err := lesson.validate(); err != nil {
		return Result{}, err
	}
	if lesson.Status != "active" {
		return Result{}, errors.New("lesson append --status must be active; use lesson supersede to retire an active row")
	}
	path := filepath.Join(filepath.Clean(repo), filepath.FromSlash(RelativePath))
	var result Result
	err := withLock(path, func() error {
		r, err := appendLocked(path, lesson)
		result = r
		return err
	})
	return result, err
}

func appendLocked(path string, lesson Lesson) (Result, error) {
	raw, err := readTerminated(path)
	if err != nil {
		return Result{}, err
	}
	lines := lineCount(raw)
	if lines > MaxLines {
		return Result{}, fmt.Errorf("%s has %d lines; cap is %d", path, lines, MaxLines)
	}
	row := lesson.row()
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		text := string(line)
		if text == row {
			return Result{Path: path, Lines: lines}, nil
		}
		if rowHasID(text, lesson.ID) {
			return Result{}, fmt.Errorf("lesson id %q already exists with different content", lesson.ID)
		}
	}
	archivePath := filepath.Join(filepath.Dir(path), filepath.Base(ArchiveRelativePath))
	if archive, err := os.ReadFile(archivePath); err == nil {
		for _, line := range bytes.Split(archive, []byte{'\n'}) {
			if rowHasID(string(line), lesson.ID) {
				return Result{}, fmt.Errorf("lesson id %q is reserved by %s", lesson.ID, archivePath)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("read %s: %w", archivePath, err)
	}
	if lines+1 > MaxLines {
		return Result{}, fmt.Errorf("append would make %s %d lines; cap is %d; supersede an active lesson first", path, lines+1, MaxLines)
	}
	next := append(append([]byte(nil), raw...), row...)
	next = append(next, '\n')
	if err := atomicWrite(path, next); err != nil {
		return Result{}, err
	}
	return Result{Path: path, Lines: lines + 1, Appended: true}, nil
}

// Supersede moves one active lesson to docs/LESSONS-ARCHIVE.md with status
// superseded. The archive publishes first, so interruption may leave a duplicate
// but cannot erase evidence; retry removes the active copy and frees one line.
func Supersede(repo, id string) (SupersedeResult, error) {
	if strings.TrimSpace(repo) == "" {
		return SupersedeResult{}, errors.New("--repo <dir> is required")
	}
	if !idPattern.MatchString(id) {
		return SupersedeResult{}, errors.New("--id must match [a-z0-9][a-z0-9._-]*")
	}
	if id == "id" {
		return SupersedeResult{}, errors.New("--id id is reserved for the table header")
	}
	path := filepath.Join(filepath.Clean(repo), filepath.FromSlash(RelativePath))
	var result SupersedeResult
	err := withLock(path, func() error {
		r, err := supersedeLocked(path, id)
		result = r
		return err
	})
	return result, err
}

func supersedeLocked(path, id string) (SupersedeResult, error) {
	raw, err := readTerminated(path)
	if err != nil {
		return SupersedeResult{}, err
	}
	archivePath := filepath.Join(filepath.Dir(path), filepath.Base(ArchiveRelativePath))
	archive, err := os.ReadFile(archivePath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(archive) == 0) {
		archive = []byte(archiveHeader)
	} else if err != nil {
		return SupersedeResult{}, fmt.Errorf("read %s: %w", archivePath, err)
	} else if len(archive) > 0 && archive[len(archive)-1] != '\n' {
		return SupersedeResult{}, fmt.Errorf("%s must end with a newline", archivePath)
	}

	activeLines := bytes.Split(raw, []byte{'\n'})
	activeAt := -1
	var lesson Lesson
	for i, line := range activeLines {
		if rowHasID(string(line), id) {
			parsed, ok := parseRow(string(line))
			if !ok || parsed.Status != "active" {
				return SupersedeResult{}, fmt.Errorf("lesson id %q has an invalid row", id)
			}
			activeAt, lesson = i, parsed
			break
		}
	}
	archiveHasID := false
	for _, line := range bytes.Split(archive, []byte{'\n'}) {
		if rowHasID(string(line), id) {
			archived, ok := parseRow(string(line))
			if !ok || archived.Status != "superseded" {
				return SupersedeResult{}, fmt.Errorf("lesson id %q has an invalid row in %s", id, archivePath)
			}
			archiveHasID = true
			if activeAt >= 0 {
				lesson.Status = "superseded"
				if string(line) != lesson.row() {
					return SupersedeResult{}, fmt.Errorf("lesson id %q already exists with different content in %s", id, archivePath)
				}
			}
			break
		}
	}
	if activeAt < 0 {
		if archiveHasID {
			return SupersedeResult{Path: path, ArchivePath: archivePath, Lines: lineCount(raw)}, nil
		}
		return SupersedeResult{}, fmt.Errorf("lesson id %q is not active", id)
	}

	lesson.Status = "superseded"
	if !archiveHasID {
		nextArchive := append(append([]byte(nil), archive...), lesson.row()...)
		nextArchive = append(nextArchive, '\n')
		if err := atomicWrite(archivePath, nextArchive); err != nil {
			return SupersedeResult{}, err
		}
	}
	activeLines = append(activeLines[:activeAt], activeLines[activeAt+1:]...)
	nextActive := bytes.Join(activeLines, []byte{'\n'})
	if err := atomicWrite(path, nextActive); err != nil {
		return SupersedeResult{}, err
	}
	return SupersedeResult{Path: path, ArchivePath: archivePath, Lines: lineCount(nextActive), Moved: true}, nil
}

func (l Lesson) validate() error {
	if !idPattern.MatchString(l.ID) {
		return errors.New("--id must match [a-z0-9][a-z0-9._-]*")
	}
	if l.ID == "id" {
		return errors.New("--id id is reserved for the table header")
	}
	fields := []struct{ name, value string }{
		{"--component", l.Component}, {"--kind", l.Kind}, {"--failure", l.Failure},
		{"--prevention", l.Prevention}, {"--evidence", l.Evidence}, {"--status", l.Status},
		{"--reviewed-by", l.ReviewedBy},
	}
	for _, f := range fields {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%s is required", f.name)
		}
		if strings.ContainsAny(f.value, "\r\n|") {
			return fmt.Errorf("%s must be one line without a pipe", f.name)
		}
	}
	if l.Status != "active" && l.Status != "superseded" {
		return errors.New("--status must be active or superseded")
	}
	return nil
}

func (l Lesson) row() string {
	return fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |",
		l.ID, l.Component, l.Kind, l.Failure, l.Prevention, l.Evidence, l.Status, l.ReviewedBy)
}

func rowHasID(row, id string) bool { return strings.HasPrefix(row, "| "+id+" |") }

func parseRow(row string) (Lesson, bool) {
	if !strings.HasPrefix(row, "| ") || !strings.HasSuffix(row, " |") {
		return Lesson{}, false
	}
	fields := strings.Split(strings.TrimSuffix(strings.TrimPrefix(row, "| "), " |"), " | ")
	if len(fields) != 8 {
		return Lesson{}, false
	}
	return Lesson{ID: fields[0], Component: fields[1], Kind: fields[2], Failure: fields[3], Prevention: fields[4], Evidence: fields[5], Status: fields[6], ReviewedBy: fields[7]}, true
}

func readTerminated(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		return nil, fmt.Errorf("%s must end with a newline", path)
	}
	return raw, nil
}

func lineCount(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	return bytes.Count(raw, []byte{'\n'})
}

// withLock serializes the read-modify-write transaction across processes. The
// owner token keeps a stale process from deleting a successor's lock.
func withLock(path string, fn func() error) error {
	lock := path + ".lock"
	token, err := randomToken()
	if err != nil {
		return fmt.Errorf("lesson lock token: %w", err)
	}
	deadline := time.Now().Add(lockWait)
	for {
		if err := os.Mkdir(lock, 0o700); err == nil {
			owner := filepath.Join(lock, "owner")
			if err := os.WriteFile(owner, []byte(token), 0o600); err != nil {
				_ = os.Remove(owner)
				_ = os.Remove(lock)
				return fmt.Errorf("write lesson lock: %w", err)
			}
			defer releaseLock(lock, token)
			return fn()
		} else if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquire lesson lock: %w", err)
		}
		if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > lockStaleAfter {
			stale := lock + ".stale-" + token
			if os.Rename(lock, stale) == nil {
				_ = os.Remove(filepath.Join(stale, "owner"))
				_ = os.Remove(stale)
				continue
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lesson file is busy: %s", lock)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func releaseLock(lock, token string) {
	owner := filepath.Join(lock, "owner")
	got, err := os.ReadFile(owner)
	if err != nil || string(got) != token {
		return
	}
	_ = os.Remove(owner)
	_ = os.Remove(lock)
}

func randomToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func atomicWrite(path string, body []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".LESSONS.md-*")
	if err != nil {
		return fmt.Errorf("create temporary lessons file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temporary lessons file: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary lessons file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary lessons file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary lessons file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	return nil
}
