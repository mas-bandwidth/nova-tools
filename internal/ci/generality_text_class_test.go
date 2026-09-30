package ci

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// generality_text_class_test.go is the generality rule for every living text file
// that is not Go. generality_class_test.go holds living .go files under cmd/ and
// internal/ to the rule that the tools know concepts and never our fleet: no machine,
// host, tailnet, friend or person name. The same rule binds the rest of the tree,
// and a scan that reads only .go files let a fleet's tailnet address, its coordinator
// seat, its user names and its home paths ride in fleet/*.tsv, *.yml and *.j2 unseen
// (docs/SPEC-CI.md#generality-text).
//
// One inventory. The name list is the one in generality_class_test.go
// (forbiddenTokens, through extractTokensFromText); this file adds no name to code.
// It adds three shapes that are patterns and not names:
//   - tailnet-address: an IPv4 address in 100.64.0.0/10 (the CGNAT range a tailnet
//     hands out), an IPv6 address under the tailnet prefix fd7a:115c:a1e0, and a
//     hostname under .ts.net. The range written as a CIDR (100.64.0.0/10) is the
//     concept and is not a finding.
//   - home-path: /Users/<name>, /home/<name> and a drive path C:/Users/<name> whose
//     <name> is not one of the generic names in genericHomeUsers (a documented
//     placeholder, a container user this repository defines, or a hosted runner's).
//   - the project's own public links are not findings: mas-bandwidth/nova-tools (the
//     module path and issue references), mas-bandwidth/nova (the seed) and
//     mas-bandwidth/secrets (the secrets store design), and the @mas-bandwidth.com
//     contact addresses in docs/SECURITY.md. Any other reference to that account is.
//
// What is scanned. Every file the shared walk finds outside deprecated/ whose name is
// Makefile or Containerfile or ends in one of textScanSuffixes (.lua .tsv .yml .yaml
// .j2 .md .sh .json .txt, the workflows under .github/ among them, and the rest of the
// text formats the repository ships). .go files are the other test's; .git is never
// read.
//
// Two lists, both shrink-only:
//   - generality_text_fixtures_allowlist.txt: `path reason`. A whole file whose names
//     are recorded data (a captured transcript, a fixture of a public issue), with the
//     reason on the row. A row for a file with no finding is stale and fails.
//   - generality_text_allowlist.txt: `path:token count`, the debt. A finding not listed
//     fails; a count that rises fails; a count that falls, or a row with no finding,
//     fails until the row shrinks. The update run (NOVA_CI_UPDATE=1) only ever removes.

const (
	generalityTextFixturesPath = "testdata/generality_text_fixtures_allowlist.txt"
	generalityTextDebtPath     = "testdata/generality_text_allowlist.txt"
)

// textScanSuffixes are the file suffixes the text scan reads.
var textScanSuffixes = []string{
	".lua", ".tsv", ".yml", ".yaml", ".j2", ".md", ".sh", ".json", ".txt",
	".ini", ".tmpl", ".tla", ".lisp", ".sexp", ".cfg", ".card", ".sql", ".py", ".ps1",
	".jsonl", ".log", ".notes",
}

// textScanNames are the file names read whatever their suffix.
var textScanNames = map[string]bool{"Makefile": true, "Containerfile": true}

// genericHomeUsers are the <name> of a home path that names no person or machine of ours:
// documented placeholders, the users the repository's own container images define, and
// the users a hosted runner image provides.
var genericHomeUsers = map[string]bool{
	"user": true, "username": true, "you": true, "me": true, "example": true, "name": true,
	"ubuntu": true, "runner": true, "runneradmin": true, "runn": true, "nova": true, "bench": true, "card": true,
	"ada": true, "alice": true, "bob": true, "agent": true, "worker": true,
	"someone": true, "anyone": true, "x": true, "u": true,
}

var (
	reTailnetV4   = regexp.MustCompile(`(?:^|[^0-9.])(100)\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\b(/\d+)?`)
	reTailnetV6   = regexp.MustCompile(`(?i)\bfd7a:115c:a1e0\b(::/\d+)?`)
	reTailnetName = regexp.MustCompile(`(?i)[a-z0-9-]+\.ts\.net\b`)
	reHomePath    = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./}~-])(?:/Users/|/home/)([A-Za-z0-9_.-]+)|[A-Za-z]:[/\\]Users[/\\]([A-Za-z0-9_.-]+)`)
	rePosixClass  = regexp.MustCompile(`\[:space:\]`)
	// The project's own public repositories are its identity, not fleet names: this
	// repository, the seed it grows from and the store of the secrets design. The
	// name must end there, so mas-bandwidth/nova-tools-x is not the project's.
	// It is anchored on the left: the start of the line, a character that cannot continue
	// a path (or an escaped tab, newline or return, as a JSON log writes one), optionally followed by a URL scheme, github.com/ (or api.github.com/) and
	// repos/ (the API path), so
	// other/mas-bandwidth/nova is not the project's. Group 2 is the part blanked.
	reOwnIdentity = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_./-]|\\[nrt])(?:https?://)?(?:(?:api\.)?github\.com/)?(?:repos/)?(mas-bandwidth/(?:nova-tools|nova|secrets))([^A-Za-z0-9_-]|$)`)
	// The project's contact addresses, in the one document that publishes them.
	// Exactly the two published addresses, not any local part. Group 2 is the address.
	reContact = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9._+-])((?:glenn|rowan)@mas-bandwidth\.com)(?:\.?(?:[^A-Za-z0-9.-]|$))`)
)

// lineMayContainText is the cheap prefilter: a line with none of these substrings has
// no finding of any shape.
func lineMayContainText(line string) bool {
	if lineMayContainGenerality(line) {
		return true
	}
	lower := strings.ToLower(line)
	for _, s := range []string{"100.", "fd7a", "ts.net", "/users/", "/home/", `\users\`} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// blankGroup replaces the bytes of group g of every match of re with spaces. A match
// consumes the character after it, so the scan repeats until none is left to blank: two
// adjacent links are both found.
func blankGroup(s string, re *regexp.Regexp, g int) string {
	for {
		idx := re.FindAllStringSubmatchIndex(s, -1)
		if len(idx) == 0 {
			return s
		}
		b := []byte(s)
		for _, m := range idx {
			for i := m[2*g]; i < m[2*g+1]; i++ {
				b[i] = ' '
			}
		}
		s = string(b)
	}
}

// contactDoc is the document that publishes the project's contact addresses.
const contactDoc = "docs/SECURITY.md"

// generalityTextFindings returns every finding of one line of a text file, as tokens:
// forbidden names, and the pattern findings tailnet-address and home-path.
func generalityTextFindings(rel, line string) []string {
	if !lineMayContainText(line) {
		return nil
	}
	var out []string
	for _, m := range reTailnetV4.FindAllStringSubmatch(line, -1) {
		if m[5] != "" {
			continue // a CIDR: the concept of the range, not an address
		}
		o2, _ := strconv.Atoi(m[2])
		o3, _ := strconv.Atoi(m[3])
		o4, _ := strconv.Atoi(m[4])
		if o2 >= 64 && o2 <= 127 && o3 <= 255 && o4 <= 255 {
			out = append(out, "tailnet-address")
		}
	}
	for _, m := range reTailnetV6.FindAllStringSubmatch(line, -1) {
		if m[1] == "" { // the prefix written as a CIDR (fd7a:115c:a1e0::/48) is the concept
			out = append(out, "tailnet-address")
		}
	}
	for range reTailnetName.FindAllString(line, -1) {
		out = append(out, "tailnet-address")
	}
	for _, m := range reHomePath.FindAllStringSubmatch(line, -1) {
		if user := m[1] + m[2]; !genericHomeUsers[strings.ToLower(user)] {
			out = append(out, "home-path")
		}
	}
	scrubbed := rePosixClass.ReplaceAllString(line, "        ")
	scrubbed = blankGroup(scrubbed, reOwnIdentity, 2)
	if rel == contactDoc {
		scrubbed = blankGroup(scrubbed, reContact, 2)
	}
	out = append(out, extractTokensFromText(scrubbed)...)
	return out
}

// textScanFile is one text file to scan.
type textScanFile struct {
	Rel string
	Src []byte
}

func isTextScanned(rel string) bool {
	if strings.HasSuffix(rel, ".go") {
		return false
	}
	base := path.Base(rel)
	if textScanNames[base] {
		return true
	}
	for _, s := range textScanSuffixes {
		if strings.HasSuffix(rel, s) {
			return true
		}
	}
	return false
}

// measureTextGenerality counts findings per `path:token`.
func measureTextGenerality(files []textScanFile) map[string]int {
	counts := map[string]int{}
	for _, f := range files {
		for _, line := range strings.Split(string(f.Src), "\n") {
			for _, tok := range generalityTextFindings(f.Rel, line) {
				counts[f.Rel+":"+tok]++
			}
		}
	}
	return counts
}

func fileOfKey(key string) string {
	if i := strings.LastIndex(key, ":"); i >= 0 {
		return key[:i]
	}
	return key
}

// checkTextGenerality returns every violation of the two lists over the files.
func checkTextGenerality(files []textScanFile, fixtures, debt *allowlist.List) []string {
	var v []string
	counts := measureTextGenerality(files)

	// Fixture rows: a whole file, with a reason.
	fixtureFile := map[string]bool{}
	for _, row := range fixtures.Rows() {
		fields := strings.Fields(row.Text)
		if len(fields) < 2 || len(strings.Join(fields[1:], " ")) < 12 {
			v = append(v, fmt.Sprintf("%s:%d: fixture row %q names no reason; write `path reason`", fixtures.Path, row.Line, row.Text))
			continue
		}
		fixtureFile[fields[0]] = true
	}
	hit := map[string]bool{}
	for key := range counts {
		hit[fileOfKey(key)] = true
	}
	for f := range fixtureFile {
		if !hit[f] {
			v = append(v, fmt.Sprintf("%s lists %s, but it has no finding any more; delete the stale row (the list only shrinks)", fixtures.Path, f))
		}
	}

	// Debt rows: path:token count.
	allowed := map[string]int{}
	for _, row := range debt.Rows() {
		fields := strings.Fields(row.Text)
		if len(fields) != 2 {
			v = append(v, fmt.Sprintf("%s:%d: malformed row %q: expected <file:token> <count>", debt.Path, row.Line, row.Text))
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || n <= 0 {
			v = append(v, fmt.Sprintf("%s:%d: invalid count in row %q: expected a positive integer", debt.Path, row.Line, row.Text))
			continue
		}
		if fixtureFile[fileOfKey(fields[0])] {
			v = append(v, fmt.Sprintf("%s:%d: %s is a fixture (%s); it has no debt row", debt.Path, row.Line, fileOfKey(fields[0]), fixtures.Path))
		}
		allowed[fields[0]] = n
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if fixtureFile[fileOfKey(key)] {
			continue
		}
		n := counts[key]
		tok := key[strings.LastIndex(key, ":")+1:]
		switch a, ok := allowed[key]; {
		case !ok:
			v = append(v, fmt.Sprintf("%s: %d forbidden reference(s) to %q in a living text file (generality: no host, machine, tailnet, friend or person name; use a placeholder or a config lookup, a generic name in a doc example; docs/SPEC-CI.md#generality-text)", fileOfKey(key), n, tok))
		case n > a:
			v = append(v, fmt.Sprintf("%s: %d occurrences of %q exceeds allowed count %d (the list only shrinks)", fileOfKey(key), n, tok, a))
		case n < a:
			v = append(v, fmt.Sprintf("%s: %d occurrences of %q is below allowed count %d; shrink the row in %s (the list only shrinks)", fileOfKey(key), n, tok, a, debt.Path))
		}
	}
	for key := range allowed {
		if counts[key] == 0 {
			v = append(v, fmt.Sprintf("%s lists %s, but no reference is in the living tree any more; delete the stale row (the list only shrinks)", debt.Path, key))
		}
	}
	if n, ok := debt.Ceiling(); ok && len(debt.Rows()) > n {
		v = append(v, fmt.Sprintf("%s has %d rows, over its ceiling of %d; the list only shrinks", debt.Path, len(debt.Rows()), n))
	}
	if n, ok := fixtures.Ceiling(); ok && len(fixtures.Rows()) > n {
		v = append(v, fmt.Sprintf("%s has %d rows, over its ceiling of %d; the list only shrinks", fixtures.Path, len(fixtures.Rows()), n))
	}
	sort.Strings(v)
	return v
}

func livingTextFiles(t *testing.T) []textScanFile {
	t.Helper()
	tree := repoTree(t)
	var files []textScanFile
	for _, f := range tree.Files {
		if !isTextScanned(f.Rel) {
			continue
		}
		src := f.Src
		if src == nil {
			raw, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatalf("reading %s: %v", f.Rel, err)
			}
			src = raw
		}
		files = append(files, textScanFile{Rel: f.Rel, Src: src})
	}
	return files
}

// TestGeneralityText holds every living non-Go text file to the generality rule:
// the names of generality_class_test.go, tailnet addresses and home paths.
func TestGeneralityText(t *testing.T) {
	t.Parallel()

	files := livingTextFiles(t)
	if allowlist.Updating() {
		skip := map[string]bool{}
		for _, row := range loadAllowlist(t, generalityTextFixturesPath, shrinkOnly).Rows() {
			skip[strings.Fields(row.Text)[0]] = true
		}
		counts := map[string]int{}
		for k, n := range measureTextGenerality(files) {
			if !skip[fileOfKey(k)] {
				counts[k] = n
			}
		}
		p := filepath.Join(repoTree(t).Root, "internal/ci", generalityTextDebtPath)
		if err := writeGeneralityAllowlist(p, counts); err != nil {
			t.Fatalf("failed to rewrite the text generality list: %v", err)
		}
		t.Fatal(allowlist.UpdatedRerun)
	}
	fixtures := loadAllowlist(t, generalityTextFixturesPath, shrinkOnly)
	debt := loadAllowlist(t, generalityTextDebtPath, shrinkOnly)
	for _, v := range checkTextGenerality(files, fixtures, debt) {
		t.Error(v)
	}
}

// TestGeneralityTextFindings pins what a line's findings are: the names, the three
// tailnet shapes, the home paths, and what is not a finding.
func TestGeneralityTextFindings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line string
		want []string
	}{
		{"store: 100.101.102.103:6379", []string{"tailnet-address"}},
		{"--bind 127.0.0.1,100.64.0.1", []string{"tailnet-address"}},
		{"addr 100.127.255.254", []string{"tailnet-address"}},
		{"range 100.64.0.0/10 and 127.0.0.1", nil},
		{"100.63.1.1 and 100.128.1.1 are public", nil},
		{"version 1.100.64.5.9 is not an address", nil},
		{"fd7a:115c:a1e0::1", []string{"tailnet-address"}},
		{"the prefix fd7a:115c:a1e0::/48", nil},
		{"store at redis.tail1234.ts.net:6379", []string{"tailnet-address"}},
		{"ssh someone-else@host:/home/someone-else/x", []string{"home-path"}},
		{"HOME=/Users/someone-else/x", []string{"home-path"}},
		{`C:\Users\someone-else\bin`, []string{"home-path"}},
		{"key /Users/rowan/.config/k", []string{"home-path", "rowan"}},
		{"path /home/example/x /Users/you/y /home/runner/work", nil},
		{`HOME=/home/someone-else`, []string{"home-path"}},
		{"$TMP/nopro/home/some-dir/x and ~/home/dir", nil},
		{`C:\Users\runneradmin\AppData`, nil},
		{"HOME=/home/card", nil},
		{"--seat studio --as rowan", []string{"rowan", "studio"}},
		{"sed -e 's/[[:space:]]*$//'", nil},
		{"go install github.com/mas-bandwidth/nova-tools/cmd/x@latest", nil},
		{"see mas-bandwidth/nova-tools#4339", nil},
		{"see mas-bandwidth/ideas#12", []string{"mas-bandwidth"}},
		{"[seed](github.com/mas-bandwidth/nova) and [sec](github.com/mas-bandwidth/nova/blob/main/SECURITY.md)", nil},
		{"the store `mas-bandwidth/secrets`, and repos/mas-bandwidth/secrets/collaborators", nil},
		{"mas-bandwidth/nova-tools-x and mas-bandwidth/novax and mas-bandwidth/secrets2", []string{"mas-bandwidth", "mas-bandwidth", "mas-bandwidth"}},
		{"mas-bandwidth/nova mas-bandwidth/nova-tools", nil},
		{`{"Output":"FAIL\tgithub.com/mas-bandwidth/nova-tools/cmd/x"}`, nil},
		{"other/mas-bandwidth/nova and xgithub.com/mas-bandwidth/nova and other/github.com/mas-bandwidth/nova", []string{"mas-bandwidth", "mas-bandwidth", "mas-bandwidth"}},
		{"gh api repos/mas-bandwidth/secrets/collaborators and (mas-bandwidth/nova)", nil},
		{"mail ada@mas-bandwidth.com", []string{"mas-bandwidth"}},
		{"in a namespace, on miniredis, in whitespace", nil},
		{"the swarm-hulk seat", []string{"hulk"}},
	}
	for _, tc := range cases {
		got := generalityTextFindings("docs/x.md", tc.line)
		sort.Strings(got)
		want := append([]string(nil), tc.want...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("generalityTextFindings(%q) = %v, want %v", tc.line, got, want)
		}
	}
}

// TestGeneralityTextContactDoc: the project's contact addresses pass in the one document
// that publishes them and nowhere else.
func TestGeneralityTextContactDoc(t *testing.T) {
	t.Parallel()

	line := "Email <glenn@mas-bandwidth.com>."
	if got := generalityTextFindings(contactDoc, line); len(got) != 0 {
		t.Errorf("%s: %v, want none", contactDoc, got)
	}
	got := generalityTextFindings("docs/CLI.md", line)
	sort.Strings(got)
	if strings.Join(got, ",") != "glenn,mas-bandwidth" {
		t.Errorf("elsewhere: %v, want the name and the account", got)
	}
	if got := generalityTextFindings(contactDoc, "ask rowan or mas-bandwidth/ideas"); len(got) != 2 {
		t.Errorf("%s: a name and another repository still count, got %v", contactDoc, got)
	}
	// Only the two published addresses pass, whole.
	for _, l := range []string{"mail ada@mas-bandwidth.com", "mail xglenn@mas-bandwidth.com", "mail glenn@mas-bandwidth.com.evil"} {
		if got := generalityTextFindings(contactDoc, l); len(got) == 0 {
			t.Errorf("%s: %q passed", contactDoc, l)
		}
	}
	if got := generalityTextFindings(contactDoc, "to <rowan@mas-bandwidth.com>, <glenn@mas-bandwidth.com>"); len(got) != 0 {
		t.Errorf("%s: both published addresses: %v", contactDoc, got)
	}
}

// TestGeneralityTextScope pins which files the text scan reads.
func TestGeneralityTextScope(t *testing.T) {
	t.Parallel()

	for rel, want := range map[string]bool{
		"fleet/machines.tsv":                            true,
		"fleet/templates/unit.service.j2":               true,
		"fleet/loops.yml":                               true,
		".github/workflows/ci.yml":                      true,
		"docs/CLI.md":                                   true,
		"Makefile":                                      true,
		"tools/run.sh":                                  true,
		"internal/nsprint/fn/lua/x.lua":                 true,
		"internal/x/testdata/case.json":                 true,
		"infra/image/Containerfile":                     true,
		"internal/x/code.go":                            false,
		"assets/logo.png":                               false,
		"go.sum":                                        false,
		"fleet/inventory.container-runtime.example.ini": true,
	} {
		if got := isTextScanned(rel); got != want {
			t.Errorf("isTextScanned(%q) = %v, want %v", rel, got, want)
		}
	}
}

// TestGeneralityTextWitness is the reversed witness: a new finding in a file nobody
// listed fails, a second one in a listed file fails, a fixture row needs a reason, and
// a stale row fails. Each control passes first.
func TestGeneralityTextWitness(t *testing.T) {
	t.Parallel()

	parse := func(name, text string) *allowlist.List {
		l, err := allowlist.Parse(name, text, allowlist.Options{Ceiling: true})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	empty := parse("debt", "# ceiling: 0\n")
	noFixtures := parse("fixtures", "# ceiling: 0\n")
	file := func(rel, src string) []textScanFile { return []textScanFile{{Rel: rel, Src: []byte(src)}} }

	t.Run("new-finding-in-a-yml-fails", func(t *testing.T) {
		if v := checkTextGenerality(file("fleet/a.yml", "store: redis\n"), noFixtures, empty); len(v) != 0 {
			t.Fatalf("control: %v", v)
		}
		for _, src := range []string{"store: 100.101.102.103\n", "user: rowan\n", "home: /Users/somebody/x\n"} {
			if v := checkTextGenerality(file("fleet/a.yml", src), noFixtures, empty); len(v) == 0 {
				t.Errorf("%q passed", src)
			}
		}
	})
	t.Run("new-finding-in-a-tsv-and-a-template-fails", func(t *testing.T) {
		for _, rel := range []string{"fleet/m.tsv", "fleet/t/x.j2", "docs/A.md", "x.lua", "x.sh", "Makefile", ".github/workflows/w.yml"} {
			if v := checkTextGenerality(file(rel, "host\tstudio\n"), noFixtures, empty); len(v) == 0 {
				t.Errorf("%s passed", rel)
			}
		}
	})
	t.Run("second-occurrence-fails", func(t *testing.T) {
		debt := parse("debt", "# ceiling: 1\nfleet/a.yml:studio 1\n")
		if v := checkTextGenerality(file("fleet/a.yml", "a: studio\n"), noFixtures, debt); len(v) != 0 {
			t.Fatalf("control: %v", v)
		}
		v := checkTextGenerality(file("fleet/a.yml", "a: studio\nb: studio\n"), noFixtures, debt)
		if len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), "exceeds allowed count 1") {
			t.Errorf("violations %v", v)
		}
	})
	t.Run("a-fixture-row-needs-a-reason-and-a-finding", func(t *testing.T) {
		src := file("testdata/t.md", "transcript of studio\n")
		ok := parse("fixtures", "# ceiling: 1\ntestdata/t.md a captured transcript, recorded data\n")
		if v := checkTextGenerality(src, ok, empty); len(v) != 0 {
			t.Fatalf("control: %v", v)
		}
		bare := parse("fixtures", "# ceiling: 1\ntestdata/t.md\n")
		if v := checkTextGenerality(src, bare, empty); len(v) == 0 {
			t.Error("a fixture row with no reason passed")
		}
		stale := file("testdata/t.md", "nothing here\n")
		if v := checkTextGenerality(stale, ok, empty); len(v) == 0 {
			t.Error("a stale fixture row passed")
		}
	})
	t.Run("a-row-that-falls-or-goes-stale-must-shrink", func(t *testing.T) {
		debt := parse("debt", "# ceiling: 1\nfleet/a.yml:studio 2\n")
		if v := checkTextGenerality(file("fleet/a.yml", "a: studio\n"), noFixtures, debt); len(v) == 0 {
			t.Error("a fallen count passed")
		}
		if v := checkTextGenerality(file("fleet/a.yml", "a: none\n"), noFixtures, debt); len(v) == 0 {
			t.Error("a stale row passed")
		}
	})
}
