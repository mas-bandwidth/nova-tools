# nova-redis READ rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash (opencode)
Build: 0c5803c2de40
Score: 8/10
README: 8/10

## Reasons

The source under review is exactly 0c5803c2de406c1b0b2b0841f579c9bf73406b1c.
The README's table row for this tool is honest and lands a stranger in the
right place: one sentence ("run a local Redis store, and keep short-lived named
values in it") and a first command that writes an expiring value,
`spill --addr 127.0.0.1:6379 --owner trial --name note --ttl 1m --value hello`,
which needs a separate running instance, as the row's own caveat says. The
store-free dry-run move lives in the banner (main.go:124, main.go:145), not the
row. The banner repeats that same sentence as line one and names the nouns
(serve, spill, recall, fn) and where state lives in four lines.
`spill --dry-run` is the best thing here: a reader can watch the
key, its TTL and its byte count without an instance, and the code path is the
real one up to the dial (main.go:204). The refusal grammar is one line, names
where a bad value came from (the flag or the variable), takes the remedy a cold
reader can paste, and lists every independent problem in one run. The security
stance is the tool's spine and the code bears it out: the password is never an
argument (main.go:15), `serve` strips it from the child environment
(serve.go:255), redisconn withholds any text that held it (classify.go:179),
and a scratch key always carries an owner and a positive TTL (main.go:498).

What keeps it from a 9 or 10:
- The displayed verb list and the rated spec disagree. `docs/CLI.md:2034` and
  the code (`acl.go`) show three more verbs than `docs/SPEC-REDIS.md:17` lists,
  and a reader's map is now split across two documents that each claim to be
  the source of truth. A tool this small can afford one normative page.
- Two of ~5,000 lines of core prose are walls: the package comment
  (`main.go:1`) runs 26 lines across four paragraphs before a reader sees a
  verb, and the `fn` failure `err=` strings are long enough that the promised
  "one line" is a paragraph on a narrow terminal (fn.go:117).
- A card key's convention (`s:T:card:x`, `run=` used as a key name, "card" in
  comments) is a fleet concept leaking into a spec that promises it carries
  "none of the fleet".
- The restart proof (`TestRestartOnTheSameDirKeepsTheStore`) is skipped, so the
  "a restart replays every key" promise is asserted by no running test.

A 10 would be: one normative spec page that lists every verb the code has,
first-run help that shows the reader a refusal and its remedy rather than a
per-verb `Example: ""`, the restart/`serve` contract proven by a running
functional test, and no field-specific key convention in the general tool.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-REDIS.md:17 | The rated spec lists serve, spill, recall, fn load, fn check, version, help; the CLI reference (docs/CLI.md:2029) and acl.go add acl render, acl check, acl apply. A cold reader has two disagreeing lists of the tool's verbs and no single normative page. | Add the acl verbs to the spec's verb block and its "Tests this spec demands", or fold the reference's redis section into the spec and link one from the other. | M |
| 2 | cmd/nova-redis/main.go:1 | The package comment is a 26-line, four-paragraph wall before the first verb; a stranger meets all of serve, spill, recall and fn's rationale before learning how to run anything. | Move the per-file rationale into serve.go/fn.go (which already carry it) and leave one paragraph naming the tool, its store and its one opening door. | M |
| 3 | cmd/nova-redis/fn.go:117 | The FAILED line packs the cause stream, redisconn's own one-line-plus-next-step text, and `remedy=` into one physical line; the store's multi-clause `err` text makes it read as a paragraph, not the promised one-line grammar. | Bound the `err=` field (as `Err` does in redisconn) or move the cause details to a `NOTE` line, keeping the leading FAILED line short. | S |
| 4 | docs/SPEC-REDIS.md:98 | The spec promises "No unit test in this tool opens a network socket", but TestRestartOnTheSameDirKeepsTheStore in the functional tier is skipped with `t.Skip` (serve_functional_test.go:29), so the restart promise has no running proof. | Replace the wall-clock wait with a readiness poll seam and unskip it, per the comment's own plan. | M |
| 5 | cmd/nova-redis/serve_functional_test.go:38 | A test source and its comment name a card key (`s:T:card:x`, `origin`, `stream`) and rule numbers; a stranger rating this general tool meets a fleet vocabulary it was told the tool does not carry. | Use a plain key (`store:key`) and drop the ticket references; keep the contract, not the fleet. | S |
| 6 | docs/CLI.md:2045 | The refusal paragraph, the JSON envelope, the dry-run line and the four-refusal list crowd one unbroken block; the `--json` shape and the `spill --dry-run` line a reader most needs are buried mid-paragraph. | Break the paragraph into a short list: one bullet per refusal, one for `--json`, one for `--dry-run`. | S |

## Good, keep

- `spill --dry-run` prints the key, TTL, expiry and byte count and dials
  nothing (main.go:204); it is the tool's best onboarding move and should be
  the model for every write verb in the family.
- The refusal grammar names where each bad value came from (flag or variable),
  reports every independent problem in one run, and ends in a pasteable remedy
  (main.go:284, docs/TESTS.md:772).
- Every message about a connection opens with the store, the user and the
  password's variable name and never the password (redisconn/options.go:214,
  classify.go:179); this is the standard the rest of the family should copy.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| hand-rolled skeleton (1aac13259) | CHANGED | The tool now runs on pkg/tool (cmd/nova-redis/main.go:113 `redisTool(d deps) *tool.Tool`); only the Prints verbs hand-roll line rendering, and each says why (fn.go:231, acl.go:23). |
| a 50-line prose wall in help | FIXED | The banner's `How` is five lines (main.go:120-124), followed by a runnable `example:` block; verbhelp_test.go holds each verb's `-h`. |
| ticket numbers in the package doc | STILL THERE | cmd/nova-redis/main.go:1-27 cites docs/SPEC-REDIS.md and pkg/redisconn, and serve_functional_test.go:38 names `nova-tools#3879` in the key map. |
| three line grammars (1aac13259) | CHANGED | spill/recall render one `tool.Out` value; fn, serve and acl still print their own lines with documented reasons, so three printers remain but each is deliberate. |
| onboarding and output grammar rough edges (8.7 rater) | CHANGED | The first-run refusals are now executed line by line (docs/TESTS.md:772, firstrun_test.go:25), and every verb's `-h` is held by verbhelp_test.go. |
