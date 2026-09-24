// Package lessons appends reviewed operational lessons to a repository's
// bounded docs/LESSONS.md file (nova-tools#2498 S9).
package lessons

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	RelativePath = "docs/LESSONS.md"
	MaxLines     = 40
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Lesson is one reviewed, evidence-backed operational lesson. Each field is
// one line so each lesson occupies exactly one physical line in the capped
// file.
type Lesson struct {
	ID, Component, Kind, Failure, Prevention, Evidence, Status, ReviewedBy string
}

// Result describes whether Append added a row or found an identical retry.
type Result struct {
	Path     string
	Lines    int
	Appended bool
}

// Append admits one lesson to repo/docs/LESSONS.md. The repository and file
// must already exist: callers name the repository explicitly and this verb
// never guesses or initializes one. Reusing an ID with the same row is an
// idempotent retry; reusing it with different content refuses.
func Append(repo string, lesson Lesson) (Result, error) {
	if strings.TrimSpace(repo) == "" {
		return Result{}, errors.New("--repo <dir> is required")
	}
	if err := lesson.validate(); err != nil {
		return Result{}, err
	}
	path := filepath.Join(filepath.Clean(repo), filepath.FromSlash(RelativePath))
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		return Result{}, fmt.Errorf("%s must end with a newline", path)
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
		if strings.HasPrefix(text, "| "+lesson.ID+" |") {
			return Result{}, fmt.Errorf("lesson id %q already exists with different content", lesson.ID)
		}
	}
	if lines+1 > MaxLines {
		return Result{}, fmt.Errorf("append would make %s %d lines; cap is %d", path, lines+1, MaxLines)
	}
	next := append(append([]byte(nil), raw...), row...)
	next = append(next, '\n')
	if err := atomicWrite(path, next); err != nil {
		return Result{}, err
	}
	return Result{Path: path, Lines: lines + 1, Appended: true}, nil
}

func (l Lesson) validate() error {
	if !idPattern.MatchString(l.ID) {
		return errors.New("--id must match [a-z0-9][a-z0-9._-]*")
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

func lineCount(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	return bytes.Count(raw, []byte{'\n'})
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
