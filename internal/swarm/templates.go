package swarm

import (
	"fmt"
	"sort"
	"strings"
)

// THE TEMPLATES ARE THE SINGLE HIGHEST-VALUE THING IN THIS SPEC.
//
// A task is a text, and a bare text produces a bare answer. The conditions below are what
// turned one worker from 62 of 67 accurate with 5 wrong and 25 duplicate (batch 1) into 17
// of 17 with 0 wrong and 0 duplicate (batch 2), and a file budget turned 0 of 3 complete
// into 2 of 3 (batch 3). In the prototype they lived in a task text a person retyped, which
// means they were sometimes retyped and sometimes forgotten. Here they are text in the
// binary, printable, and a caller may write their own file instead: the tool has no list of
// blessed task shapes.
//
// A TEMPLATE IS TEXT AND NOTHING ELSE. It is not code, it does not execute, and nothing in
// this package reads a worker's RESULT.md and acts on it.

const templateReadPR = `read-pr — read one pull request against the rules

1. READ THE PR BODY'S OWED LIST FIRST, before reading any code, and for every
   finding you report, say whether it is already on that list. A finding that
   is already owed is marked ` + "`dup:`" + ` and is not a new finding.
   [batch 1: 25 of 67 findings were duplicates of the owed list]
2. QUOTE EVERY RULE VERBATIM, with ` + "`file:line`" + `. Never paraphrase a rule from
   memory, and never assert a rule you did not open.
   [batch 1: 5 of 67 findings were wrong, each a paraphrase]
3. APPEND EACH FINDING TO RESULT.md THE MOMENT IT EXISTS. Not at the end.
   You may be killed at your deadline; what is on disk is what you found.
4. A FILE BUDGET: read at most <n> files (the task's --files). When the budget
   is spent, write what you have and stop. Say in RESULT.md which files you
   did not open.
   [batch 3: with a budget, 2 of 3 tasks complete; without, 0 of 3]
5. A RESULT.md CONTAINING ONLY A PLAN IS A FAILED TASK. The plan belongs at
   the top, before the work; the findings are the work. A finished read that
   found nothing is NOT a failed task: write the ` + "`## Head`" + ` with ` + "`findings: 0`" + `.
   Never report a finding to have something to report.
6. Check the board before reporting: a card that already names this is a ` + "`dup:`" + `.
`

const templateProbeRow = `probe-row — make one claim true or false

1. Name the claim in one sentence at the top of RESULT.md before probing it.
2. The probe is a command, a file:line, or a measurement — never an opinion.
   Paste the command and its tail into RESULT.md.
3. Append the result the moment you have it.
4. A file budget: read at most <n> files (the task's --files). When the budget
   is spent, write what you have and stop.
5. A probe that could not be run is a RESULT with ` + "`not done`" + ` and the reason.
   That is a complete task; a guess is not.
`

const templateFixCard = `fix-card — take one card and land the fix

1. Read the card, and the board, before touching anything: a card already taken
   is a ` + "`dup:`" + ` and you stop.
2. Work only inside the job directory. The clone is yours; nothing outside it
   is yours.
3. Quote the rule the fix serves, verbatim, with file:line.
4. Write the gate you ran and its result into RESULT.md's Gates table. A fix
   with no gate is ` + "`not done`" + `.
5. A file budget: read at most <n> files (the task's --files). When the budget
   is spent, write what you have and stop.
6. Leave what you did not do under ` + "`Left owed`" + `, named so the next worker can
   pick it up with no other context.
`

// templateResult is the ONE shape a report has, so the fold is mechanical and a person
// reads counts. The parser in result.go parses exactly this and nothing else.
const templateResult = "# <task>\n" + `
## Head
findings: <n>
notes read: <n>
repo: <owner>/<name>
rev: <sha>
<one paragraph: what was asked, what the state is now, and the single most
important fact.>

## Findings
- <one finding, appended the moment it exists: what was found, the rule it rests
  on quoted verbatim between backticks, and the file:line it is beside. A finding
  already on the owed list begins ` + "`dup:`" + `.>

## Per item
| item | state | evidence |
| --- | --- | --- |
| <the item as it was handed to me> | red / green / not done | <file:line, gate name, PR #, or the command and its tail> |

## Gates
| name | result | seconds |
| --- | --- | --- |
| <gate or command> | pass / fail / not run | <n> |

## Left owed
- <the item nobody did, named so the next worker can pick it up with no other
  context.>

## One line
<one sentence a coordinator can paste into the board.>
`

// Template returns one template by name.
func Template(name string) (string, error) {
	switch name {
	case "read-pr":
		return templateReadPR, nil
	case "probe-row":
		return templateProbeRow, nil
	case "fix-card":
		return templateFixCard, nil
	case "result":
		return templateResult, nil
	}
	return "", fmt.Errorf("--name wants one of %s, got %q", strings.Join(TemplateNames(), ", "), name)
}

// TemplateNames is every name Template answers to, in a fixed order.
func TemplateNames() []string {
	names := []string{"read-pr", "probe-row", "fix-card", "result"}
	sort.Strings(names)
	return names
}

// WrapTemplate puts a task's own text under its template's conditions, with the file budget
// written into the condition that names it, so the number in the prompt is the number the
// machinery will hold the worker to.
func WrapTemplate(name string, files int, text []byte) ([]byte, error) {
	body, err := Template(name)
	if err != nil {
		return nil, err
	}
	if name == "result" {
		return nil, fmt.Errorf("--template wants a task template (read-pr, probe-row, fix-card); `result` is the report's shape, printed by `template --name result`")
	}
	body = strings.ReplaceAll(body, "<n> files", fmt.Sprintf("%d files", files))
	var b strings.Builder
	b.WriteString("## The conditions (they are worth more than the model)\n\n")
	b.WriteString(body)
	b.WriteString("\n## The task\n\n")
	b.Write(text)
	if len(text) > 0 && text[len(text)-1] != '\n' {
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}
