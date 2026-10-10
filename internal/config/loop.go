package config

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// A LOOP WHOSE VERB IS GONE. A loop row runs a nova verb; when a release retires
// the verb the unit exits 2 at every start and is restarted for ever (the
// coordinator machine's sprint-table-live ran `nova-sprint table` for days
// after nova-sprint lost it, found 2026-10-04). Each nova program the argv runs
// is asked, through its own help, whether it still has the verb: a loop whose
// verb is gone is refused at add and set (CheckLoopVerb), and named by status
// (DeadLoops), so it is removed rather than left failing.

// NovaTools are the programs of this repository (cmd/): the only words of an
// argv the check runs. A wrapper (nova-loop, a shell) is never run to ask it.
// TestNovaToolsIsEveryCommand holds the list to the directories of cmd/: a new
// command is added here, or that test is red.
var NovaTools = []string{
	"nova-bus", "nova-cairn", "nova-check", "nova-ci", "nova-config", "nova-decide",
	"nova-doctor", "nova-friend", "nova-fuse", "nova-local", "nova-memory", "nova-redis",
	"nova-sandbox", "nova-secrets", "nova-self-talk", "nova-swarm", "nova-table",
	"nova-tokens", "nova-up", "nova-update", "nova-version",
}

// verbWord is a word that can be a verb: lower-case, a letter first. A path, a
// flag or a value with any other character is no verb, and is never asked.
var verbWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// LoopCall is one nova program an argv runs and the verb it runs it with.
type LoopCall struct{ Program, Verb string }

// LoopCalls is every nova program in argv followed by a verb word, in order:
// the program under each wrapper (nova-secrets exec ... -- nova-sprint where)
// as well as the wrapper's own. help and version are every tool's.
func LoopCalls(argv []string) []LoopCall {
	var out []LoopCall
	for i := 0; i+1 < len(argv); i++ {
		prog, verb := filepath.Base(argv[i]), argv[i+1]
		if !slices.Contains(NovaTools, prog) || !verbWord.MatchString(verb) || verb == "help" || verb == "version" {
			continue
		}
		out = append(out, LoopCall{Program: argv[i], Verb: verb})
		i++
	}
	return out
}

// VerbProbe says whether program no longer has verb: gone is true only when the
// program answered that it has no such verb; err is a probe that could not
// answer (the program is not installed here), which judges nothing.
type VerbProbe func(ctx context.Context, program, verb string) (gone bool, err error)

// DeadLoopVerb is the first call of argv whose verb probe says is gone, ok false
// when none is. A call the probe cannot answer is skipped: the binary a loop
// runs may be installed only on the loop's machine.
func DeadLoopVerb(ctx context.Context, argv []string, probe VerbProbe) (LoopCall, bool) {
	for _, c := range LoopCalls(argv) {
		if gone, err := probe(ctx, c.Program, c.Verb); err == nil && gone {
			return c, true
		}
	}
	return LoopCall{}, false
}

// DeadLoops is a line for each loop row whose verb is gone, in the rows' order:
// what status says, each with the remove that ends it.
func DeadLoops(ctx context.Context, rows []Row, probe VerbProbe) []string {
	var out []string
	for _, r := range rows {
		if c, gone := DeadLoopVerb(ctx, Argv(r.Fields["argv"]), probe); gone {
			out = append(out, deadLoopLine(r.Name, c)+"; run: nova-config loop remove "+r.Name)
		}
	}
	return out
}

func deadLoopLine(name string, c LoopCall) string {
	prog := filepath.Base(c.Program)
	return fmt.Sprintf("loop %s runs %s %s, and %s has no verb %s: its unit exits at every start", name, prog, c.Verb, prog, c.Verb)
}

// loopProbeBudget is how long one program has to answer its help.
const loopProbeBudget = 10 * time.Second

// CheckLoopVerb is the refusal add and set make of an enabled loop row whose
// verb is gone, nil when none is. A disabled row passes: its unit is not
// started, and setting --enabled false is a way out. The kind's Check runs no
// program; nova-config asks this with HelpProbe beside it.
func CheckLoopVerb(ctx context.Context, r Row, probe VerbProbe) error {
	if r.Fields["enabled"] == "false" {
		return nil
	}
	if c, gone := DeadLoopVerb(ctx, Argv(r.Fields["argv"]), probe); gone {
		return fmt.Errorf("%s; run %s help for its verbs, or --enabled false", deadLoopLine(r.Name, c), filepath.Base(c.Program))
	}
	return nil
}

// HelpProbe asks program `help <verb>`: exit 0 is a verb it has; exit 2 with a
// refusal naming verbs (every nova tool's unknown-verb refusal) is a verb it
// has not; anything else, or a program not installed here, is an error.
func HelpProbe(ctx context.Context, program, verb string) (bool, error) {
	path := program
	if !strings.ContainsRune(program, filepath.Separator) {
		p, err := exec.LookPath(program)
		if err != nil {
			return false, err
		}
		path = p
	}
	cmd, cancel := subproc.CommandFor(ctx, loopProbeBudget, path, "help", verb)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return false, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 && strings.Contains(string(out), "verb") {
		return true, nil
	}
	return false, fmt.Errorf("%s help %s: %w", program, verb, err)
}
