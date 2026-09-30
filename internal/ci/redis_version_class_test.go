package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// THE CLASS RULE: ONE REDIS VERSION EVERYWHERE.
//
// The repository names a Redis version in several places: the functional
// image's Containerfile builds one from source, the CI installer builds one on
// a runner that has none, the image's README and the nova-table README state
// the one a reader should run, and comments in the code say which server a
// captured reply or error text came from. The owner's ruling (2026-09-29): if
// the versions differ, standardize on the most recent, so there is one. A test
// is only worth what it ran against, and two Redis versions in one tree means
// a green functional tier proves nothing about the other.
//
// The reference is `ARG REDIS_VERSION` in infra/functional-image/Containerfile,
// the version the whole functional tier runs on. Every other place that names a
// Redis version must equal it, and this rule reads them as text, in the unit
// tier, with no container and no server:
//
//   - the named places each name it at least once (redisVersionNamedFiles), so
//     a reworded README cannot drop out of the check unseen;
//   - a sweep over every text file of the living tree reads any three-part
//     version written right after the word Redis in the shapes a version takes
//     (`Redis 8.10.2`, `redis-server 8.10.2`, `redis-8.10.2.tar.gz`,
//     `REDIS_VERSION=8.10.2`, `REDIS_VERSION="8.10.2"`, the JSON key
//     `"REDIS_VERSION": "8.10.2"`, `redis_version:8.10.2`,
//     `redis:8.10.2`, the apt pin `redis-server=6:8.10.2-1`, the output of
//     `redis-server --version` (`v=8.10.2`), a version or a name in backticks),
//     so a place added tomorrow is held the day it lands with no list to edit.
//     The files it reads are the Go, Markdown, YAML, shell, JSON, Python, env,
//     text and config files and the ones named Containerfile, Dockerfile or
//     Makefile (redisVersionReadsFile).
//
// What it does not read: release history (CHANGELOG.md, the release notes),
// captured data under testdata, deprecated/ (the shared tree does not walk it),
// and the deprecated-in-place packages listed in redisVersionHistory, whose
// fixtures are the recorded output of servers of other versions. One- and
// two-part mentions (`Redis 7`, `Redis 6.2`, "before Redis 7") name a feature
// generation, never the version the repository runs, and are not read; the
// version a reader is told to run is always written in full. `go-redis v9.22.0`
// and `nova-redis 1.0.0` are library and tool versions and are not read.
//
// It cannot read what apt or Homebrew installs on a hosted runner, and it cannot
// check a sha256 against a version offline: the image build's `sha256sum -c`
// does that, against the tarball itself.

const (
	redisVersionRef       = "infra/functional-image/Containerfile"
	redisVersionInstaller = ".github/scripts/install-redis-server.sh"
	redisVersionSelf      = "internal/ci/redis_version_class_test.go"
)

// redisVersionNamedFiles are the places that must each name the version: the
// reference itself, the installer's source build, and the two documents that
// tell a reader which Redis to run.
var redisVersionNamedFiles = []string{
	redisVersionRef,
	redisVersionInstaller,
	"infra/functional-image/README.md",
	"docs/nova-table/README.md",
}

// redisVersionHistory are the paths the sweep does not read, each with why.
// The rule checks that each still exists, so a stale row is a red run and the
// list can only be what the tree holds.
var redisVersionHistory = []struct{ Prefix, Why string }{
	{"CHANGELOG.md", "release history: it records what each release said"},
	{"docs/RELEASE-NOTES-", "release history: the notes of a release that shipped"},
	{"internal/nsprint/acl/", "deprecated in place (deprecated/PACKAGES): its fixtures are the recorded output of redis-server 7.0.15, 8.0.5 and 8.10.2"},
}

var (
	// redisVersionRe: the word Redis, an optional server/cli/tools/version word
	// that may be closed by a quote or a backtick (a JSON key `"REDIS_VERSION"`,
	// a name in backticks; a bare `"redis"` key is a client library's pin and is
	// not read), a separator (one or two of ` =:@v-`, or the ` v=` of
	// `redis-server --version`), an optional quote or backtick opening the
	// version, an optional apt epoch (`6:`), then a three-part version.
	// Case-insensitive, so REDIS_VERSION= and redis_version: read too. \x60 is
	// the backtick.
	redisVersionRe = regexp.MustCompile(`(?i)\bredis(?:[-_ ]?(?:server|cli|tools|version)["'\x60]?|\x60)?(?:[ =:@v-]{1,2}|[ ]v=)["'\x60]?(?:\d+:)?(\d+\.\d+\.\d+)`)
	// redisInstallerPinRe: the installer's `ver=8.10.2`, read only in that file.
	redisInstallerPinRe = regexp.MustCompile(`^\s*ver=["']?(\d+\.\d+\.\d+)["']?\s*$`)
	// redisVersionTextExts are the extensions of the files the sweep reads.
	redisVersionTextExts = map[string]bool{
		".go": true, ".md": true, ".yml": true, ".yaml": true, ".sh": true, ".txt": true,
		".ini": true, ".j2": true, ".lua": true, ".card": true, ".ps1": true, ".toml": true,
		".json": true, ".env": true, ".py": true,
	}
	redisVersionTextNames = map[string]bool{"Containerfile": true, "Dockerfile": true, "Makefile": true}
	// redisVersionUnreadDirs are the directories no file under is read: captured
	// data and other people's code, none of which says what the repository runs.
	redisVersionUnreadDirs = []string{"testdata", "vendor", "node_modules"}
)

// redisVersionSite is one place a Redis version is written.
type redisVersionSite struct {
	File    string
	Line    int
	Version string
}

func (s redisVersionSite) String() string { return fmt.Sprintf("%s:%d", s.File, s.Line) }

// redisVersionSites reads the versions one file names, by line. rel is the
// repo-relative path: the installer's `ver=` pin is read only in the installer.
func redisVersionSites(rel, text string) []redisVersionSite {
	var out []redisVersionSite
	for i, line := range strings.Split(text, "\n") {
		for _, m := range redisVersionRe.FindAllStringSubmatchIndex(line, -1) {
			start, vs, ve := m[0], m[2], m[3]
			// `go-redis v9.22.0`, `nova-redis 1.0.0`, `--redis 100.115.99.19`: a name
			// glued to Redis with a hyphen is another program's, and a version
			// followed by another dot-number is an address or a four-part id.
			if start > 0 && line[start-1] == '-' {
				continue
			}
			if ve+1 < len(line) && line[ve] == '.' && line[ve+1] >= '0' && line[ve+1] <= '9' {
				continue
			}
			out = append(out, redisVersionSite{File: rel, Line: i + 1, Version: line[vs:ve]})
		}
		if rel == redisVersionInstaller {
			if m := redisInstallerPinRe.FindStringSubmatch(line); m != nil {
				out = append(out, redisVersionSite{File: rel, Line: i + 1, Version: m[1]})
			}
		}
	}
	return out
}

// redisVersionProblems compares every site with the reference and says which
// places name which version, so a red run shows the whole split at once.
func redisVersionProblems(ref string, sites []redisVersionSite) []string {
	by := map[string][]string{}
	for _, s := range sites {
		by[s.Version] = append(by[s.Version], s.String())
	}
	var versions []string
	for v := range by {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	var out []string
	for _, v := range versions {
		if v == ref {
			continue
		}
		places := by[v]
		sort.Strings(places)
		out = append(out, fmt.Sprintf("Redis %s is named at %s, but the one version is %s (ARG REDIS_VERSION in %s); make every place name %s, and take REDIS_SHA256 from the project's published hash for that release", v, strings.Join(places, ", "), ref, redisVersionRef, ref))
	}
	return out
}

// redisVersionReferenceOf reads ARG REDIS_VERSION out of the Containerfile.
func redisVersionReferenceOf(containerfile string) string {
	if m := regexp.MustCompile(`(?m)^ARG REDIS_VERSION=(\d+\.\d+\.\d+)\s*$`).FindStringSubmatch(containerfile); m != nil {
		return m[1]
	}
	return ""
}

func redisVersionSkipped(rel string) bool {
	if rel == redisVersionSelf {
		return true
	}
	for _, h := range redisVersionHistory {
		if strings.HasPrefix(rel, h.Prefix) {
			return true
		}
	}
	return false
}

// redisVersionReadsFile says whether the sweep reads the file at this
// repo-relative path: not this test, not release history, not captured data or
// vendored code, and only a text kind: an extension of redisVersionTextExts, or a
// name of redisVersionTextNames. (deprecated/ never reaches the sweep: the
// shared tree does not walk it.)
func redisVersionReadsFile(rel string) bool {
	if redisVersionSkipped(rel) {
		return false
	}
	f := &treeFile{Rel: rel}
	for _, d := range redisVersionUnreadDirs {
		if f.HasDirNamed(d) {
			return false
		}
	}
	base := rel[strings.LastIndex(rel, "/")+1:]
	return redisVersionTextExts[filepath.Ext(rel)] || redisVersionTextNames[base]
}

// TestRedisIsOneVersionEverywhere: every place the repository names a Redis
// version names the same one, the version the functional image builds.
func TestRedisIsOneVersionEverywhere(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)

	ref := redisVersionReferenceOf(readFile(t, filepath.Join(tree.Root, filepath.FromSlash(redisVersionRef))))
	if ref == "" {
		t.Fatalf("%s carries no `ARG REDIS_VERSION=<major.minor.patch>`; the one Redis version is that ARG", redisVersionRef)
	}

	for _, h := range redisVersionHistory {
		found := false
		for _, f := range tree.Files {
			if strings.HasPrefix(f.Rel, h.Prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("redisVersionHistory names %q (%s) and nothing in the tree is there; drop the row", h.Prefix, h.Why)
		}
	}

	var sites []redisVersionSite
	perFile := map[string]int{}
	for _, f := range tree.Files {
		if !redisVersionReadsFile(f.Rel) {
			continue
		}
		src := f.Src
		if src == nil {
			raw, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatal(err)
			}
			src = raw
		}
		for _, s := range redisVersionSites(f.Rel, string(src)) {
			sites = append(sites, s)
			perFile[f.Rel]++
		}
	}

	// A sweep that found nothing checked nothing: each named place is there.
	for _, rel := range redisVersionNamedFiles {
		if perFile[rel] == 0 {
			t.Errorf("%s names no Redis version (a `Redis <major.minor.patch>` phrase, or the installer's `ver=`); this file is one of the places the rule holds to the one version", rel)
		}
	}
	for _, p := range redisVersionProblems(ref, sites) {
		t.Error(p)
	}
}

// TestRedisVersionRuleSeesEachShape is the control: the reader finds each
// shape a version is written in, leaves the other programs' versions alone,
// and the comparison names every place of a version that differs.
func TestRedisVersionRuleSeesEachShape(t *testing.T) {
	t.Parallel()
	seen := []struct {
		rel, text, want string
	}{
		{"a.md", "| redis-server, redis-cli | Redis 8.10.2 built from source |", "8.10.2"},
		{"a.md", "Redis 8.10.2 that does not know the command", "8.10.2"},
		{"a.go", "// the replies are that server's (redis-server 8.0.5).", "8.0.5"},
		{"a.sh", "tar -xzf /tmp/redis-8.10.2.tar.gz -C /tmp", "8.10.2"},
		{"Containerfile", "ARG REDIS_VERSION=8.10.2", "8.10.2"},
		{"a.txt", "redis_version:7.0.15", "7.0.15"},
		{"a.yml", "image: docker.io/library/redis:7.4.11", "7.4.11"},
		{"a.md", "install Redis v8.0.5 first", "8.0.5"},
		{"a.md", "brew install redis@8.0.5", "8.0.5"},
		{redisVersionInstaller, "\tver=8.10.2", "8.10.2"},
		// a quoted pin, double and single
		{"a.sh", `REDIS_VERSION="8.0.5"`, "8.0.5"},
		{"a.sh", `export REDIS_VERSION='8.0.5'`, "8.0.5"},
		{"a.json", `{"REDIS_VERSION": "8.0.5"}`, "8.0.5"},
		// an apt pin: the epoch before the version, the package revision after
		{"a.md", "sudo apt-get install -y redis-server=6:8.0.5-1", "8.0.5"},
		{"a.md", "sudo apt-get install -y redis-tools=6:8.0.5-1", "8.0.5"},
		// the output of `redis-server --version`
		{"a.txt", "Redis server v=8.0.5 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=0", "8.0.5"},
		// a version in backticks, and a name in backticks
		{"a.md", "the server is Redis `8.10.2`, built from source", "8.10.2"},
		{"a.md", "the `redis-server` 8.10.2 binary", "8.10.2"},
		{"a.md", "`REDIS_VERSION`=8.10.2", "8.10.2"},
	}
	for _, c := range seen {
		got := redisVersionSites(c.rel, c.text)
		if len(got) != 1 || got[0].Version != c.want {
			t.Errorf("%s %q: the reader found %v, want one site naming %s", c.rel, c.text, got, c.want)
		}
	}
	// The files the sweep reads: the text kinds by extension or by name, and
	// nothing that is history or captured data.
	for _, rel := range []string{
		"a.go", "docs/a.md", "x/a.yml", "x/a.yaml", ".github/scripts/a.sh", "a.txt",
		"infra/functional-image/Containerfile", "Containerfile", "Dockerfile", "infra/x/Dockerfile", "Makefile",
		"fleet/land/ruleset.json", "a.json", ".env", "deploy/prod.env", "tools/check.py",
	} {
		if !redisVersionReadsFile(rel) {
			t.Errorf("%s: the sweep must read this file (a version written there is a place the repository names one)", rel)
		}
	}
	for _, rel := range []string{
		"CHANGELOG.md", "docs/RELEASE-NOTES-1.2.3.md", "internal/nsprint/acl/acl_test.go",
		"internal/foo/testdata/capture.json", "internal/foo/testdata/info.txt", "internal/foo/testdata/deep/x/reply.py",
		"vendor/x/y.go", "web/node_modules/p/package.json",
		redisVersionSelf, "a.png", "a.bin", "notes",
	} {
		if redisVersionReadsFile(rel) {
			t.Errorf("%s: the sweep must not read this file (release history, captured data, another's code, or not text)", rel)
		}
	}
	unseen := []struct{ rel, text string }{
		{"a.go", "go-redis v9.22.0 sends it after HELLO 3"},
		{"a.md", "nova-redis 1.0.0 (commit abc)"},
		{"a.md", "Redis 7 or later"},
		{"a.go", "needs no Redis 6.2 exclusive range"},
		{"a.go", "\"Ready to accept connections\" before Redis 7"},
		{"a.md", "nova-config status redis=127.0.0.1:6379 machine=1"},
		{"a.md", "nova-sprint table --seat studio --redis 100.115.99.19:6380"},
		{"a.txt", "260 1 02-00:00:00 0.0 501 redis-server 127.0.0.1:26491"},
		{"a.go", "miniredis v2.35.0"},
		{"a.sh", "ver=8.10.2"},
		{"a.md", "`go-redis` v9.22.0 is the client"},
		{"a.md", "`nova-redis` 1.0.0 is the tool"},
		{"a.md", "install Redis `7` or later"},
		{"a.sh", `REDIS_VERSION="7"`},
		{"a.md", "sudo apt-get install -y redis-server"},
		{"a.txt", "Redis server v=7 sha=00000000:0"},
		{"a.md", "the address is redis=127.0.0.1:6379"},
		{"a.json", `{"redis": "4.6.0"}`},
		{"a.json", `{"go-redis": "9.22.0"}`},
	}
	for _, c := range unseen {
		if got := redisVersionSites(c.rel, c.text); len(got) != 0 {
			t.Errorf("%s %q: the reader found %v, want none: that is not a Redis version the repository runs", c.rel, c.text, got)
		}
	}

	same := []redisVersionSite{{"x", 1, "8.10.2"}, {"y", 2, "8.10.2"}}
	if p := redisVersionProblems("8.10.2", same); len(p) != 0 {
		t.Errorf("two places naming the reference are one version, got %v", p)
	}
	split := []redisVersionSite{{"x", 1, "8.10.2"}, {"y", 2, "8.0.5"}, {"z", 3, "8.0.5"}, {"w", 4, "7.4.11"}}
	p := redisVersionProblems("8.10.2", split)
	if len(p) != 2 || !strings.Contains(p[1], "y:2, z:3") && !strings.Contains(p[0], "y:2, z:3") {
		t.Errorf("a split of three versions must be reported once per differing version, naming every place; got %v", p)
	}
	if p := redisVersionProblems("8.10.2", []redisVersionSite{{"x", 1, "8.0.5"}}); len(p) != 1 {
		t.Errorf("a lone place that is not the reference is a difference, got %v", p)
	}
	if p := redisVersionProblems("8.10.2", nil); len(p) != 0 {
		t.Errorf("no places is not a difference (the rule's own floor catches an empty read), got %v", p)
	}
}
