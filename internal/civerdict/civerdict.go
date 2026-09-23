// Package civerdict is the one reader of a commit's CI verdict record,
// ci:<owner/repo>:<sha>, shared by nova-sprint's land and nova-merge.
//
// The record is a HASH whose "verdict" field is OK, or FAIL plus the package
// and the test (the sprint CI card's end writes it; internal/sprintci). Two
// readers that disagreed on the shape -- land read HGETALL, nova-merge read GET
// -- turned every written key into WRONGTYPE and every refusal into
// "ci: MISSING" (nova-tools #2924 follow-up). Both now read through here.
package civerdict

import (
	"context"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	// Field is the hash field that carries the verdict word.
	Field = "verdict"
	// OK is the only green verdict word.
	OK = "OK"
	// Missing is what a reader prints when no record says anything.
	Missing = "MISSING"
)

// Key is where one commit's verdict lives: ci:<owner/repo>:<sha>.
func Key(repo, sha string) string {
	return "ci:" + repo + ":" + strings.TrimSpace(sha)
}

// Of is the verdict word a record carries, trimmed, or "" when the record is
// absent or has no verdict. "" is NOT a verdict: a missing record is no
// evidence either way.
func Of(fields map[string]string) string {
	if fields == nil {
		return ""
	}
	return strings.TrimSpace(fields[Field])
}

// Green reports whether the verdict word is exactly OK.
func Green(verdict string) bool { return strings.TrimSpace(verdict) == OK }

// Read fetches one commit's record with HGETALL. An absent key is an empty
// map and a nil error. Any error (WRONGTYPE, NOPERM, a dead store) is returned
// as is: it is an unreadable record, never a verdict.
func Read(ctx context.Context, c redis.Cmdable, repo, sha string) (map[string]string, error) {
	fields, err := c.HGetAll(ctx, Key(repo, sha)).Result()
	if errors.Is(err, redis.Nil) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return fields, nil
}
