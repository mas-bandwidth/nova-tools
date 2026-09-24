// Package card pushes a sprint card into Redis and releases it when its
// parent has landed.
//
// Push refuses, before any write, a card missing BASE, base-sha, PATHS,
// DEPENDS-ON, or DONE-WHEN, and a card whose repository is private. A
// redirect is private: the page it names can be a login form that returns
// 200, and that page is not the repository. A card whose dependency is not
// landed is stored in the waiting set. Release moves that card into the pool
// only after the dependency is landed, and leaves every other waiting card
// where it is.
package card

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// VerbResult is one card verb (push, release, land, lint). Code 0 wrote or found the same card. Code 2 is a
// refusal. Code 4 is a label whose payload differs from the card already stored.
type VerbResult struct {
	Code   int
	Stdout string
	Stderr string
}

const (
	exitOK       = 0
	exitRefused  = 2
	exitConflict = 4
)

// Required header keys, in the order a refusal names them. base-sha stays
// lowercase because the launchers read that spelling.
var requiredKeys = []string{"BASE", "base-sha", "PATHS", "DEPENDS-ON", "DONE-WHEN"}

var (
	keyRE    = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*):\s*(.*)$`)
	idRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	streamRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	githubRE = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9][A-Za-z0-9._-]*)#([1-9][0-9]*)$`)
)

type dependencyKind string

const (
	dependencyCard   dependencyKind = "card"
	dependencyGitHub dependencyKind = "github"
	dependencyStream dependencyKind = "stream"
	dependencyTask   dependencyKind = "task"
)

type dependency struct {
	Kind  dependencyKind
	Value string
}

func (d dependency) Typed() string { return string(d.Kind) + ":" + d.Value }

// probeClient is the unauthenticated repository check. It does not follow
// redirects. A 404, 401, or 403 is how a forge answers a private repository.
// A redirect to a login page is the same answer: that page returns 200, and
// the default client would report the page instead of the repository.
var probeClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type cardDoc struct {
	Label          string
	Base           string
	BaseSHA        string
	Paths          string
	DependsOn      string // comma-separated ids, empty when the card depends on nothing
	Deps           []dependency
	TypedDependsOn string
	Repo           string // owner/name
	Kind           string
	Type           string // optional TYPE: line, the Jev work type (code, docs, spec, ...); not KIND
	Route          string // ROUTE: pro|flash, the routes.yaml tier the bench harness picks its model from; absent is flash
	Priority       string // PRIORITY: <integer>, the card's score in the pool ZSET; absent is 0
	Payload        string
}

func refused(reason string) VerbResult {
	return VerbResult{Code: exitRefused, Stderr: oneline.Escape(reason) + "\n"}
}

func lint(ctx context.Context, body []byte) (cardDoc, error) {
	label, header, dups := parseHeader(body)
	if len(dups) > 0 {
		return cardDoc{}, fmt.Errorf("%s declared twice", dups[0])
	}
	var missing []string
	for _, key := range requiredKeys {
		if strings.TrimSpace(header[key]) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return cardDoc{}, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	if label == "" {
		label = header["LABEL"]
	}
	if !idRE.MatchString(label) {
		return cardDoc{}, errors.New("missing label; the contract line names the card id")
	}
	baseSHA := header["base-sha"]
	if !shaRE.MatchString(baseSHA) {
		return cardDoc{}, errors.New("base-sha is not 40 lowercase hex")
	}
	depends, deps, err := parseDepends(label, header["DEPENDS-ON"])
	if err != nil {
		return cardDoc{}, err
	}
	routeValue, routeDeclared := header["ROUTE"]
	route, err := parseRoute(routeValue, routeDeclared)
	if err != nil {
		return cardDoc{}, err
	}
	priority, err := parsePriority(header["PRIORITY"])
	if err != nil {
		return cardDoc{}, err
	}
	repo, err := probeRepo(ctx, cloneURL(header))
	if err != nil {
		return cardDoc{}, err
	}
	kind := header["KIND"]
	if kind == "" {
		kind = "model"
	}
	sum := sha256.Sum256(body)
	return cardDoc{
		Label:          label,
		Base:           header["BASE"],
		BaseSHA:        baseSHA,
		Paths:          header["PATHS"],
		DependsOn:      depends,
		Deps:           deps,
		TypedDependsOn: typedDependencies(deps),
		Repo:           repo,
		Kind:           kind,
		Type:           header["TYPE"],
		Route:          route,
		Priority:       priority,
		Payload:        hex.EncodeToString(sum[:]),
	}, nil
}

// The routes a card may carry: the rung of internal/nsprint/route/routes.yaml
// the bench harness picks its model from (nova-sprint routes --tier <route>,
// first allowed route). A card with no ROUTE line is flash.
const (
	RouteFlash = "flash"
	RoutePro   = "pro"
)

// parseRoute accepts ROUTE: pro or ROUTE: flash. An absent line is flash; an
// empty ROUTE: line or any other value is refused, never guessed.
func parseRoute(value string, declared bool) (string, error) {
	if !declared {
		return RouteFlash, nil
	}
	switch value {
	case RouteFlash, RoutePro:
		return value, nil
	}
	return "", fmt.Errorf("ROUTE: %q is not pro or flash", value)
}

// parsePriority accepts PRIORITY: <integer>, the ZADD score card push gives
// the card in the pool. An absent line is 0.
func parsePriority(value string) (string, error) {
	if value == "" {
		return "0", nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return "", fmt.Errorf("PRIORITY: %q is not an integer", value)
	}
	return strconv.FormatInt(n, 10), nil
}

// parseHeader reads the contract line and the contiguous KEY: value block
// under it. A second line with the same key is returned, not dropped.
func parseHeader(body []byte) (label string, header map[string]string, dups []string) {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	start := 0
	if len(lines) > 0 && isContract(lines[0]) {
		label = contractLabel(lines[0])
		start = 1
	}
	header = map[string]string{}
	ended := false
	for _, line := range lines[start:] {
		if strings.TrimSpace(line) == "" && !ended {
			continue
		}
		m := keyRE.FindStringSubmatch(line)
		if m == nil {
			ended = true
			continue
		}
		if ended {
			continue
		}
		key, value := m[1], strings.TrimSpace(m[2])
		if _, seen := header[key]; seen {
			dups = append(dups, key)
			continue
		}
		header[key] = value
	}
	return label, header, dups
}

func isContract(line string) bool {
	return strings.HasPrefix(line, "RESULT:") || strings.HasPrefix(line, "RESULT ")
}

func contractLabel(line string) string {
	rest := line
	switch {
	case strings.HasPrefix(line, "RESULT:"):
		rest = line[len("RESULT:"):]
	case strings.HasPrefix(line, "RESULT "):
		rest = line[len("RESULT "):]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || strings.HasPrefix(fields[0], "sha=") {
		return ""
	}
	return fields[0]
}

// parseDepends accepts the one DEPENDS-ON vocabulary and assigns every entry
// a type before it reaches Redis. An unprefixed entry is a same-sprint card id.
func parseDepends(label, value string) (string, []dependency, error) {
	if value == "none" || value == "-" {
		return "", nil, nil
	}
	var deps []dependency
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			return "", nil, errors.New("DEPENDS-ON has an empty entry")
		}
		if entry == "none" || entry == "-" {
			return "", nil, errors.New("DEPENDS-ON: none and - must stand alone")
		}
		dep, err := parseDependency(entry)
		if err != nil {
			return "", nil, err
		}
		if dep.Kind == dependencyCard && dep.Value == label {
			return "", nil, errors.New("DEPENDS-ON names this card")
		}
		if seen[dep.Typed()] {
			continue
		}
		seen[dep.Typed()] = true
		deps = append(deps, dep)
	}
	if len(deps) == 0 {
		return "", nil, errors.New("DEPENDS-ON names no card")
	}
	entries := make([]string, len(deps))
	for i, dep := range deps {
		entries[i] = dep.Value
		if dep.Kind == dependencyStream {
			entries[i] = "stream/" + dep.Value
		} else if dep.Kind == dependencyTask {
			entries[i] = "task:" + dep.Value
		}
	}
	return strings.Join(entries, ","), deps, nil
}

func parseDependency(entry string) (dependency, error) {
	if m := githubRE.FindStringSubmatch(entry); m != nil {
		return dependency{Kind: dependencyGitHub, Value: entry}, nil
	}
	if slug, ok := strings.CutPrefix(entry, "stream/"); ok {
		if !streamRE.MatchString(slug) {
			return dependency{}, fmt.Errorf("DEPENDS-ON: %s is not stream/<slug>", entry)
		}
		return dependency{Kind: dependencyStream, Value: slug}, nil
	}
	if id, ok := strings.CutPrefix(entry, "task:"); ok {
		if !idRE.MatchString(id) {
			return dependency{}, fmt.Errorf("DEPENDS-ON: %s is not task:<id>", entry)
		}
		return dependency{Kind: dependencyTask, Value: id}, nil
	}
	if !idRE.MatchString(entry) {
		return dependency{}, fmt.Errorf("DEPENDS-ON: %s is not a card id, owner/repo#n, stream/<slug>, or task:<id>", entry)
	}
	return dependency{Kind: dependencyCard, Value: entry}, nil
}

func typedDependencies(deps []dependency) string {
	entries := make([]string, len(deps))
	for i, dep := range deps {
		entries[i] = dep.Typed()
	}
	return strings.Join(entries, ",")
}

func cloneURL(header map[string]string) string {
	if u := strings.TrimSpace(header["base-repo"]); u != "" {
		return u
	}
	repo := strings.TrimSpace(header["REPO"])
	if repo == "" {
		return ""
	}
	if strings.Contains(repo, "://") {
		return repo
	}
	return "https://github.com/" + repo
}

type privateRepoError struct{ Name string }

func (e *privateRepoError) Error() string { return "private repo " + e.Name }

// probeRepo refuses a repository an unauthenticated request cannot read.
// 404, 401, and 403 are private. A redirect is private too: it is not the
// repository, and following it can land on a login page that returns 200.
// Anything else that is not 200 is a probe failure, which is also a refusal:
// an unread repository is not a public one.
func probeRepo(ctx context.Context, cloneURL string) (string, error) {
	if strings.TrimSpace(cloneURL) == "" {
		return "", errors.New("missing base-repo")
	}
	probeURL, name, err := repoProbeURL(cloneURL)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, probeURL, nil)
	if err != nil {
		return "", fmt.Errorf("probe: %s", oneline.Escape(err.Error()))
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("probe: %s", oneline.Escape(err.Error()))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch {
	case resp.StatusCode == http.StatusOK:
		return name, nil
	case resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		isRedirect(resp.StatusCode):
		return "", &privateRepoError{Name: name}
	default:
		return "", fmt.Errorf("probe: HTTP %d", resp.StatusCode)
	}
}

func isRedirect(code int) bool {
	return code >= 300 && code < 400
}

func repoProbeURL(cloneURL string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(cloneURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", "", errors.New("base-repo is not an http(s) owner/name URL")
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !idRE.MatchString(parts[0]) || !idRE.MatchString(parts[1]) {
		return "", "", errors.New("base-repo is not an http(s) owner/name URL")
	}
	u.Path = "/" + parts[0] + "/" + parts[1]
	u.RawQuery, u.Fragment = "", ""
	return u.String(), parts[0] + "/" + parts[1], nil
}
