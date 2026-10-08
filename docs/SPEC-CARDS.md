# Card lint: the eight admission rules

The card lint is `internal/cardlint`. `Lint` reads a brief and the names and file sizes the caller passes, and returns one finding per defect. The finding's text is the refusal line below. A caller that admits a brief prints that line and admits nothing. Each rule is what the lint checks, the line it refuses with, and the remedy. A defect caught here is one refusal line.

The names are the caller's. The package writes none of its own: a deployment passes the people, friends and machines it knows. File sizes are the sizes at the brief's base. When the caller passes none, the three files below are the sizes this rule was cut against.

## The list

### step-lines-stated

A brief whose `STEP` carries a `VERDICT:` sub-line states the result line the machine parses: `step <n>: <ok|broken|not-done|skipped> <the full 40-hex sha of that step's commit, or -> <one line>`.

Refusal: `CARD REFUSED: step <n> carries VERDICT: but the brief never states the step line the machine parses; run: add THE FINISH FORM with the step-line sentence`

Remedy: add that sentence in the finish form.

### finish-form-present

Every brief carries the paragraph `THE FINISH FORM`. Its verdict line is `Verdict: LAND|HOLD|FAIL`. Its head line is `Head: <40-hex>` or `Head: -`.

Refusal: `CARD REFUSED: no FINISH FORM: the machine reads the report's first two lines before anything else; run: add the FINISH FORM paragraph`

Remedy: add the paragraph.

### one-lanes-home-on-any-lane

A brief with no `WHO:` pin names no lane's home (`~/<name>-working/jobs/`, `outbox/` or `inbox/`), no `Set JOB=... from BRIEF.md`, and no bare `git push` as its end. A pinned brief may name that friend's own home, and may not also carry the fleet start (`do not clone`) beside a `git push` command.

Refusal: `CARD REFUSED: the start and the finish name one lane (<path>) but the card is for whoever the dealer gives it to; run: use the lane-neutral STEP 1 and END`

Remedy: use the lane-neutral start and end. The job's own paths are the ones the lane hands over.

### no-whole-file-read-over-100k

A step that says to read a file whole (`read the docs PATHS names`, `read the docs named in PATHS`, `read AGENTS.md`, or `read <file>`) is refused when that file is over 100 KB. A step that names a section (`section`, `grep -n`, `sed -n`) is not a whole-file read. With no sizes from the caller, the files and sizes are `docs/SPEC-SPRINT.md` (667502 bytes), `docs/CLI.md` (274352 bytes) and `cmd/nova-sprint/verbs.go` (188036 bytes).

Refusal: `CARD REFUSED: STEP <n> reads <file> whole (<size> KB at BASE) against a 400,000-token budget; run: name the section (grep -n '^#' <file>, then sed -n '<from>,<to>p')`

Remedy: name the section.

### no-truncated-text

A prose paragraph ends in `.`, `:`, `)`, `?`, `!`, `>` or a closed backtick, and holds no unclosed backtick. A quoted block also holds a sentence end in its last 80 characters. A heading, a header line, a `STEP` line and a table row are not prose.

Refusal: `CARD REFUSED: paragraph <n> ends mid-sentence ("<last 20 chars>"); run: paste the whole finding, or state it whole in fewer words`

Remedy: state the finding whole.

### examples-are-placeholders

A `FORM` table's data row holds placeholders only (every cell has an angle bracket). An example line in a card whose `PATHS` name `cmd/<tool>/` names no other `nova-*` tool.

Refusals: `CARD REFUSED: the FORM's example row is concrete (<cell>): it is copied and read false; run: make it the shape only, <the verb ...> <file>:<line> ...` and `CARD REFUSED: the example names <other> in a card about <tool>`

Remedy: the example is the card's own shape and the card's own tool.

### tla-edits-carry-tlacheck

A brief whose `PATHS` or `NEW` names a `tla/` model has `tla/RUNS.tsv` on `PATHS` and on `SHARED`, a `TEST` line that names a Go test, and a `STEP` that runs `tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv` on a TLC record machine, with the skip `TLC records owed`. The finding is a refusal, never a note.

Refusal: `CARD REFUSED: PATHS name tla/ but no STEP runs tlacheck merge --keep (or tla/RUNS.tsv is not SHARED); run: add the TLC record step`

Remedy: add the record step, the `SHARED` line and the real test.

### no-names-outside-quotes

Outside double quotes, and on every line, not only the first paragraph, the brief holds none of the names the caller passed. A `WHO:` line is the pin and is not a name. A lane home is the lane rule's finding, not this one.

Refusal: `CARD REFUSED: line <n> names <name> outside a quotation; run: say "a Linux bench", "the coordinator's machine", "the owner", "a friend", or By: <your own name>`

Remedy: use the generic word.
