# nova-memory READ rating, nova-tools 1.1.0

Rater: qwen3.8-flash (opencode)
Build: c28448a54d56
Score: 8/10
README: 8/10

## Reasons

The README's sentence for this tool (README.md:33) is the banner's line 1 word
for word (cmd/nova-memory/main.go:49): search your own markdown notes, and
check a draft against what they already say. It is true of the code: eight
verbs, one in-memory index, nothing written. The cold path is a quickstart over
an included corpus with no store and no invented setup, and docs/CLI.md:339-424
is the best first-run writing in the set: a real transcript, what flags want,
what the output means, and the limits named plainly.

- Confused first at docs/CLI.md:379: a run of `nova-memory check` prints lines
  led by MEMORY, not by the verb that ran. The family rule says the verb leads
  (AGENTS.md section 2), and CLI.md's Reading-it paragraph (docs/CLI.md:416)
  does not say the exception exists; only docs/SPEC.md:299 and the code
  (cmd/nova-memory/main.go:228) hold it.
- Bored first at docs/CLI.md:424: the why-it-exists paragraph stands nearly
  unchanged a third time on the reading path, after cmd/nova-memory/main.go:3-9
  and docs/SPEC.md:2357-2363. By the third copy the rationale slows the reader
  it already convinced.
- Doubted first at cmd/nova-memory/main.go:58: the pasted SEARCH CAL
  `score=1.46` reads like a constant until the next sentence resolves it. That
  sentence is now exact (a hit's score= compares with the band only when its
  score-channel= names the same channel) and a test runs the verb to hold the
  paste to the run (cmd/nova-memory/verbhelp_test.go:72-80); what nothing says
  yet is that a fixed probe scores higher, and collapses the band, on a corpus
  whose vocabulary leans toward the probe's own words.

The rest holds up under a cold read: the entry point (cmd/nova-memory/main.go:
254), the verbs (main.go:226) and the data (internal/memindex/memindex.go:68-98)
are found inside a minute; names are a stranger's words (quickstart, search,
check, verify, eval, boot); comments say why in the present tense throughout;
and the tests teach the contract rather than merely guard it — the README block
and the first-run transcript are compared with what the tool prints
(cmd/nova-memory/firstrun_test.go:139, firstrun_test.go:686), every exit-code
claim in the help is run beside the verb it describes
(cmd/nova-memory/verbhelp_test.go:34-66), and the index is deterministic twice
built (internal/memindex/memindex.go:33-37). Is it a good tool to use, code to
trust and enjoy working in? Yes, and the score holds back two points for the
weight below: a main.go of 1,405 lines that hand-rolls what the shared skeleton
gives by construction, four verbs that print their lines and build their JSON
at two sites, and a refusal that round-trips through its own stderr text.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:851 | stats, boot, verify and eval print their lines by hand (main.go:859, main.go:898, main.go:1231, main.go:1342) and build the same facts again for --json at a second site; retrieval.go renders search and check from one value but through two written paths (cmd/nova-memory/retrieval.go:35). The two renderings stay in sync by eye, which is the drift the one-value rule exists to prevent (AGENTS.md section 2). | build one tool.Out per verb and let the skeleton's line renderer (pkg/tool/out.go:189) print the typed lines, keeping hand-rendered tails only for the receipt's file:line and quoted snippet | M |
| 2 | cmd/nova-memory/main.go:277 | under --json a refusal is formatted into a stderr buffer as one escaped line, split back out by a line loop, and carried as Why text with its own `TOKEN REFUSED` prefix inside the envelope's refused status — the tool parses its own output | have refuse hand the reason and remedy to the caller as data and render once through tool.Refuse, so no text is scraped back into structure | M |
| 3 | cmd/nova-memory/main.go:228 | check's lines lead with MEMORY while every other verb leads with its own name; the exception is specced (docs/SPEC.md:299) but the place a visitor meets it first, the CLI.md transcript (docs/CLI.md:379) and its Reading-it paragraph (docs/CLI.md:416), never names it, so a reader greps for a token the tool does not print | say the exception in one sentence of docs/CLI.md's Reading-it paragraph, with the reason it keeps the binary's token | S |
| 4 | internal/memindex/memindex.go:311 | a paragraph's start line advances by a fixed 2 per split separator, so a run of three or more blank lines leaves a one-newline part that advances 3 where the file moved 1: receipt file:line addresses run a line high for every blank after the second, pointing the reader at the wrong source line | advance by the newlines the separator actually consumed, and hold a corpus with a run of blank lines in the receipt-address test | S |
| 5 | internal/memindex/memindex.go:183 | frontmatter reads name and type from any fenced leading block, while stripFrontmatter treats only a YAML-shaped block as frontmatter (internal/memindex/memindex.go:219): a thematic break holding a name line has its text indexed as prose and its name carried on receipts, half the disagreement docs/SPEC.md:2387 rules out | one shared shape guard used by both readers, so a block is frontmatter for both or for neither | S |

## Good, keep

- The exit codes line names each verb's answer, says each ran here, and a test
  runs every claim beside the verb it describes
  (cmd/nova-memory/main.go:149-154, cmd/nova-memory/verbhelp_test.go:34-66).
- quickstart echoes each command line and then runs that same argv through the
  dispatch a shell reaches (cmd/nova-memory/main.go:644), so what a reader
  pastes is what executed.
- The calibration probe is re-scored on the caller's own corpus every run and
  moves with the schema version (cmd/nova-memory/main.go:207-212,
  cmd/nova-memory/main_test.go:237), so the noise band is never someone
  else's stale number.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| READ 2026-10-02 at 1aac13259: a 1,394-line main.go hand-prints every verb twice | CHANGED | the file stands at 1,405 lines and four verbs still render at two sites (cmd/nova-memory/main.go:851), but search and check now share one value across both renderings (cmd/nova-memory/retrieval.go:15) |
| READ 2026-10-02 at 1aac13259: it scrapes its own stderr for --json | STILL THERE | cmd/nova-memory/main.go:277-288 buffers the refused lines and splits them back into Why strings |
| READ 2026-10-02 at 1aac13259: the universal unrelated-noise calibration claim is not supported | CHANGED | the CAL line is pasted as printed and its sentence says which score= compares with it, pinned by a run (cmd/nova-memory/main.go:58-65, cmd/nova-memory/verbhelp_test.go:72-80); the probe's vocabulary can still overlap a corpus's own |
| The README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | the tool's row equals the banner's line 1 word for word (README.md:33, cmd/nova-memory/main.go:49) and its first-run pointer prints what it promises (cmd/nova-memory/firstrun_test.go:139); this rater reads it 8/10 |
