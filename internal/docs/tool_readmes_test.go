package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flagshipReadmeTool is the one tool whose standalone guide is not under cmd/:
// nova-sprint's is the sprint guide card (docs/sprint/README.md), so this card
// writes none for it and the scan below skips it.
const flagshipReadmeTool = "nova-sprint"

// shimTools are the one-release compatibility shims: a directory under cmd/
// whose binary is a passthrough to another tool, not a tool of its own. It has
// no verbs, no banner and no docs/CLI.md section of its own, so the six-section
// guide and the release catalogue's row are the tool's (nova-worker's), not the
// shim's. nova-swarm is the v1.3 shim for nova-worker and goes in the release
// after it (cmd/nova-swarm/README.md).
var shimTools = map[string]bool{"nova-swarm": true}

// readmeRequiredSections are the sections a tool's standalone README must carry:
// what the tool is, why a reader uses it, the one install line, a first run the
// docs tests execute, its verbs linked to its docs/CLI.md section, and its spec.
var readmeRequiredSections = []string{"What it is", "Why use it", "Install", "First run", "Verbs", "Spec"}

// readmeLinkRe is the narrow inline-link form `](dest)`, the same shape the
// top-level link checker reads.
var readmeLinkRe = regexp.MustCompile(`\]\(([^)\s]+)(?:\s[^)]*)?\)`)

// readmeAnchorStrip drops the characters GitHub removes from a heading when it
// makes the heading's anchor.
var readmeAnchorStrip = regexp.MustCompile(`[^a-z0-9 _-]`)

// readmeAnchor is the anchor GitHub gives a heading.
func readmeAnchor(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = strings.ReplaceAll(s, " ", "-")
	return readmeAnchorStrip.ReplaceAllString(s, "")
}

// mdSection returns the body of a `## <name>` section up to the next `## `.
func mdSection(md, name string) (string, bool) {
	_, tail, found := strings.Cut("\n"+md, "\n## "+name+"\n")
	if !found {
		return "", false
	}
	body, _, _ := strings.Cut(tail, "\n## ")
	return body, true
}

// TestEveryToolHasAStandaloneReadme refuses a tool directory with no README, a
// README missing one of its six sections, or a relative link that does not
// resolve (file or anchor). The top README must link every tool's README.
func TestEveryToolHasAStandaloneReadme(t *testing.T) {
	t.Parallel()

	root := testRoot(t)

	cmdEntries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)

	cliRaw, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	require.NoError(t, err)
	cli := string(cliRaw)

	var tools []string
	for _, e := range cmdEntries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	sort.Strings(tools)
	require.NotEmpty(t, tools, "no tools under cmd/")

	for _, tool := range tools {
		tool := tool
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			if tool == flagshipReadmeTool || shimTools[tool] {
				return
			}
			rel := filepath.Join("cmd", tool, "README.md")
			path := filepath.Join(root, rel)
			raw, err := os.ReadFile(path)
			if !assert.NoError(t, err, "%s: every tool needs a standalone README", rel) {
				return
			}
			md := string(raw)

			for _, section := range readmeRequiredSections {
				body, ok := mdSection(md, section)
				assert.True(t, ok, "%s: missing ## %s section", rel, section)
				assert.NotEmpty(t, strings.TrimSpace(body), "%s: ## %s is empty", rel, section)
			}

			if body, ok := mdSection(md, "What it is"); ok {
				assert.Contains(t, body, tool, "%s: ## What it is does not name %s", rel, tool)
			}

			assert.Contains(t, md, "go install github.com/mas-bandwidth/nova-tools/cmd/"+tool+"@",
				"%s: ## Install does not carry the one install line for %s", rel, tool)

			if body, ok := mdSection(md, "First run"); ok {
				assert.Contains(t, body, "$ "+tool+" ", "%s: ## First run has no `$ %s ...` transcript line", rel, tool)
				assert.Contains(t, body, "```", "%s: ## First run has no fenced transcript", rel)
			}

			if body, ok := mdSection(md, "Verbs"); ok {
				if strings.Contains(cli, "\n## "+tool+"\n") {
					assert.Contains(t, body, "docs/CLI.md#"+tool, "%s: ## Verbs does not link to docs/CLI.md#%s", rel, tool)
				} else {
					assert.Regexp(t, `docs/[A-Za-z0-9-]+\.md`, body, "%s: ## Verbs links no reference document", rel)
				}
			}

			if body, ok := mdSection(md, "Spec"); ok {
				assert.Regexp(t, `docs/SPEC[A-Z0-9-]*\.md`, body, "%s: ## Spec names no docs/SPEC*.md", rel)
			}

			checkReadmeLinks(t, path, md)
		})
	}

	topRaw, err := os.ReadFile(filepath.Join(root, "README.md"))
	require.NoError(t, err)
	top := string(topRaw)
	for _, tool := range tools {
		if tool == flagshipReadmeTool || shimTools[tool] {
			continue
		}
		assert.Contains(t, top, "cmd/"+tool+"/README.md", "README.md does not link cmd/%s/README.md", tool)
	}
}

// checkReadmeLinks holds every relative link in one README against the tree: the
// target file must exist, and a `#anchor` must name a heading in the target.
func checkReadmeLinks(t *testing.T, path, md string) {
	t.Helper()
	dir := filepath.Dir(path)
	inFence := false
	for i, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = inlineCodeRe.ReplaceAllString(line, "")
		for _, m := range readmeLinkRe.FindAllStringSubmatch(line, -1) {
			written := m[1]
			target, anchor := written, ""
			if cut := strings.IndexByte(target, '#'); cut >= 0 {
				anchor = target[cut+1:]
				target = target[:cut]
			}
			if target == "" || strings.HasPrefix(target, "http://") ||
				strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			resolved := filepath.Join(dir, target)
			if _, err := os.Stat(resolved); !assert.NoError(t, err, "%s:%d: link %q does not resolve to %s", path, i+1, written, resolved) {
				continue
			}
			if anchor == "" || !strings.HasSuffix(resolved, ".md") {
				continue
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				continue
			}
			var anchors []string
			for _, l := range strings.Split(string(data), "\n") {
				for _, prefix := range []string{"## ", "### "} {
					if h, ok := strings.CutPrefix(l, prefix); ok {
						anchors = append(anchors, readmeAnchor(h))
					}
				}
			}
			assert.Contains(t, anchors, anchor, "%s:%d: anchor #%s does not resolve in %s", path, i+1, anchor, resolved)
		}
	}
}

// TestToolReadmeTranscriptsAreTheTestedOnes holds each README's first run to the
// record it copies: a README whose ## First run links docs/TESTS.md, which each
// tool's firstrun_test.go executes, shows only lines that record carries, so no
// README shows output a test never produced.
func TestToolReadmeTranscriptsAreTheTestedOnes(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "TESTS.md"))
	require.NoError(t, err)
	tested := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		tested[l] = true
	}
	readmes, err := filepath.Glob(filepath.Join(root, "cmd", "*", "README.md"))
	require.NoError(t, err)
	require.NotEmpty(t, readmes, "no cmd/*/README.md found")
	for _, path := range readmes {
		md, err := os.ReadFile(path)
		require.NoError(t, err)
		body, ok := mdSection(string(md), "First run")
		if !ok || !strings.Contains(body, "docs/TESTS.md") {
			continue
		}
		inFence := false
		for _, l := range strings.Split(body, "\n") {
			if strings.HasPrefix(l, "```") {
				inFence = !inFence
				continue
			}
			if inFence && strings.TrimSpace(l) != "" {
				assert.True(t, tested[l], "%s: ## First run shows %q, which docs/TESTS.md does not carry; copy the tested line", path, l)
			}
		}
	}
}
