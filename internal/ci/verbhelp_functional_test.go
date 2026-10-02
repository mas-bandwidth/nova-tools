//go:build functional

package ci

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
)

// The verb-help rule (the CLI style's rule (b), #4505), held for EVERY living
// command and EVERY verb its own `help` names, by walking cmd/ rather than by
// listing tools: `<tool> <verb> -h` prints that verb's help on stdout, exits
// 0, writes nothing on stderr and creates nothing. Go's flag package answers
// -h with `flag: help requested` at exit 2 unless a verb parses through the
// one seam, internal/nsprint/verbflag, and an AI reads that exit 2 as a
// syntax error rather than as the help it asked for (2026-09-27 verb audit,
// 14 of 16 tools). It runs inside TestEveryCommandMeetsTheOnboardingStandard,
// on the binary that test has already built and the banner it has already
// read, so no command is built twice.

// helpRefusedByDesign names the living commands whose verbs keep refusing -h
// at exit 2, with the reason. It is not a skip: the refusal is asserted, so a
// change to it is a decision someone has to make on purpose. nova-fuse is the
// one: its exit 0 means CLEAR or DONE-AND-VERIFIED, and a surface or reason
// that arrives as "-h" must never be answered with exit 0.
var helpRefusedByDesign = map[string]string{
	"nova-fuse": "exit 0 is the fuse's CLEAR and DONE; a surface named -h must not read as permission",
}

// exitLabel is the label of an exit-codes paragraph in any case: `exit codes:`
// anywhere in a line, or the short `exit:` where it opens one.
var exitLabel = regexp.MustCompile(`(?i)(\bexit codes?\s*:|^\s*exit\s*:)`)

// statedExitCodes is the first line of the exit-codes paragraph a banner
// states, from the label on; "" when the banner states none. A label that
// opens a line wins over one inside a sentence. It is the witness for
// verbflag's own matcher, written apart from it.
func statedExitCodes(banner string) string {
	inSentence := ""
	for _, l := range strings.Split(banner, "\n") {
		loc := exitLabel.FindStringIndex(l)
		if loc == nil {
			continue
		}
		if strings.TrimSpace(l[:loc[0]]) == "" {
			return strings.TrimSpace(l[loc[0]:])
		}
		if inSentence == "" {
			inSentence = strings.TrimSpace(l[loc[0]:])
		}
	}
	return inSentence
}

// helpStatesExitCodes reports whether a verb's help carries a line that opens
// with an exit-codes label other than the `see help` pointer.
func helpStatesExitCodes(tool, help string) bool {
	for _, l := range strings.Split(help, "\n") {
		if exitLabel.MatchString(l) && strings.TrimSpace(l) != "exit codes: see `"+tool+" help`" {
			return true
		}
	}
	return false
}

// usageVerbs are the verbs a banner's own usage block names: the lines above
// its first `example:` (an example line is an invocation with arguments, not
// a verb), through the dogfood ledger's parser, less the bare tool and help.
func usageVerbs(tool, banner string) []string {
	block, _, _ := strings.Cut(banner, "\nexample:\n")
	var verbs []string
	for _, v := range dogfood.ParseHelp(block) {
		if v.Tool != tool || v.Verb == "" || v.Verb == "help" {
			continue
		}
		verbs = append(verbs, v.Verb)
	}
	return verbs
}

func everyVerbAnswersHelp(t *testing.T, root, tool, bin, banner string) {
	t.Helper()
	if !loadLiveTree(t, root).Package("cmd/" + tool) {
		return // deprecated: never tested (internal/pkgselect/DEPRECATED)
	}
	verbs := usageVerbs(tool, banner)
	if len(verbs) == 0 {
		t.Fatalf("`%s help` names no verb in its usage block; the verb-help check would pass by checking nothing", tool)
	}
	_, byDesign := helpRefusedByDesign[tool]
	for _, verb := range verbs {
		dir := t.TempDir()
		args := append(strings.Fields(verb), "-h")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + dir}
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		var exitErr *exec.ExitError
		switch err := cmd.Run(); {
		case err == nil:
		case errors.As(err, &exitErr):
			code = exitErr.ExitCode()
		default:
			cancel()
			t.Fatalf("running %s %s: %v", tool, strings.Join(args, " "), err)
		}
		cancel()
		line := tool + " " + strings.Join(args, " ")
		if byDesign {
			if code != 2 || out.Len() != 0 {
				t.Errorf("`%s` exited %d with stdout %q; %s refuses -h at exit 2 by design (%s)", line, code, out.String(), tool, helpRefusedByDesign[tool])
			}
			continue
		}
		if code != 0 || strings.TrimSpace(out.String()) == "" || errb.Len() != 0 {
			t.Errorf("`%s` exited %d, stdout %d bytes, stderr %q; want that verb's help on stdout at exit 0 and nothing on stderr (route the verb's flag parsing through internal/nsprint/verbflag)", line, code, out.Len(), errb.String())
		}
		// The exit-codes line is the onboarding standard, and it reaches every
		// tool's verb help: the banner states the tool's codes (no exemption),
		// and a verb's -h quotes them, never the `see help` pointer. A verb that
		// verbflag assembles quotes the banner's own line; a tool with verb help
		// of its own states its own. One verb per tool is enough: every verb's
		// help is assembled by one seam or one table.
		if verb == verbs[0] {
			want := statedExitCodes(banner)
			if want == "" {
				t.Errorf("`%s help` states no exit codes; the onboarding standard wants an `exit codes: 0 ..., 1 ..., 2 ...` line in every banner", tool)
			} else if !helpStatesExitCodes(tool, out.String()) {
				t.Errorf("`%s` prints no exit-codes line; its banner states %q; got:\n%s", line, want, out.String())
			} else if strings.HasPrefix(out.String(), "usage: "+tool) && !strings.Contains("\n"+out.String(), "\n"+want+"\n") {
				t.Errorf("`%s` does not quote the exit-codes line its banner states (%q); got:\n%s", line, want, out.String())
			}
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			var names []string
			for _, e := range entries {
				names = append(names, filepath.Join(dir, e.Name()))
			}
			t.Errorf("`%s` created %v; help writes nothing", line, names)
		}
	}
}
