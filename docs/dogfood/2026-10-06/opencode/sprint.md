# nova-sprint dogfood report: 2026-10-06 (opencode)

1. `nova-sprint -h`
   Output (first 3 lines):
   nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land
   how it works: one store (Redis or a twin file) holds the work, readers, merge
   and fleet tables and the sprint view. A card is one unit of work in a stream.
   Expected: banner answering what it does, how it works, how to use it, with an example block.
   Grade: URGENT (help is long and hard to scan; no quickstart verb)

2. `nova-sprint help add`
   Output (first 3 lines):
   usage: nova-sprint add --stream <s> (<id>... | --count <n> | ...)
   from `nova-sprint help`:
   nova-sprint add --stream <s> (<id>... | --count <n> | ...)
   Expected: concise usage with a short example block, not a 60-line wall.
   Grade: NEXT (very verbose; harder for a stranger to find what they need)

3. `nova-sprint help land`
   Output (first 3 lines):
   usage: nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
   from `nova-sprint help`:
   nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
   Expected: concise help with clear example.
   Grade: NEXT (too verbose)

4. `nova-sprint help friend`
   Output (first 3 lines):
   usage:
   nova-sprint friend sync [--pg <dsn>] [--root <dir>]
   nova-sprint friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...]
   Expected: shorter, clearer verb list with inline examples.
   Grade: NEXT (verbose; lacks a quickstart for friends)

5. `nova-sprint where --json` (with empty mem store)
   Output (first 3 lines):
   {"all":1,"archived_cards":0,"archived_landed":0,"at":"2026-10-07T02:35:51.538735573Z","backup":"merges","buffer":"0/128","cleared":"0001-01-01T00:00:00Z","coordinator":"boss",...}
   Expected: JSON output with clear structure showing sprint state.
   Grade: NEXT (large JSON; useful but could be better organized)

6. `nova-sprint help rules`
   Output (first 3 lines):
   usage: nova-sprint rules [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
   from `nova-sprint help`:
   nova-sprint rules [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
   Expected: clearer explanation of what rules are.
   Grade: NEXT (short but lacks context)

7. `nova-sprint help set`
   Output (first 3 lines):
   usage: nova-sprint set [--read-tier <flash|pro|default>] [--read-cards <on|off|default>] [--dealt-max <duration|default>] [--go-lanes <n|default>] [--alarm-review <n|off>] [--alarm-merging <n|off>] [--alarm-fleet <percent|off>] [--alarm-ready <on|off>] [--attempts <n|default>] [--friend-idle <duration|default>] [--friend-finish <duration|default>] [--fleet-tiers <tiers|all>] [--friends-tiers <tiers|all>]
   Expected: a summary of what `set` does before the flags.
   Grade: NEXT (too many flags; hard to parse)

8. `nova-sprint help goal`
   Output (first 3 lines):
   usage:
   nova-sprint goal set <name> [--file <path>] [--to file:<path>]
   nova-sprint goal show [<name>]
   Expected: more inline examples for each subcommand.
   Grade: NEXT (basic but minimal examples)

9. `nova-sprint help clear`
   Output (first 3 lines):
   usage: nova-sprint clear --confirm sprint
   from `nova-sprint help`:
   nova-sprint clear --confirm sprint
   Expected: short and clear as it is, but a warning about data loss would help.
   Grade: NEXT (help is brief, but could warn more strongly)

10. `nova-sprint where` (text output with no sprint initialized)
    Output (first 3 lines):
    Work:
    s1: waiting 0 ready 0 working 0 review 0 merging 0 landed 0
    Fleet:
    Expected: no work/queue state when nothing is set up.
    Grade: NEXT (clear but no guidance on how to initialize)

11. `nova-sprint add --stream s1 --count 1 --one` (without --actor)
    Output:
    nova-sprint add REFUSED: ACTOR: an actor is required for this verb; run: nova-sprint help add
    Expected: the error explains what actor is and why it is needed.
    Grade: URGENT (refusal does not explain what the actor flag is used for)

12. `nova-sprint inbox` (with no judgments)
    Output:
    INBOX OK judgments=0 cursor=-
    Expected: a message saying the inbox is empty and what to do next.
    Grade: NEXT (minimal output; helpful but terse)

READ 6/10: Readability is uneven; the help pages are extremely verbose, making it hard for a stranger to find what they need. Some verbs have clear help (like `clear`), but others like `add` and `land` are 60+ lines.

USE 5/10: Usability is hampered by the lack of a quickstart workflow. You need to manually initialize a sprint with `init`, then you can explore other verbs. The `mem:` store works well for testing.

urgent=2 next=10
