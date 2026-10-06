package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// docs_ci_class_test.go is the door the documentation gates hang in: a workflow
// that runs `make docs-check` on every pull request and every push that touches
// docs, a tool's README or a verb's help text, and the Makefile target that holds
// the gates themselves.
//
// Docs break quietly. The guards that read them live in internal/docs, and the
// sharded test job runs a package's tests only when a Go package changed: an edit
// to a markdown file or to a banner leaves a broken link, a stale command
// reference or a retired word in the tree with nothing saying NO. This workflow is
// the thing that says NO.
//
// It reads the workflow as YAML and the Makefile as parsed text (parseMakefile in
// makefile_test.go) and runs neither make nor a gate: the contract is the text a
// reviewer reads, and its answer must not move with the host's make version. No
// socket is opened and no wall clock is waited on.
//
// The shape held here is the owner's rule of 2026-10-04, no bash in anything that
// ships (docs/SPEC-CI.md, `no-shell-ships`): every step is one command of a Go
// tool or make and carries no shell control flow, so the logic of a gate lives in
// a Go tool or a test and the workflow only names it.

const (
	// docsWorkflowPath is the workflow the documentation gates live in.
	docsWorkflowPath = ".github/workflows/docs.yml"
	// docsCheckTarget is the Makefile target it runs: the Makefile is the one
	// entry, so the workflow names a target and never a gate of its own.
	docsCheckTarget = "docs-check"
	// docsGateStep is the one command the workflow's gate step runs.
	docsGateStep = "make docs-check"
)

// docsTriggerPaths are the paths a change must touch for the docs gates to run:
// the documentation itself, a tool's README, the help text a banner is built from,
// and the generator of the command reference.
var docsTriggerPaths = []string{"docs/**", "**/README.md", "cmd/*/verbhelp.go", "tools/clidoc/**"}

// docsGate is one gate the documentation is held to, the command that runs it in
// the docs-check target, and what is lost when the line goes.
type docsGate struct {
	name   string
	cmd    string
	remedy string
}

// docsGates is every gate the documentation carries. The command strings are the
// Makefile's own text, `$(GO)` and all: a target that runs something else, or runs
// one of these without naming it here, is a finding.
var docsGates = []docsGate{
	{
		name:   "the internal/docs tests",
		cmd:    "$(GO) test -count=1 -timeout $(DOCS_TIMEOUT) ./internal/docs",
		remedy: "the docs package's tests read the specs, the maps and the transcripts",
	},
	{
		name:   "the generated-CLI check",
		cmd:    "$(GO) test -count=1 -timeout $(DOCS_TIMEOUT) -run 'TestCLIReference|TestTheCLIReference' ./internal/docs",
		remedy: "docs/CLI.md is the reference a stranger copies from, so it is checked against what the tools print",
	},
	{
		name:   "the terminology lint",
		cmd:    "$(GO) test -count=1 -timeout $(DOCS_TIMEOUT) -run 'TestRetiredWordsAppearOnlyInRecords|TestGlossariesDefineEveryTermWithItsSection' ./internal/docs",
		remedy: "a retired word in a living document, or a glossary term without its section, is a finding",
	},
	{
		name:   "the link check over the tree",
		cmd:    `$(GO) run ./cmd/nova-check links --dir "$(CURDIR)"`,
		remedy: "a relative link that does not resolve inside the tree is a finding",
	},
}

// docsWorkflow is .github/workflows/docs.yml as far as this rule reads it.
type docsWorkflow struct {
	On struct {
		PullRequest struct {
			Paths []string `yaml:"paths"`
		} `yaml:"pull_request"`
		Push struct {
			Paths []string `yaml:"paths"`
		} `yaml:"push"`
	} `yaml:"on"`
	Jobs map[string]docsJob `yaml:"jobs"`
}

// docsJob is one job of that workflow.
type docsJob struct {
	TimeoutMinutes int        `yaml:"timeout-minutes"`
	Steps          []docsStep `yaml:"steps"`
}

// docsStep is one step of that job.
type docsStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

func parseDocsWorkflow(t *testing.T, src string) docsWorkflow {
	t.Helper()
	var wf docsWorkflow
	require.NoError(t, yaml.Unmarshal([]byte(src), &wf), "the docs workflow does not parse; this rule cannot read its steps")
	return wf
}

// inList is whether needle is one of the strings in haystack.
func inList(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// makeVarRefRe is one make variable reference, $(NAME) or ${NAME}. In a Makefile
// recipe line it is make's own expansion; in a workflow step a `$(` of any kind is
// a command substitution and stays a finding.
var makeVarRefRe = regexp.MustCompile(`\$(?:\(|\{)[A-Za-z_][A-Za-z0-9_]*(?:\)|\})`)

// quotedSpanRe is one single- or double-quoted span, whose contents are one
// argument and never shell: the `-run 'TestA|TestB'` alternation is a test name.
var quotedSpanRe = regexp.MustCompile(`'[^']*'|"[^"]*"`)

// shellLogic is every shell shape a command of a Go tool or make has no reason to
// carry. Each is named so a finding says which one to take out.
var shellLogic = []struct {
	re    *regexp.Regexp
	shape string
}{
	{regexp.MustCompile(`&&`), "`&&`"},
	{regexp.MustCompile(`\|\|`), "`||`"},
	{regexp.MustCompile("`"), "a backtick"},
	{regexp.MustCompile(`[;&]`), "`;` or `&`"},
	{regexp.MustCompile(`\|`), "a pipe"},
	{regexp.MustCompile(`\$\(`), "a command substitution"},
	{regexp.MustCompile(`\b(if|then|elif|else|fi|for|while|until|do|done|case|esac|set|export|local|source|eval|exec)\b`), "a shell control word"},
}

// docsCommandFinding names what keeps one line from being a single command of the
// entrypoints it allows: a shell shape it carries, or a first word off the list.
// dropMakeVars is true for a Makefile recipe line, where $(GO) is make's expansion
// and not a substitution the recipe shell performs.
func docsCommandFinding(where, cmd string, entrypoints []string, dropMakeVars bool) string {
	text := cmd
	if dropMakeVars {
		text = makeVarRefRe.ReplaceAllString(text, "x")
	}
	text = quotedSpanRe.ReplaceAllString(text, " ")
	for _, s := range shellLogic {
		if s.re.MatchString(text) {
			return fmt.Sprintf("%s carries shell logic (%s): %q; one command of a Go tool or make, no shell", where, s.shape, cmd)
		}
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 || !inList(entrypoints, fields[0]) {
		return fmt.Sprintf("%s is not one command of %s: %q", where, strings.Join(entrypoints, " or "), cmd)
	}
	return ""
}

// makeEntrypoints are how a Makefile recipe line begins: the toolchain variable the
// file declares, or make itself for a target that calls a target.
var makeEntrypoints = []string{"go", "$(GO)", "make", "$(MAKE)"}

// docsWorkflowFindings is what is wrong with a docs workflow: a trigger whose paths
// are not the docs set, a job over the CL cap or uncapped, a step that is not one
// command of make, and no step at all running the docs gate target.
func docsWorkflowFindings(wf docsWorkflow) []string {
	var out []string
	for _, event := range []string{"pull_request", "push"} {
		paths := wf.On.PullRequest.Paths
		if event == "push" {
			paths = wf.On.Push.Paths
		}
		for _, want := range docsTriggerPaths {
			if !inList(paths, want) {
				out = append(out, fmt.Sprintf("docs.yml's %s trigger does not name %q; the docs gates must run on a change under it", event, want))
			}
		}
		for _, got := range paths {
			if !inList(docsTriggerPaths, got) {
				out = append(out, fmt.Sprintf("docs.yml's %s trigger names %q, which is no docs path of this rule", event, got))
			}
		}
	}
	sawGate := false
	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		job := wf.Jobs[name]
		where := fmt.Sprintf("docs.yml job %q", name)
		if job.TimeoutMinutes <= 0 {
			out = append(out, fmt.Sprintf("%s declares no timeout-minutes; the CL budget is %d", where, twoMinuteCap))
		} else if job.TimeoutMinutes > twoMinuteCap {
			out = append(out, fmt.Sprintf("%s has timeout-minutes %d, over the CL cap of %d", where, job.TimeoutMinutes, twoMinuteCap))
		}
		for _, s := range job.Steps {
			if strings.TrimSpace(s.Run) == "" {
				continue
			}
			stepWhere := fmt.Sprintf("%s step %q", where, s.Name)
			code := 0
			for _, line := range strings.Split(s.Run, "\n") {
				t := strings.TrimSpace(line)
				if t == "" || strings.HasPrefix(t, "#") {
					continue
				}
				code++
				if code > 1 {
					continue
				}
				if f := docsCommandFinding(stepWhere, t, []string{"make"}, false); f != "" {
					out = append(out, f)
				}
			}
			if code != 1 {
				out = append(out, fmt.Sprintf("%s runs %d commands; a step is one command of a Go tool or make", stepWhere, code))
			}
			if strings.TrimSpace(s.Run) == docsGateStep {
				sawGate = true
			}
		}
	}
	if !sawGate {
		out = append(out, fmt.Sprintf("docs.yml runs no %q step; the Makefile is the one entry for the docs gates", docsGateStep))
	}
	return out
}

// docsMakeFindings is what is wrong with the Makefile's docs-check target: no
// target, no .PHONY, a gate missing from it, a command in it that names no gate,
// and any line that is not one command of a Go tool or make.
func docsMakeFindings(mk *parsedMakefile) []string {
	var out []string
	recipe := mk.recipes[docsCheckTarget]
	if len(recipe) == 0 {
		return append(out, fmt.Sprintf("Makefile declares no %q target; docs.yml runs it, so the docs gates run nowhere", docsCheckTarget))
	}
	if !mk.phony[docsCheckTarget] {
		out = append(out, fmt.Sprintf("Makefile .PHONY does not name %s", docsCheckTarget))
	}
	wanted := make([]string, 0, len(docsGates))
	for _, g := range docsGates {
		wanted = append(wanted, g.cmd)
		if !inList(recipe, g.cmd) {
			out = append(out, fmt.Sprintf("Makefile %s does not run %s; add %q: %s", docsCheckTarget, g.name, g.cmd, g.remedy))
		}
	}
	for _, line := range recipe {
		if !inList(wanted, line) {
			out = append(out, fmt.Sprintf("Makefile %s runs %q, which names no docs gate of this rule; a new gate is a gate here too", docsCheckTarget, line))
		}
		if f := docsCommandFinding(fmt.Sprintf("Makefile %s", docsCheckTarget), line, makeEntrypoints, true); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange is the docs CI contract: the
// workflow fires on every pull request and every push under the docs paths, one
// step runs `make docs-check`, and that target runs every gate the documentation
// carries, one command each. It is red while a gate is missing from the target,
// while the workflow is missing, and while any step carries shell control flow.
func TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	wf := parseDocsWorkflow(t, readFile(t, filepath.Join(root, docsWorkflowPath)))
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	findings := append(docsWorkflowFindings(wf), docsMakeFindings(mk)...)
	assert.Empty(t, findings, "the documentation gates do not run on every change that touches docs:\n%s", strings.Join(findings, "\n"))
}

// TestDocsGateFindingsNamesAMissingGateOrAShellStep pins the rule on fakes: a
// workflow or a target with one docs gate dropped, with a step of shell logic, or
// with a trigger path missing is refused naming it, and the complete pair is not.
// A test that only reads the tree cannot show that a rule bites.
func TestDocsGateFindingsNamesAMissingGateOrAShellStep(t *testing.T) {
	t.Parallel()

	gateYAML := docsStepsYAML(t, docsGateStep)
	fullRecipe := []string{docsGates[0].cmd, docsGates[1].cmd, docsGates[2].cmd, docsGates[3].cmd}

	cases := []struct {
		name     string
		findings func() []string
		want     string
	}{
		{
			name: "the complete workflow and target are accepted",
			findings: func() []string {
				return append(docsWorkflowFindings(parseDocsWorkflow(t, gateYAML)), docsMakeFindings(fakeDocsMakefile(t, fullRecipe, true))...)
			},
			want: "",
		},
		{
			name: "a missing terminology lint is named",
			findings: func() []string {
				return docsMakeFindings(fakeDocsMakefile(t, []string{fullRecipe[0], fullRecipe[1], fullRecipe[3]}, true))
			},
			want: "does not run the terminology lint",
		},
		{
			name: "a missing generated-CLI check is named",
			findings: func() []string {
				return docsMakeFindings(fakeDocsMakefile(t, []string{fullRecipe[0], fullRecipe[2], fullRecipe[3]}, true))
			},
			want: "does not run the generated-CLI check",
		},
		{
			name:     "a missing link check is named",
			findings: func() []string { return docsMakeFindings(fakeDocsMakefile(t, fullRecipe[:3], true)) },
			want:     "does not run the link check over the tree",
		},
		{
			name:     "a missing docs test gate is named",
			findings: func() []string { return docsMakeFindings(fakeDocsMakefile(t, fullRecipe[1:], true)) },
			want:     "does not run the internal/docs tests",
		},
		{
			name:     "no docs-check target at all is named",
			findings: func() []string { return docsMakeFindings(fakeDocsMakefile(t, nil, true)) },
			want:     "declares no \"docs-check\" target",
		},
		{
			name:     "a target off .PHONY is named",
			findings: func() []string { return docsMakeFindings(fakeDocsMakefile(t, fullRecipe, false)) },
			want:     ".PHONY does not name docs-check",
		},
		{
			name: "a gate rewritten into a shell loop is named",
			findings: func() []string {
				return docsMakeFindings(fakeDocsMakefile(t, append(fullRecipe[:2], "for f in docs/*.md; do $(GO) run ./cmd/nova-check links --dir .; done", fullRecipe[3]), true))
			},
			want: "does not run the terminology lint",
		},
		{
			name: "a shell loop over the docs in a step is named",
			findings: func() []string {
				return docsWorkflowFindings(parseDocsWorkflow(t, docsStepsYAML(t, "for f in docs/*.md; do make docs-check; done")))
			},
			want: "carries shell logic",
		},
		{
			name: "two commands in one step are named",
			findings: func() []string {
				return docsWorkflowFindings(parseDocsWorkflow(t, docsStepsYAML(t, docsGateStep+"\n\techo done")))
			},
			want: "runs 2 commands",
		},
		{
			name: "a step testing the packages itself is named",
			findings: func() []string {
				return docsWorkflowFindings(parseDocsWorkflow(t, docsStepsYAML(t, "go test ./internal/docs")))
			},
			want: "is not one command of make",
		},
		{
			name: "a pipeline in a step is named",
			findings: func() []string {
				return docsWorkflowFindings(parseDocsWorkflow(t, docsStepsYAML(t, "make docs-check | tee out.txt")))
			},
			want: "a pipe",
		},
		{
			name: "a trigger path dropped from both events is named",
			findings: func() []string {
				return docsWorkflowFindings(parseDocsWorkflow(t, strings.Replace(gateYAML, "      - 'cmd/*/verbhelp.go'\n", "", -1)))
			},
			want: "does not name \"cmd/*/verbhelp.go\"",
		},
		{
			name:     "no step running the target is named",
			findings: func() []string { return docsWorkflowFindings(parseDocsWorkflow(t, docsStepsYAML(t, "make help"))) },
			want:     "runs no \"make docs-check\" step",
		},
		{
			name: "a job over the CL cap is named",
			findings: func() []string {
				return docsWorkflowFindings(parseDocsWorkflow(t, strings.Replace(gateYAML, "timeout-minutes: 2", "timeout-minutes: 15", 1)))
			},
			want: "over the CL cap of 2",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := tc.findings()
			if tc.want == "" {
				assert.Empty(t, findings, "the complete workflow and target are refused: %s", strings.Join(findings, "\n"))
				return
			}
			assert.Contains(t, strings.Join(findings, "\n"), tc.want, "the fake is accepted, or refused for something else: %v", findings)
		})
	}
}

// docsStepsYAML is a whole docs workflow whose one gate step runs that command: the
// triggers of the rule, and one job under the CL cap.
func docsStepsYAML(t *testing.T, run string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("on:\n  pull_request:\n    paths:\n")
	for _, p := range docsTriggerPaths {
		fmt.Fprintf(&b, "      - '%s'\n", p)
	}
	b.WriteString("  push:\n    paths:\n")
	for _, p := range docsTriggerPaths {
		fmt.Fprintf(&b, "      - '%s'\n", p)
	}
	b.WriteString("jobs:\n  docs:\n    timeout-minutes: 2\n    steps:\n      - name: gate\n        run: |\n")
	for _, line := range strings.Split(run, "\n") {
		fmt.Fprintf(&b, "          %s\n", strings.ReplaceAll(line, "\t", "    "))
	}
	return b.String()
}

// fakeDocsMakefile parses one generated Makefile from recipe lines, so a witness
// shows what the real target with one gate dropped does: the finding, not a guess.
func fakeDocsMakefile(t *testing.T, recipe []string, phony bool) *parsedMakefile {
	t.Helper()
	var b strings.Builder
	b.WriteString("GO ?= go\nDOCS_TIMEOUT ?= 110s\n")
	if phony {
		b.WriteString(".PHONY: docs-check\n")
	}
	b.WriteString("docs-check:\n")
	for _, line := range recipe {
		b.WriteString("\t" + line + "\n")
	}
	path := filepath.Join(t.TempDir(), "Makefile")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	return parseMakefile(t, path)
}
