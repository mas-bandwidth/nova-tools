// Package prkey names the PR record: pr:<name>:<n>, keyed by the bare
// repository name (nova-tools, rowan-tools), never owner/name. It is the one
// key function every writer and reader of the record calls: the pr verb and
// the stream lander (internal/nsprint/land/stream, whose land_stream.lua
// mirrors Key), read, ci run (ci_run.lua copies the summary under the same
// key), a work copy's wrapper (harvestcopy, for a card repo in owner/name
// form, #3740) and the reconciler's PR legs. Before it, pr record wrote
// pr:mas-bandwidth/rowan-tools:375 while read post and ci read
// pr:rowan-tools:375, so a recorded PR could never be read.
//
// Every verb that takes --repo for this record accepts owner/name or name;
// Split normalises either to the owner and the bare name, the owner
// defaulting to DefaultOwner.
//
// Not this record: the reconciler's idem key pr:<repo>:<branch>
// (internal/nsprint/reconcile, PRKey) is a field of the sprint's idem hash,
// the sprint-scoped s:<S>:pr:<repo>:<n> hashes are the card pipeline's own,
// and nova-wake's pr:<owner/repo>#<n> items live in its own state.
package prkey

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultOwner is the owner of a --repo given as a bare name.
const DefaultOwner = "mas-bandwidth"

// Split reads --repo as owner/name or name and returns both parts, the owner
// defaulting to DefaultOwner. It refuses an empty part, a second slash and
// any space or colon (a colon would split the key).
func Split(repo string) (owner, name string, err error) {
	repo = strings.TrimSpace(repo)
	owner, name, found := strings.Cut(repo, "/")
	if !found {
		owner, name = DefaultOwner, repo
	}
	if owner == "" || name == "" || strings.Contains(name, "/") || strings.ContainsAny(repo, " :\t\r\n") {
		return "", "", fmt.Errorf("repo %q is not <owner>/<name> or <name>", repo)
	}
	return owner, name, nil
}

// Name is the bare repository name of repo (owner/name or name): the text
// after the last slash. It never fails; callers that take a flag call Split
// first.
func Name(repo string) string {
	repo = strings.TrimSpace(repo)
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// Full is owner/name for repo given either way.
func Full(repo string) (string, error) {
	owner, name, err := Split(repo)
	if err != nil {
		return "", err
	}
	return owner + "/" + name, nil
}

// Key is the PR record of PR n in repo (owner/name or name): pr:<name>:<n>.
func Key(repo string, n int) string { return KeyText(repo, strconv.Itoa(n)) }

// KeyText is Key for a PR number already held as decimal text.
func KeyText(repo, n string) string { return "pr:" + Name(repo) + ":" + n }

// HeadKey is the head index of repo at sha: pr:<name>:head:<sha>, a set of
// the PR numbers whose record's head is sha (land.RecordPRHead keeps it; the
// runner receipt folds its CI word through it). The member is the decimal
// PR number; the key never collides with a record, whose last part is a
// number.
func HeadKey(repo, sha string) string { return "pr:" + Name(repo) + ":head:" + sha }
