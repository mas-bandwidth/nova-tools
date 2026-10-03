# nova-bus READ rating, nova-tools 1.1.0

Rater: gpt-5.6-sol
Build: 2c02b2aa2042
Score: 6.5/10
README: 7/10

## Reasons

README first impression: I was first confused at README.md:24 because the first-command cell presents a bus command before the later local repository setup. I was first bored at README.md:21 when the long all-tools table began; the bus row itself is compact, but finding its setup requires continuing through many unrelated rows. I first doubted a claim at README.md:48, where the page calls these the 1.0.0 commands while this rating targets 1.1.0; the explicit release pin is coherent, but it makes the current-version path harder to locate.

The tool has a coherent idea and unusually explicit failure semantics. The repository, lanes, roster, notes, receipts, and cursor form a vocabulary a reader can recover quickly from the banner. The code also bears out the central promise: Git is the transport and record, state stays in the repository, writing validates identity and repository state, and read operations distinguish new work from carried work.

The score loses most of its distance from 10 on weight and navigation. The command entry point is 3,823 lines and owns dispatch, every large verb, output formatting, continuation state, polling, repair logging, and Git-facing orchestration. The command reference similarly asks a cold reader to absorb setup, cursor migration, delivery, reply, pagination, and recovery in one very long section, then spreads the formal contract over three bus specifications. The explanations are thoughtful, but many comments preserve design history and argue through alternatives at paragraph length, so the code reads like the complete deliberation rather than the smallest finished story. A 10 would split verb orchestration into narrow files, give the command reference a short common path with advanced material behind links, and contract comments to present-tense invariants while keeping the strong tests.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-bus/main.go:350 | One 3,823-line entry file dispatches all verbs and also contains the large inbox, wait, check, pagination, repair, formatting, and Git orchestration paths; a reader finds the verbs quickly but cannot hold each file as one idea. | Move each verb and its private output helpers into a focused file, leaving dispatch and shared flag mechanics in the entry file. | L |
| 2 | docs/CLI.md:420 | The opening paragraph compresses the whole protocol into a dense wall, and the section then carries setup, ten verbs, migration behavior, retry semantics, paging, and recovery; the first useful path is accurate but expensive to isolate. | Open with a three-line purpose and one minimal local exchange, then link separate task pages for setup, reading, sending, and recovery. | M |
| 3 | cmd/nova-bus/main.go:4 | The 36-line package preface explains historical failures and successive fixes before the package begins; similar long comments throughout preserve deliberation that obscures the present contract. | Keep the current nouns and invariants in a short package comment and move historical rationale to the design documents already present. | M |

## Good, keep

The banner's first lines name the repository, roster, lane, note, receipt, and cursor clearly. The strict parsing, bounded Git output, path guards, and explicit retry behavior show careful refusal design. The first-run tests execute documented setup and editing steps, while the refusal and synopsis tests pin the public door and advertised flags; those tests teach real contracts rather than mirror implementation.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| A 467-line function | STILL THERE | cmd/nova-bus/main.go:1618 begins `inboxListing`, which ends at cmd/nova-bus/main.go:2083. |
| Calls with 16 parameters | STILL THERE | cmd/nova-bus/main.go:2171 gives `advanceCursorTo` a long mixed list of state, policy, clock, and output parameters. |
| War-story comments | STILL THERE | cmd/nova-bus/main.go:2172 narrates a dated prior break and its aftermath inside a current helper. |
| REFUSED at exit 1 | STILL THERE | cmd/nova-bus/main.go:2191 prints `INBOX REFUSED` immediately before returning 1. |
| Retired flags still declared | STILL THERE | cmd/nova-bus/main.go:2639 declares retired `--beat` and `--beat-lease` flags that are accepted and ignored. |
| Prose promises more than bounded retries deliver | CHANGED | docs/CLI.md:519 now names the 25-attempt default and explains that it is a measured retry budget. |
