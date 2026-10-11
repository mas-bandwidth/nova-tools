# nova-bus READ rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 6.5/10
README: 7/10

## Reasons

The README sentence for this tool is README.md:24, "notes between AIs, over a git repository". That line is the same sentence as the banner's first line (cmd/nova-bus/main.go:61), and the code's verbs do that job: a git repository, a roster, one lane directory per sender, one markdown note per file, and commands to draft, send, list, wait, receipt, and check.

Confused first at README.md:24. The printed command is `nova-bus inbox --bus ./trial-bus --as Ada --receipt-max-words 40 --full`. None of those flags is defined on the page yet, the cell says both "Reads the example bus" and "Initialize the local Git bus as shown below", and the block that creates the directory is README.md:64. A cold reader cannot run the row's command from the row.

Bored first at AGENTS.md:19. The generated map embeds the whole house standard through AGENTS.md:156 before the one useful line for this command, cmd/AGENTS.md:6 ("coordination bus inbox, send, and wait CLI"). The README's first pointer, README.md:16, is the install section of the usage guide, which is the same catalog again rather than this tool.

Doubted first at README.md:48, "These are the Nova Tools 1.0.0 commands", and the install example at README.md:53, which pins release 1.0.0. This head's command reference already describes prepare, reply, close, and bodies. The page does not say which of those the pinned release lacks.

What is close to a 10 is the command reference's opening. docs/CLI.md:420 says what the tool is in three lines (a git repository, lanes, a header, ten verbs, no opinion about the body), and docs/CLI.md:422-473 is a first sitting with a real transcript, including the cursor refusal a copied example produces. docs/CLI.md:636-640 says the price of the cursor in one paragraph: a reader is asked twice and is never told a note is answered when it is not. docs/CLI.md:710 and cmd/nova-bus/main.go:37-39 keep the limit: a postal service, not a reader, and the rule that a note is not a grant is stated and is not enforced in code. pkg/bus/draft_test.go:128 and pkg/bus/draft_test.go:154 teach the contract by name: what send still refuses, and that one run reports every problem in the draft.

What holds the score down is the writing an AI actually meets.

The banner does not stop. cmd/nova-bus/main.go:61 opens a usage string that runs to cmd/nova-bus/main.go:331: verb lines, then the same cursor, switch-day, and wait story told again, then a roster essay, then a second setup, then the example. The file comment at cmd/nova-bus/main.go:4-39 tells that story a third time, in the past tense, and docs/SPEC.md:2818 opens the normative section as the record of one night. An AI that runs help cannot tell the contract from the memoir.

One function is the whole inbox report. cmd/nova-bus/main.go:1618, inboxListing, is 467 lines, and main.go is every verb (3823 lines). pkg/bus is split by noun, which is the right shape, and then the nouns grow novels: repair.go 1146 lines, git.go 1122, cursor.go 992. A stranger does not find one verb in one file.

The specs do not agree with the tree or with each other. docs/SPEC-BUS.md:7 says "specified, not implemented; no code" at the top of a file whose own line 185 says reply is already implemented. docs/SPEC-BUS-DELIVERY.md:2 says "not implemented" and docs/SPEC-BUS-DELIVERY.md:133 says the tree already carries a full implementation of prepare and send --prepared. docs/SPEC.md:2845, the verb block a reader hits first, omits reply and prepare, which the same file documents later (docs/SPEC.md:3526) and which the banner lists (cmd/nova-bus/main.go:80 and cmd/nova-bus/main.go:88). The house rule is that a spec is normative and present tense. These status lines are neither.

Refusal is two grammars. A missing flag is `nova-bus <verb>: --<flag> is required; refusing to guess; run: nova-bus help` at exit 2 (cmd/nova-bus/main.go:448), and the remedy is the 270-line banner rather than that verb's help. A cursor with no lane is `INBOX REFUSED:` at exit 1 (cmd/nova-bus/main.go:2191). docs/SPEC.md:2914 puts "a draft refused" on exit 1 and a missing flag on exit 2, so the split is specified, and an AI still has to learn two sentences and two codes for "no". docs/CLI-STYLE.md:91 asks the malformed-invocation remedy to be `<tool> <verb> -h`. This tool's missing-flag line does not do that.

The push sentence is larger than the loop. cmd/nova-bus/main.go:13 and pkg/bus/git.go:135 say a rejected push never reaches the caller. pkg/bus/git.go:810 retries, then pkg/bus/git.go:857 returns the commit unpushed and names a hand recovery. docs/CLI.md:420 repeats the larger sentence ("no rejected push ever reaches a person"). Bounded retry is real. The sentence is not. The same file disagrees with itself about conflicts: pkg/bus/git.go:140 says a rebase conflict is not this tool's to settle, and pkg/bus/git.go:836 settles the tool's own files.

Retired flags are still declared. cmd/nova-bus/main.go:2639 and cmd/nova-bus/main.go:2640 keep `--beat` and `--beat-lease`, described as retired and ignored, on a verb whose own note says the bus carries notes and not beats.

A 10 keeps the three-line purpose, the first-run transcript, the cursor paragraph, and the tests that name every refusal. It cuts the banner to the verb lines, the exit table, and the example. It makes one refusal sentence, with the verb's own help as the remedy, and it makes the exit table match that sentence. It rewrites the three status lines so each says what the tree does now, and it puts reply and prepare in the verb block they already belong to. It splits inboxListing until a reader can hold it, and it deletes the retired flags. The push sentence then says what pkg/bus/git.go:857 already says: after the budget, the commit is local and not on the bus.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-bus/main.go:61 | help is a memoir: the usage string runs to line 331 and retells the cursor, the switch-day line, and the wait loop that the file comment and the command reference already tell | cut usage to the three-line banner, the verb lines, the exit table, and the example block; leave the memoir in the command reference | L |
| 2 | cmd/nova-bus/main.go:1618 | inboxListing is 467 lines and main.go is every verb, so a reader cannot find one behaviour in one place | split the report into load, decide, and print, and move each verb into its own file | L |
| 3 | docs/SPEC-BUS.md:7 | the status line says there is no code, the delivery spec says both "not implemented" and "full implementation", and the verb block omits reply and prepare | one present-tense status per spec, and add reply and prepare to the verb block at docs/SPEC.md:2845 | M |
| 4 | cmd/nova-bus/main.go:448 | a missing flag and a runtime no are different sentences and different exits, and the missing-flag remedy opens the whole banner | one refusal line, invocation problems at exit 2, and a remedy of `nova-bus <verb> -h` | M |
| 5 | pkg/bus/git.go:135 | the comment says a rejected push never reaches the caller; the loop gives up at line 857 and leaves the commit unpushed | say the bound, and name the exhaustion line as the result of the last attempt | S |
| 6 | cmd/nova-bus/main.go:2639 | `--beat` and `--beat-lease` are still declared on wait, then ignored with a note | delete the two flags and the note | S |

## Good, keep

The banner's first line matches the README sentence, and the how-it-works paragraph names the nouns (roster, lane, note, receipt, cursor) in five lines (cmd/nova-bus/main.go:61-69).

docs/CLI.md:636-640 says the cursor's price in one paragraph, and docs/CLI.md:710 keeps the limit: this tool delivers a note and does not read it.

pkg/bus/draft_test.go:154 pins the rule an AI needs on a bad draft: one run names every problem, not the first.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 467-line function | STILL THERE | cmd/nova-bus/main.go:1618 inboxListing still runs 467 lines, to the next function at line 2085 |
| 16-parameter calls | CHANGED | the widest bare signature is 13 parameters at cmd/nova-bus/main.go:2171; 16 values travel as one replyOpts value at cmd/nova-bus/draft.go:82 |
| war-story comments | STILL THERE | cmd/nova-bus/main.go:4 opens on a lost note, and docs/SPEC.md:2818 opens the contract on one night's record |
| REFUSED at exit 1 | STILL THERE | cmd/nova-bus/main.go:2191 prints INBOX REFUSED and returns 1; docs/SPEC.md:2914 still puts a draft refusal on exit 1 |
| retired flags still declared | STILL THERE | cmd/nova-bus/main.go:2639 and cmd/nova-bus/main.go:2640 still declare `--beat` and `--beat-lease` |
| prose promises more than bounded retries deliver | STILL THERE | pkg/bus/git.go:135 says a rejected push never reaches the caller; pkg/bus/git.go:857 returns the commit unpushed |
