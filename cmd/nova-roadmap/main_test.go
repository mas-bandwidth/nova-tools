package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type result struct {
	code           int
	stdout, stderr string
}

// call runs the tool with dir as its working directory.
func call(dir string, args ...string) result {
	var out, errb bytes.Buffer
	code := run(dir, args, &out, &errb)
	return result{code, out.String(), errb.String()}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// The banner is the manual: a bare run and help print it, and it names every
// verb, the form topic and the exit codes.
func TestBanner(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		r := call(t.TempDir(), args...)
		require.Equal(t, 0, r.code, args)
		for _, want := range []string{"example", "render", "check", "version", "help form", "exit codes", "<!-- nova-work:<area id>:start -->"} {
			require.Contains(t, r.stdout, want, args)
		}
	}
	for _, verb := range []string{"example", "render", "check"} {
		r := call(t.TempDir(), "help", verb)
		require.Equal(t, 0, r.code)
		require.True(t, strings.HasPrefix(r.stdout, "usage: nova-roadmap "+verb), r.stdout)
		require.Equal(t, r, call(t.TempDir(), verb, "-h"), "-h prints the same help")
	}
	require.Contains(t, call("", "version").stdout, "nova-roadmap ")
}

// The worked example in `help form` is real data: it decodes, and renders the
// table the topic shows.
func TestHelpFormExampleIsRealData(t *testing.T) {
	t.Parallel()
	r := call("", "help", "form")
	require.Equal(t, 0, r.code)
	start := strings.Index(r.stdout, "(:schema 1")
	require.GreaterOrEqual(t, start, 0)
	end := strings.Index(r.stdout[start:], "\n\n")
	require.Positive(t, end)
	data := strings.ReplaceAll(r.stdout[start:start+end], "\n  ", "\n")
	rm, err := Decode("help", []byte(data))
	require.NoError(t, err)
	page, err := rm.Starter("docs/roadmap.sexp")
	require.NoError(t, err)
	table := "| feature | done |\n|---|:---:|\n| Connect tokens | ✅ |\n| IPv6 (later) | ❌ |\n| complete | 50% |\n\n[Source data](docs/roadmap.sexp)\n"
	require.Contains(t, page, table)
	require.Contains(t, r.stdout, strings.ReplaceAll("  "+strings.ReplaceAll(strings.TrimSuffix(table, "\n"), "\n", "\n  "), "\n  \n", "\n\n"), "the topic shows the table the data renders")
	require.Contains(t, page, "| feature | Go | C# |\n|---|:---:|:---:|\n| Connect | ✅ | ❌ |\n| Send and receive | ❌ | ❌ |\n| complete | 50% | 0% |\n")
}

// The banner's example block is a first run that works, in an empty directory:
// every line exits 0, and it leaves a page that check passes.
func TestFirstRunFromTheBanner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	banner := call(dir).stdout
	block := banner[strings.Index(banner, "example (a first run"):]
	var lines []string
	for _, l := range strings.Split(block, "\n")[1:] {
		if f := strings.Fields(l); len(f) > 1 && f[0] == "nova-roadmap" {
			lines = append(lines, l)
			r := call(dir, f[1:]...)
			require.Equal(t, 0, r.code, "%s: %s%s", l, r.stdout, r.stderr)
		}
	}
	require.Len(t, lines, 3)
	page := readFile(t, filepath.Join(dir, "ROADMAP.md"))
	require.True(t, strings.HasPrefix(page, "# Roadmap\n"))
	require.Contains(t, page, "## Protocol\n\n<!-- nova-work:protocol:start -->\n\n| feature | done |\n")
	require.Contains(t, page, "## Clients\n")
}

// render keeps every hand-written byte; check is the drift gate, 1 after the
// data moves and 0 once render has run again; --dry-run writes nothing.
func TestRenderAndCheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.Equal(t, 0, call(dir, "example").code)
	r := call(dir, "render", "--dry-run")
	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "DRY RUN")
	_, err := os.Stat(filepath.Join(dir, "ROADMAP.md"))
	require.True(t, os.IsNotExist(err), "--dry-run wrote the page")
	r = call(dir, "check")
	require.Equal(t, 1, r.code, "a missing page is drift")
	require.Contains(t, r.stderr, "DRIFT: ROADMAP.md is missing")

	r = call(dir, "render")
	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "wrote a starter ROADMAP.md")
	pagePath := filepath.Join(dir, "ROADMAP.md")
	page := strings.Replace(readFile(t, pagePath), "# Roadmap\n", "# My project\n\nWritten by hand.\n", 1)
	require.NoError(t, os.WriteFile(pagePath, []byte(page), 0o644))
	require.Equal(t, 0, call(dir, "check").code, "hand-written text is not drift")

	ev := filepath.Join(dir, "docs", "roadmap-evidence")
	require.NoError(t, os.MkdirAll(ev, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ev, "ipv6.sexp"), []byte(`(:id "t-ipv6" :state :done :evidence ("ipv6_test.go: TestIPv6"))`+"\n"), 0o644))
	r = call(dir, "check")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "DRIFT: a table in ROADMAP.md differs from what docs/roadmap.sexp renders (areas: protocol); run: nova-roadmap render")
	require.Equal(t, page, readFile(t, pagePath), "check writes nothing")

	r = call(dir, "render")
	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "spliced the tables into ROADMAP.md")
	require.Contains(t, r.stdout, "1 evidence files, 1 entries")
	got := readFile(t, pagePath)
	require.True(t, strings.HasPrefix(got, "# My project\n\nWritten by hand.\n"))
	require.Contains(t, got, "| IPv6 (later) | ✅ |\n| complete | 100% |\n")
	require.Equal(t, HandWritten(page), HandWritten(got))
	require.Equal(t, 0, call(dir, "check").code)
	require.Contains(t, call(dir, "render").stdout, "is up to date")
}

// The [Source data] link is the data as seen from the page, however the paths
// are given.
func TestSourceLinkIsRelativeToThePage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.Equal(t, 0, call(dir, "example").code)
	file, out := filepath.Join(dir, "docs", "roadmap.sexp"), filepath.Join(dir, "ROADMAP.md")
	r := call("", "render", "--file", file, "--out", out)
	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, readFile(t, out), "[Source data](docs/roadmap.sexp)")
	require.Equal(t, 0, call(dir, "check", "--file", "./docs/roadmap.sexp", "--out", "./ROADMAP.md").code)
	require.Equal(t, 0, call(filepath.Join(dir, "docs"), "check", "--file", "roadmap.sexp", "--out", "../ROADMAP.md").code)
}

// Every refusal is exit 2 and names what to fix.
func TestRefusalsExitTwo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.Equal(t, 0, call(dir, "example").code)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "NOMARKERS.md"), []byte("# A page\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.sexp"), []byte("(:schema 2)"), 0o644))
	for args, want := range map[string]string{
		"frobnicate":                                `"frobnicate" is no verb`,
		"render extra":                              "render takes no arguments",
		"render --nope":                             "flag provided but not defined: -nope",
		"check --file missing.sexp":                 "missing.sexp: open ",
		"check --file bad.sexp":                     "want :schema 1",
		"check --evidence no-dir":                   "no-dir is not a directory",
		"render --out NOMARKERS.md":                 "NOMARKERS.md: the page carries marker pairs for []; the data's :page :roadmaps are [\"protocol\" \"clients\"]. Each area needs its own pair of marker lines, each on a line of its own, in this order, wherever its table belongs on the page:\n  <!-- nova-work:protocol:start -->\n  <!-- nova-work:protocol:end -->\n  <!-- nova-work:clients:start -->",
		"example":                                   "docs/roadmap.sexp is there already",
		"help nothing":                              "help nothing: no such topic",
		"render --out NOMARKERS.md --file bad.sexp": "want :schema 1",
	} {
		r := call(dir, strings.Fields(args)...)
		require.Equal(t, 2, r.code, args)
		require.Contains(t, r.stderr, want, args)
		require.Contains(t, r.stderr, "run: nova-roadmap help", args)
	}
	require.Equal(t, "# A page\n", readFile(t, filepath.Join(dir, "NOMARKERS.md")), "a refusal writes nothing")
}

// example --dry-run writes nothing; example makes the directory it needs.
func TestExample(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := call(dir, "example", "--dry-run", "--out", "a/b/roadmap.sexp")
	require.Equal(t, 0, r.code)
	_, err := os.Stat(filepath.Join(dir, "a"))
	require.True(t, os.IsNotExist(err))
	r = call(dir, "example", "--out", "a/b/roadmap.sexp")
	require.Equal(t, 0, r.code, r.stderr)
	require.Equal(t, exampleData, readFile(t, filepath.Join(dir, "a", "b", "roadmap.sexp")))
}
