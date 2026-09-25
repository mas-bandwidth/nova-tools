package ci

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ci_outputs_test.go is the one-line law for step outputs.
//
// $GITHUB_OUTPUT and $GITHUB_ENV are key=value FILES, one pair per line. A value
// that carries an embedded newline is not a long value, it is a second line the
// runner tries to read as another key, and it fails the step with
// "Invalid format '...'". The cost, measured: integration-3's merge group failed
// on ALL EIGHTEEN legs at once (2026-09-18), because the merge gate's package
// selection did `pkgs=$(go list ./...)` on a go.mod change — go list prints one
// package per line — and wrote it straight to $GITHUB_OUTPUT. The step had been
// correct for every group since it was written, because until #1272 (go-redis)
// no group had changed go.mod, so the multi-line branch had never been taken.
// That is the shape worth a class rule: a line that is right until the day its
// input is plural, and then fails everything at once rather than one leg.
//
// #1318 fixed the instance with `| tr '\n' ' '`. This is the class.
//
// THE HEURISTIC, stated plainly, because a workflow test with false positives is
// a test people learn to edit around:
//
//	Inside one `run:` block, a variable becomes MULTI-LINE when it is assigned
//	from a command substitution containing a producer whose output is one item
//	per line — `go list` over a `...` pattern, `git diff --name-only`,
//	`git ls-files`, `find`, `ls`, `cat`, `printf '%s\n'` — and that pipeline
//	carries no single-line guard: `tr`, `paste`, `xargs`, `jq -c`, `head -1`,
//	`tail -1` or `wc -l`. Writing such a variable, or such a substitution
//	directly, to $GITHUB_OUTPUT or $GITHUB_ENV is the violation. A later
//	GUARDED assignment CLEARS the variable — `x=$(... | tr '\n' ' ')` really
//	does normalize it — while a plain one does not, because this reader is
//	linear and a shell script is not: the step that lost the merge group
//	assigned the plural value in one branch of an `if` and `pkgs=""` in the
//	other, and a reader that cleared on any assignment would have called it
//	clean. That is the one place the rule can over-report — a variable made
//	plural, consumed, and then reused for something else — and the remedy is
//	the same guard.
//
// It is deliberately narrow in three places, each of them a false negative
// accepted to keep the false positives at zero:
//
//   - `go list` counts only with a `...` pattern in its arguments. The merge
//     gate's own `p=$(go list "./${d#./}")` asks about ONE package and prints one
//     line, and flagging it would flag a correct step.
//   - A repository script — `x=$(bash .github/scripts/foo.sh)` — is not a
//     producer. Its output shape is the script's business, not this file's, and
//     guessing would flag plan-merge's one-number slots script.
//   - The `key<<EOF` heredoc form, which is the RIGHT way to write a genuinely
//     multi-line output, is allowed; so is a redirect attached to a `{ ... }`
//     block, which this reader does not follow into.
//
// It reads the workflows as text, like the rest of this package: go.mod carries
// no YAML library.
func TestNoMultiLineValueIsWrittenToAStepOutput(t *testing.T) {
	root := repoRoot(t)
	for _, file := range []string{".github/workflows/ci.yml", ".github/workflows/certification.yml"} {
		src := readFile(t, filepath.Join(root, file))
		found := flaggedOutputWrites(src)
		if len(runBlocks(src)) == 0 {
			t.Fatalf("no run blocks parsed from %s; the parser is looking in the wrong place", file)
		}
		for _, v := range found {
			t.Errorf("%s:%d: %s", file, v.line, v.msg)
		}
	}
}

// TestOutputHeuristicFlagsTheMergeGateRegression is the rule's own test, over the
// two texts that matter: the merge gate's step as it stood when integration-3's
// group died, and the same step with #1318's guard. A class rule whose tree is
// already clean proves nothing by passing, so it is made to fail here on purpose.
func TestOutputHeuristicFlagsTheMergeGateRegression(t *testing.T) {
	const before = `
jobs:
  test-hosted-merge:
    steps:
      - name: select the packages this group changes
        id: pkgs
        shell: bash
        run: |
          set -euo pipefail
          base="${{ github.event.merge_group.base_sha }}"
          if git diff --name-only "$base" HEAD | grep -q -E '^(go\.mod|go\.sum)$'; then
            pkgs=$(go list ./...)
          else
            pkgs=""
          fi
          pkgs="$pkgs ./internal/ci"
          echo "pkgs=$pkgs" >> "$GITHUB_OUTPUT"
`
	const after = `
jobs:
  test-hosted-merge:
    steps:
      - name: select the packages this group changes
        id: pkgs
        shell: bash
        run: |
          set -euo pipefail
          base="${{ github.event.merge_group.base_sha }}"
          if git diff --name-only "$base" HEAD | grep -q -E '^(go\.mod|go\.sum)$'; then
            pkgs=$(go list ./... | tr '\n' ' ')
          else
            pkgs=""
          fi
          pkgs="$pkgs ./internal/ci"
          echo "pkgs=$pkgs" >> "$GITHUB_OUTPUT"
`
	if got := flaggedOutputWrites(before); len(got) != 1 {
		t.Errorf("the pre-#1318 step must be flagged exactly once, got %d: %v", len(got), got)
	} else if !strings.Contains(got[0].msg, "go list") {
		t.Errorf("the finding does not name the producer that made the value multi-line: %s", got[0].msg)
	}
	if got := flaggedOutputWrites(after); len(got) != 0 {
		t.Errorf("the fixed step must pass, got %d finding(s): %v", len(got), got)
	}

	// The other two shapes the rule claims: a substitution written straight to
	// the output, and the heredoc form that is the right way to write a real
	// multi-line value.
	const direct = `
jobs:
  j:
    steps:
      - run: |
          echo "files=$(git diff --name-only HEAD~1 HEAD)" >> "$GITHUB_OUTPUT"
`
	if got := flaggedOutputWrites(direct); len(got) != 1 {
		t.Errorf("a multi-line substitution written straight to $GITHUB_OUTPUT must be flagged once, got %d: %v", len(got), got)
	}
	const heredoc = `
jobs:
  j:
    steps:
      - run: |
          files=$(git diff --name-only HEAD~1 HEAD)
          echo "files<<NOVA_EOF" >> "$GITHUB_OUTPUT"
          echo "$files" >> "$GITHUB_OUTPUT"
          echo "NOVA_EOF" >> "$GITHUB_OUTPUT"
`
	if got := flaggedOutputWrites(heredoc); len(got) != 0 {
		t.Errorf("the key<<EOF form is the supported way to write a multi-line value and must pass, got: %v", got)
	}
	// And the two narrowings, which must stay quiet: one package, and a script.
	const narrow = `
jobs:
  j:
    steps:
      - run: |
          p=$(go list "./${d#./}")
          slots=$(bash .github/scripts/plan-merge-slots.sh "$base" HEAD)
          echo "p=$p" >> "$GITHUB_OUTPUT"
          echo "slots=$slots" >> "$GITHUB_OUTPUT"
`
	if got := flaggedOutputWrites(narrow); len(got) != 0 {
		t.Errorf("the documented narrowings must not fire, got: %v", got)
	}
}

// outputFinding is one flagged write: the line in the workflow and what to do.
type outputFinding struct {
	line int
	msg  string
}

func (f outputFinding) String() string { return fmt.Sprintf("line %d: %s", f.line, f.msg) }

var (
	// A write to the step output or environment file, in any of the spellings
	// this repository uses: >> "$GITHUB_OUTPUT", >> $GITHUB_ENV, >> "${GITHUB_OUTPUT}".
	outputWriteRe = regexp.MustCompile(`>>\s*"?\$\{?(GITHUB_OUTPUT|GITHUB_ENV)\}?"?`)
	// An assignment at the start of a shell statement: NAME=value.
	shellAssignRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	// A variable reference, $NAME or ${NAME}.
	varRefRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)
	// The key<<DELIM form: the one supported way to write a multi-line value.
	heredocKeyRe = regexp.MustCompile(`^"?[A-Za-z_][A-Za-z0-9_]*<<`)

	// The producers whose output is ONE ITEM PER LINE by definition. Each is
	// named in the finding, so a red says which command made the value plural.
	multiLineProducers = []struct {
		name string
		re   *regexp.Regexp
	}{
		{"go list over a ... pattern", regexp.MustCompile(`\bgo list\b[^|)]*\.\.\.`)},
		{"git diff --name-only", regexp.MustCompile(`\bgit diff\b[^|)]*--name-only`)},
		{"git ls-files", regexp.MustCompile(`\bgit ls-files\b`)},
		{"find", regexp.MustCompile(`(^|[;&|(=]|\$\()\s*find\s`)},
		{"ls", regexp.MustCompile(`(^|[;&|(=]|\$\()\s*ls\s`)},
		{"cat", regexp.MustCompile(`(^|[;&|(=]|\$\()\s*cat\s`)},
		{"printf '%s\\n'", regexp.MustCompile(`printf\s+'%s\\n'`)},
	}

	// The guards that fold a stream onto one line. A pipeline carrying any of
	// them is single-line by construction and is not flagged.
	singleLineGuards = []*regexp.Regexp{
		regexp.MustCompile(`\|\s*tr\b`),
		regexp.MustCompile(`\|\s*paste\b`),
		regexp.MustCompile(`\|\s*xargs\b`),
		regexp.MustCompile(`\|\s*jq\b[^|]*-[A-Za-z]*c\b`),
		regexp.MustCompile(`\|\s*head\s+(-n\s*)?-?1\b`),
		regexp.MustCompile(`\|\s*tail\s+(-n\s*)?-?1\b`),
		regexp.MustCompile(`\|\s*wc\b`),
	}
)

// hasSingleLineGuard reports whether the pipeline folds its stream onto one
// line, which is both what makes a value safe to write and what clears a
// variable that was plural before.
func hasSingleLineGuard(value string) bool {
	for _, g := range singleLineGuards {
		if g.MatchString(value) {
			return true
		}
	}
	return false
}

// multiLineValue reports the producer that makes value plural, or "" when the
// value is single-line — either because it names no producer or because its
// pipeline carries a guard.
func multiLineValue(value string) string {
	if hasSingleLineGuard(value) {
		return ""
	}
	for _, p := range multiLineProducers {
		if p.re.MatchString(value) {
			return p.name
		}
	}
	return ""
}

// flaggedOutputWrites returns every write of a multi-line value to $GITHUB_OUTPUT
// or $GITHUB_ENV in the workflow source. Taint is tracked per `run:` block: a
// block is one shell, and a variable from another step is another step's
// business.
func flaggedOutputWrites(src string) []outputFinding {
	var found []outputFinding
	for _, block := range runBlocks(src) {
		// producer[name] is the producer that made that variable plural.
		producer := map[string]string{}
		// heredoc is the delimiter of an open `key<<DELIM` write: between it and
		// its closing line the block is deliberately writing a multi-line value
		// the supported way, so those lines are not read as key=value.
		heredoc := ""
		for _, cmd := range block {
			text := cmd.cmd
			if strings.HasPrefix(strings.TrimSpace(text), "#") {
				continue
			}
			if m := shellAssignRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil && !outputWriteRe.MatchString(text) {
				name, value := m[1], m[2]
				if p := multiLineValue(value); p != "" {
					producer[name] = p
					continue
				}
				// ONLY A GUARDED assignment clears a variable, because this
				// reader is linear and a shell script is not: the step that lost
				// the merge group assigned the plural value in one branch of an
				// `if` and `pkgs=""` in the other, and a clear-on-any-assignment
				// reader would have called it clean. A guard — `| tr`, `| xargs`,
				// `| jq -c` — is the normalization that really does make the
				// variable single-line, so that is what clears it.
				if hasSingleLineGuard(value) {
					delete(producer, name)
					continue
				}
				// Otherwise a plural variable carried into this one — including
				// itself, as `pkgs="$pkgs ./internal/ci"` does — keeps it plural.
				for _, ref := range varRefRe.FindAllStringSubmatch(value, -1) {
					if p, ok := producer[ref[1]]; ok {
						producer[name] = p
						break
					}
				}
				continue
			}
			loc := outputWriteRe.FindStringIndex(text)
			if loc == nil {
				continue
			}
			payload := strings.TrimSpace(text[:loc[0]])
			payload = strings.TrimPrefix(payload, "echo ")
			payload = strings.TrimSpace(payload)
			payload = strings.Trim(payload, `"`)
			if heredoc != "" {
				if payload == heredoc {
					heredoc = ""
				}
				continue
			}
			if heredocKeyRe.MatchString(payload) {
				if i := strings.Index(payload, "<<"); i >= 0 {
					heredoc = strings.Trim(strings.TrimSpace(payload[i+2:]), `'"`)
				}
				continue
			}
			// A redirect attached to a block or a loop: this reader does not
			// follow into it, and says so in the doc comment rather than
			// guessing.
			if payload == "}" || payload == "done" || payload == "fi" || payload == "" {
				continue
			}
			if p := multiLineValue(payload); p != "" {
				found = append(found, outputFinding{line: cmd.line, msg: fmt.Sprintf(
					"a %s substitution is written straight to a step output file, which takes key=value ONE PER LINE; pipe it through `tr '\\n' ' '` or use the key<<EOF form: %q", p, strings.TrimSpace(text))})
				continue
			}
			for _, ref := range varRefRe.FindAllStringSubmatch(payload, -1) {
				p, ok := producer[ref[1]]
				if !ok {
					continue
				}
				found = append(found, outputFinding{line: cmd.line, msg: fmt.Sprintf(
					"$%s comes from %s, which prints one item per line, and a step output file takes key=value ONE PER LINE (integration-3 lost all 18 legs of a merge group to this on 2026-09-18); pipe the assignment through `tr '\\n' ' '` or use the key<<EOF form: %q", ref[1], p, strings.TrimSpace(text))})
				break
			}
		}
	}
	return found
}

// runBlocks groups ci.yml's `run:` commands by the block they live in, because
// one block is one shell: a variable assigned in one step is gone by the next.
// It reuses runCommands' line handling, splitting the flat list wherever the
// line numbers stop being consecutive — which is exactly a block boundary, since
// runCommands emits one entry per line of a block scalar.
func runBlocks(src string) [][]runCommand {
	cmds := runCommands(src)
	var out [][]runCommand
	var cur []runCommand
	prev := -1
	for _, c := range cmds {
		if prev >= 0 && c.line != prev+1 {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, c)
		prev = c.line
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
