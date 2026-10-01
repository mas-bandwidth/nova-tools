package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	register(verb{
		name:    "upload",
		summary: "attach the built artifacts to the release draft for TAG, never to a published release",
		help: `usage: go run ./tools/ghrelease upload

Attaches the built artifacts to the release for TAG, and never to a release that
is already published. Exit 0 attached; 1 refused; 2 the environment is
incomplete.

Environment: GITHUB_REPOSITORY, TAG, GH_TOKEN. dist/* is uploaded. A release
that does not exist is created as a draft (--draft) so the artifacts sit on a
draft until the configured verification and publication step, not on a parallel
published release. A release that already exists is uploaded only while it is
still a draft; a published release is refused in one line rather than extended,
so no run can overwrite another release's notes or publish assets into a release
that already crossed the boundary.

The release notes file docs/RELEASE-NOTES-<TAG without its leading v>.md must
exist in the tree, checked before any API call.

One asker, and it reads the status rather than the exit code, because gh api
exits 1 on a 404 and on no answer alike. 200 and 404 are answers; anything else
is not, and the whole response is printed before the refusal. On a 404 the
drafts are discovered from the paginated releases list, which must be a list of
releases each carrying a positive numeric id, a non-empty tag_name and a boolean
draft: a listing that is not is refused, never read as no drafts. Among the
entries for TAG, a published one refuses; one draft uploads to it; none creates
the draft with the notes file; more than one refuses as ambiguous.
`,
		do: doUpload,
	})
}

func doUpload(e env, args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(e.stderr, "usage: %s upload   (the environment carries the rest; run: go run ./tools/ghrelease help upload)\n", tool)
		return 2
	}
	v, ok := e.requireEnv("upload", "GITHUB_REPOSITORY", "TAG", "GH_TOKEN")
	if !ok {
		return 2
	}
	return uploadRelease(e, v["GITHUB_REPOSITORY"], v["TAG"])
}

// notesFile is the release notes file of a tag, relative to the repository root.
func notesFile(tag string) string {
	return "docs/RELEASE-NOTES-" + strings.TrimPrefix(tag, "v") + ".md"
}

// notesPresent reports whether the notes file exists and is not empty.
func (e env) notesPresent(tag string) bool {
	fi, err := os.Stat(filepath.Join(e.root(), filepath.FromSlash(notesFile(tag))))
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
}

// distFiles lists dist/* the way a shell glob does: every entry that does not
// begin with a dot, in byte order, named relative to the root as dist/<name>.
func (e env) distFiles() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(e.root(), "dist"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ent := range entries {
		if !strings.HasPrefix(ent.Name(), ".") {
			out = append(out, "dist/"+ent.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// askResult is one answer of the one asker: the status it carried, 200 or 404,
// and the body of a 200.
type askResult struct {
	code string
	body string
}

// ask runs `gh api -i path` and reads the status line. It returns ok false after
// printing the whole response when the answer is neither 200 nor 404: gh exits 1
// on a 404 and on a dropped connection, an expired token or a rate limit alike,
// so a step that read its exit code would read a transient failure as absence and
// walk down the create path knowing nothing about the release page.
func (e env) ask(path string) (askResult, bool) {
	out, rc := e.client().API("-i", path)
	switch {
	case hasStatus(out, "200"):
		return askResult{"200", answerBody(out)}, true
	case hasStatus(out, "404"):
		return askResult{"404", ""}, true
	}
	fmt.Fprintf(e.stdout, "asking GitHub about %s answered neither 200 nor 404 (gh exit %d):\n", path, rc)
	fmt.Fprintln(e.stdout, chomp(out))
	return askResult{}, false
}

func uploadRelease(e env, repo, tag string) int {
	notes := notesFile(tag)
	if !e.notesPresent(tag) {
		fmt.Fprintf(e.stderr, "refusing: %s does not exist in the tagged tree; write it, land it, and tag the commit that carries it\n", notes)
		return 1
	}

	got, ok := e.ask(fmt.Sprintf("repos/%s/releases/tags/%s", repo, tag))
	if !ok {
		return 1
	}
	refusePublished := func(id string) int {
		fmt.Fprintf(e.stdout, "refusing: %s already has a published release %s; upload to its draft, never a published release\n", tag, id)
		return 1
	}
	gh := e.client()
	// attach runs gh release <sub> with the artifacts after the given arguments.
	attach := func(sub string, args ...string) int {
		files, err := e.distFiles()
		if err != nil || len(files) == 0 {
			fmt.Fprintln(e.stderr, "refusing: dist/ holds no artifact to attach")
			return 1
		}
		return gh.Release(append(append([]string{sub}, args...), files...)...)
	}

	if got.code == "200" {
		var rel map[string]any
		if err := decodeUseNumber([]byte(got.body), &rel); err != nil {
			fmt.Fprintf(e.stderr, "refusing: the release answer for %s is not a release object: %v\n", tag, err)
			return 1
		}
		if orText(rel["draft"], "false") != "true" {
			return refusePublished(orText(rel["id"], "?"))
		}
		return attach("upload", tag, "--clobber")
	}

	// 404: the drafts are in the paginated list of the repository's releases.
	listing, rc := gh.API("--paginate", fmt.Sprintf("repos/%s/releases", repo))
	if rc != 0 {
		fmt.Fprintf(e.stderr, "asking GitHub about repos/%s/releases failed (gh exit %d):\n", repo, rc)
		fmt.Fprintln(e.stderr, chomp(listing))
		return 1
	}
	publishedID, drafts, err := summarizeReleases(listing, tag)
	if err != nil {
		fmt.Fprintf(e.stderr, "refusing: releases listing from repos/%s/releases is invalid or non-array:\n", repo)
		fmt.Fprintln(e.stderr, err.Error())
		return 1
	}
	if publishedID != "" {
		return refusePublished(publishedID)
	}
	switch drafts {
	case 1:
		return attach("upload", tag, "--clobber")
	case 0:
		return attach("create", tag, "--draft", "--verify-tag", "--title", tag, "--notes-file", notes)
	default:
		fmt.Fprintf(e.stderr, "refusing: %s has %d drafts, so there is no one draft to upload to; delete all but one draft for %s, then re-run this workflow\n", tag, drafts, tag)
		return 1
	}
}

// summarizeReleases reads the output of `gh api --paginate .../releases`: any
// number of JSON arrays of releases, one per page. It returns the id of the
// first published release for tag ("" when there is none) and the number of
// drafts for tag, or an error when the listing is not a list of releases each
// carrying what the decision needs. A listing that cannot be read is an error,
// never an empty list of drafts.
func summarizeReleases(listing, tag string) (publishedID string, drafts int, err error) {
	dec := json.NewDecoder(strings.NewReader(listing))
	dec.UseNumber()
	pages := 0
	for {
		var page any
		if derr := dec.Decode(&page); derr == io.EOF {
			break
		} else if derr != nil {
			return "", 0, derr
		}
		pages++
		items, isArray := page.([]any)
		if !isArray {
			return "", 0, errors.New("releases listing is not an array")
		}
		for _, it := range items {
			rel, isObj := it.(map[string]any)
			if !isObj || !positiveNumber(rel["id"]) || !nonEmptyString(rel["tag_name"]) || !isBool(rel["draft"]) {
				return "", 0, errors.New("releases listing contains item missing required metadata")
			}
			if rel["tag_name"] != tag {
				continue
			}
			if rel["draft"] == true {
				drafts++
			} else if publishedID == "" {
				publishedID = text(rel["id"])
			}
		}
	}
	if pages == 0 {
		return "", 0, errors.New("releases listing is empty")
	}
	return publishedID, drafts, nil
}

func positiveNumber(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	f, err := n.Float64()
	return err == nil && f > 0
}

func nonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && s != ""
}

func isBool(v any) bool {
	_, ok := v.(bool)
	return ok
}
