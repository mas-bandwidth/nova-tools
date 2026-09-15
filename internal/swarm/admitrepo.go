package swarm

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A card names the repositories it will clone in its own text: a `REPOS:` line carrying
// one or more `owner/name` (or `https://github.com/owner/name`) tokens, or, when there is
// no `REPOS:` line, the `https://github.com/<owner>/<name>` URLs in the text. At admission
// the tool checks each repository with an unauthenticated request, so a card that would
// need credentials to fetch its own repositories is refused before any runner starts rather
// than failing later inside the wall.

// probeBaseEnv is the ONE test seam on the probe's base URL: a test points it at a local
// httptest server standing in for github. No product path ever sets it, so production is
// always github itself.
const probeBaseEnv = "NOVA_SWARM_PROBE_BASE"

// defaultProbeBase is github's web host, the host a card's repository names live under.
const defaultProbeBase = "https://github.com"

// probeTimeout is how long one repository's reach check may take before it is a probe
// failure rather than a pass. It is the whole request: a repo behind a slow network is
// refused as `probe:`, never silently admitted.
const probeTimeout = 5 * time.Second

// githubRepoRE matches the `https://github.com/<owner>/<name>` URLs a card names when it
// has no `REPOS:` line. owner and name are the characters github permits: letters, digits,
// `_`, `-`, and `.`, which also keeps a trailing `)`, `.`, or `,` out of a pasted URL.
var githubRepoRE = regexp.MustCompile(`https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)

// admitRefusal is a card that could not be admitted and the exact reason, in one piece so
// the label and the refuse form are written in exactly one place.
type admitRefusal struct {
	label string
	why   string // the text after "ADMIT REFUSED <label> ", already escaped
}

func (r *admitRefusal) Error() string {
	return fmt.Sprintf("ADMIT REFUSED %s %s", oneline.Field(r.label), r.why)
}

// cardRepos returns the distinct `owner/name` repositories a card's text names, in the
// order they appear. A `REPOS:` line wins over the URL scan: a card that lists its clones
// by hand names exactly those and nothing a stray URL in a sentence would add.
func cardRepos(text string) []string {
	var repos []string
	seen := map[string]bool{}
	add := func(r string) {
		if r == "" || seen[r] {
			return
		}
		seen[r] = true
		repos = append(repos, r)
	}
	named := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(trimmed), "REPOS:") {
			continue
		}
		named = true
		if idx := strings.Index(trimmed, ":"); idx >= 0 {
			for _, tok := range strings.Fields(trimmed[idx+1:]) {
				add(normRepo(tok))
			}
		}
	}
	if named {
		return repos
	}
	for _, m := range githubRepoRE.FindAllStringSubmatch(text, -1) {
		add(m[1] + "/" + m[2])
	}
	return repos
}

// normRepo folds one repository token — `owner/name`, with or without the
// `https://github.com/` prefix, with or without `.git` — into the bare `owner/name` the
// probe request is built on.
func normRepo(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// probeError is one repository reach check that did not pass. private=true means the
// repository exists but is unreachable without credentials (a 404 from an unauthenticated
// request, or an authentication prompt); private=false is a probe failure of the network
// or the host, which is a refusal of its own kind, never a silent pass.
type probeError struct {
	private bool
	repo    string // owner/name, for a private refusal
	reason  string // the network or status reason, for a probe refusal
}

func (e *probeError) Error() string {
	if e.private {
		return "private-repo: " + oneline.Field(e.repo) + " unreachable without auth"
	}
	return "probe: " + oneline.Escape(e.reason)
}

// probeBase returns the host the probe request is built against: github, unless the test
// seam names another.
func probeBase() string {
	if b := strings.TrimSpace(os.Getenv(probeBaseEnv)); b != "" {
		return strings.TrimRight(b, "/")
	}
	return defaultProbeBase
}

// checkRepos is the admission check: every repository a card names must be reachable with
// an unauthenticated request, or the card is refused under its label. It returns nil when
// the card names no repository or every one of them is public.
func checkRepos(label, text string) error {
	for _, repo := range cardRepos(text) {
		owner, name, ok := strings.Cut(repo, "/")
		if !ok || owner == "" || name == "" {
			continue
		}
		if err := probeRepo(owner, name); err != nil {
			return &admitRefusal{label: label, why: err.Error()}
		}
	}
	return nil
}

// probeClient is the client the admission probe uses. It is a package variable only so a
// test can stand an in-process round trip in for github's host; no production path ever
// reassigns it, and production always dials the real network through the default transport.
var probeClient = &http.Client{Timeout: probeTimeout}

// probeRepo issues one unauthenticated HEAD request against <base>/<owner>/<name> within
// probeTimeout. A 200 means the repository is reachable without credentials. A 401 or 403
// is an authentication prompt, and a 404 is how github answers an unauthenticated request
// for a private repository (so the existence is not leaked): both refuse as private. Any
// other status and any transport error or timeout refuse as a probe failure.
func probeRepo(owner, name string) error {
	url := probeBase() + "/" + owner + "/" + name
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return &probeError{reason: err.Error()}
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return &probeError{reason: err.Error()}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return &probeError{private: true, repo: owner + "/" + name}
	default:
		return &probeError{reason: fmt.Sprintf("HTTP %d from %s", resp.StatusCode, oneline.Escape(url))}
	}
}
