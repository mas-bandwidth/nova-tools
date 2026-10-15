VERDICT: SHOWED (end to end once: .schema -> pinned compiler -> committed Go -> `nova-friend pong` writes it -> ParsePong reads it, with named refusals; internal/ci is red on two class tests that only meet generated files, and the fix lies outside this card's PATHS, see "Proposed diffs")

# Schema tracer: the friend pong

Card schema-tracer-friend-pong.w1 (attempt 1, gen 1, epoch 21), branch
`sprint/schema-tracer-friend-pong.w1.g1.e21` from `origin/v1.3/comms` at `122c01794`.
This is a tracer bullet. It ran once, and what we keep is what it taught. Its code is
not a product unless the owner decides it is.

## The pin (reuse this; the sibling tracer schema-tracer-done-record-fixed does)

- **Compiler:** the schema compiler repository (module path in the go run line below) at commit
  **`4d9941fbc7fd420b560739e48256d5b5e2892cdc`** (main on 2026-10-15). The Go pseudo-version is
  **`v2.5.1-0.20261014163537-4d9941fbc7fd`** and `schema version` prints
  `schema v2.5.1-0.20261014163537-4d9941fbc7fd`.
- **Generate command** (the `//go:generate` line in `pkg/friend/pongwire/generate.go`):

  ```
  go run github.com/mas-bandwidth/schema/v2/cmd/schema@v2.5.1-0.20261014163537-4d9941fbc7fd generate --lang go --out . ../../../schemas/friend
  ```

  Run it with `go generate ./pkg/friend/pongwire`. It needs no `go get`, adds nothing to go.mod
  or go.sum, and works under `GOFLAGS=-mod=readonly`, so I never relaxed `-mod=readonly`. The
  first run took 1m43s because it downloads and builds the compiler. A warm rerun takes 2.9s.
- **Reproducible:** a fresh `schema generate` from a separately built compiler at the same SHA is
  byte-identical to the committed files (all 5 files), and `go generate` leaves `git status`
  clean.
- The schema module is public. So is serialize.go (tags up to v1.9.0, main `a1f6ab77`), but
  this unit does not use it.

## What was built

- `schemas/friend/pong.schema` is a **variable** `table Pong` (D1), defined like this:

  ```schema
  package pongwire

  table Pong
  {
      nonce   string(32)
      queue   uint32 | min = 0, max = 100000
      working uint32 | min = 0, max = 10000
      width   uint32 | min = 0, max = 1024
  }
  ```

  The nonce bound comes from today's nonces: `cmd/nova-friend` draws 6 characters from
  `[a-z0-9]`, or falls back to 6 clock digits. 32 is the plan's bound (UNION.md 4.2) and leaves
  room for a 16-byte hex nonce. The number bounds are also the plan's. There is no version or
  kind field because the form calls for none: the table wire's FORM byte versions the file,
  and a field's identity is the hash of its name. I did not add the plan's `friend` and
  `sent_ms`, because the brief settled the four fields.
- `pkg/friend/pongwire/` holds 5 generated files committed as-is: `pong.go`, `pongTable.go`,
  `pongTableJson.go`, `pongCook.go` and `PongwireView.go`. That is 10,804 lines and 308 KB for 4
  fields. Beside them is the hand-written `generate.go` (the package doc and the go:generate
  line). Under `testdata/` are three golden pongs written by generated **newer writers**
  (`testdata/writers/{newer,changed}/pong.schema`, generated with the same compiler in a
  scratch module), so no wire byte is hand-written.
- `pkg/friend/machine.go` keeps `PongLine` and `ParsePong` with unchanged signatures:
  - `PongLine` returns `EncodePong(...)`. That is `pongwire ` followed by the table wire in
    padded standard base64. When `EncodePong` refuses, `PongLine` returns the legacy line
    instead, and the reader then refuses that line by name.
  - `ParsePong` returns `ReadPong(text)`, with `ok = err == nil`.
  - `ReadPong` reads the schema pong, or, for one release, the legacy line read strictly.
  - New exported names: `PongWirePrefix`, `PongRefused` (with Reason and Detail), the
    refusal-name constants, `Pong`, `EncodePong`, `ReadPong` and `LegacyPongLine`. I avoided
    `Parse*`, `Open*` and `New*` on purpose, so internal/ci's secrets-opener table needs no new
    row.
- **Encoding in the bus body:** padded standard base64 behind a `pongwire ` word. The schema
  docs recommend **no** encoding for text transports (see the improvements section). I chose
  base64 of the binary wire over the table's JSON text form for three reasons:
  - The binary wire keeps the FORM byte, so an unknown newer form is refused by name
    (`unknown_form`). The JSON form has no such byte.
  - Truncation and trailing junk are refused by the runtime's own framing check
    (`wire_damaged`).
  - base64 is what the schema text form itself uses for bytes (SPEC-TABLES.md §16.2: standard
    alphabet, padded on write).

  The `pongwire ` word lets a reader tell the schema pong apart from any other bus body without
  trying to decode it.

## What it showed

### Generator ergonomics

- Building the compiler (`go build ./cmd/schema`) took 1.9s. `check` and `generate` are
  silent on success.
- `uint32 | max = N` is refused with "min without max (or vice versa) is a compile error". An
  unsigned field still has to spell out `min = 0`. Each error line is followed by the compiler
  version line.
- `generate` (with `--verbose`) and `check` print a notice that no `tables.baseline` exists
  ("save-game evolution is unguarded"). Committing one belongs in `schemas/friend/`, outside
  this card's NEW (see "Proposed diffs").
- For a 4-field table the output is the whole table runtime: 7,328 lines in `pongTable.go`,
  plus the cook, JSON and view files. This unit uses none of the cook, JSON or reflection code
  except the descriptors.
- There are three ways to read a table: `PongLoad(region, wire, report)` wants an 8-aligned,
  pre-measured `[]byte` region (I reached it with `unsafe` in the scratch experiment),
  `PongLoadBuilder(&b, wire, report)` with `b.Init()`/`Shutdown()` needs no unsafe, and
  `PongLoadMeasureReason(wire)` is the only call that returns a typed refusal. A safe,
  refusing load takes measure-reason, then builder init, then builder load, then a check of
  four report fields. That is the facade's `readPongWire`.

### The go.mod and import story (D2, answered from this pass)

- **For a table-only unit with no wide values, the generated Go imports the standard library
  only** (`encoding/binary`, `math`, `sync`, `unicode/utf8`, `unsafe`, `strconv`, `fmt`,
  `errors`).
  - It does **not** import serialize.go (the header says "Wide values use serialize.go
    storage", and this unit has none).
  - So the generated package is just a package of the nova-tools module: no new module, no
    `require`, no `replace`, no local module named `pongwire`. `go list -deps` shows stdlib and
    the package itself.
- The `tables/cards` story (a local module `cards` with checkout-relative `replace` directives
  for serialize.go and two historical units) comes from the **cards build**, not from the
  generator.
  - Cards needs the replaces because it imports its own historical generated units and
    because its Makefile writes a go.mod with `serialize.go v0.0.0` and a sibling-checkout
    replace.
  - An adopter generating into its own module needs none of that.
- If a later nova unit has a wide value (int128, or a type that pulls in serialize.go), the
  answer is a plain `require` of the serialize.go module at v1.9.0 (public, tagged; not
  verified here because nothing needed it). It is never a replace.
- **The compiler is never a go.mod dependency.** `go run module@pseudo-version` pins it by
  exact SHA without touching go.mod, which also keeps the AGPL compiler out of nova-tools'
  build graph. The generated output carries `SPDX-License-Identifier: NONE` and the
  output-is-yours grant.
- **Where definitions and generated Go should live:** this pass needed nothing from another
  repo, so the definition and the generated package can live beside their only consumer (here
  `schemas/friend/` and `pkg/friend/pongwire/` in nova-tools).
  - Plan option (a), nova-sprint owning `schemas/nova/` and nova-tools vendoring a copy, is
    needed only for a type that **both** repos read. The pong is one such type: nova-sprint
    has its own `PongLine`/`ParsePong` copy at `pkg/friend/machine.go:184-195`.
  - Option (b), a schema-side adopter module, is **not needed**: the import problem it solves
    does not arise for stdlib-only generated code.
  - My reading: generate into each consuming repo from one `.schema` source of truth, with a
    byte-compare check. That is the plan's C6/C8 shape minus the module plumbing.

### Bytes on the wire (schema vs text pong)

| pong | legacy text line | table wire | bus body (`pongwire ` + base64) |
|---|---:|---:|---:|
| nonce `abc123`, 5/3/8 | 37 | 69 | 101 |
| nonce `abc123`, 0/0/0 | 37 | 27 | 45 |
| 32-byte nonce, every bound (100000/10000/1024) | 75 | 95 | 137 |

The variable wire costs a 1-byte form, a 2-byte header per field, the payload, a terminator,
and an id trailer of **8 bytes per field used** plus its count. For a pong, the trailer is most
of the bytes. With base64 the typical pong is 2.7 times the legacy line. For pongs this does
not matter: they are tens of bytes against the bus's 1 MiB `MaxBody`.

### The refusals gained (names)

- **From the schema runtime** (`TableRefuseReason` words and read-report counters):
  - `wire_damaged`: every truncation of a good wire (all 69 cut points), trailing bytes after
    the wire, an empty body.
  - `unknown_form`: a FORM byte this reader does not know, which covers the unknown newer
    version.
  - `clamped`: a value over its bound, including a 40-byte nonce from a newer writer whose
    bound is 64. The runtime clamps it to 32 and counts it, and the facade refuses any clamp.
  - `kind_mismatch`: a field whose type changed (`width` written as `string(8)`).
  - `malformed`: invalid UTF-8 in the nonce, or framing damage.
- **From the generated descriptors** (bounds read from `pongwire.PongTableFields`, never
  spelled in nova code): `out_of_range` on the write side and for legacy lines. This covers a
  negative or over-bound number, and a nonce over 32 bytes.
- **The facade's own:**
  - `not_a_pong`
  - `base64` (junk after the body, a truncated base64 body)
  - `nonce_absent`
  - `legacy_shape` (a truncated legacy line, trailing junk, fields out of order, a double
    blank, and **the bare `pong <nonce>` that used to read as silent zeros**)
- **Tolerated by rule, and pinned by a test:** a newer writer's unknown field (`sent_ms`) is
  skipped and counted by the runtime, and the pong reads. That is the variable-table promise,
  and the reason D1 chose variable.
- The red baseline before the change: today's `ParsePong` read all six hostile legacy lines as
  pongs (truncated, trailing junk, oversize nonce, negative width, queue over bound, silent
  zeros). Each is now refused by name.

### What was awkward

- No `required`. In a variable table, an absent field and a zero field read the same: a
  zero-valued field is **not written at all** (the zero pong's wire is 27 bytes, nonce only).
  - So "queue absent" reads as `queue=0` with a silent report. **That is today's silent-zeros
    bug again, at the schema level.**
  - The facade can refuse an absent nonce because an empty nonce is never valid. It cannot
    tell an absent number from a zero.
- The generated Go **writer checks no bound and no UTF-8**. `PongSave` wrote `queue=200000` and
  a `0xff 0xfe` nonce without complaint, and the reader then clamped or refused. USAGE.md
  ("Writes are the caller's responsibility") says Go "returns `ErrValueOutOfRange`", but that
  is true of the packet writer, not the table writer. The facade has to check bounds before it
  writes.
- `PongLoad` and `PongLoadBuilder` return nil or false with **no reason**. The reason comes only
  from `PongLoadMeasureReason`, and the report's counters are fields to check one by one. There
  is no `report.Err()` for anything except the build-version mismatch.
- The cards facade has `ReadStrict`, which is exactly "refuse unknown or lossy", but it is
  hand-written for cards. Every adopter rewrites it.
- nova-tools' CI class tests do not recognise Go's `// Code generated ... DO NOT EDIT.` header:
  - `TestGeneralityGuardrail` counts the generated JSON reader's whitespace-skipping method
    (6 occurrences) and one comment as a host name from its token list. It also counts the
    module path in
    the `//go:generate` line as the org name. Imports are exempt; directives are not.
  - `TestEveryNamedRepoPathExists` reads the generated comments' references to the
    compiler's own tree (schema/docs/SPEC-TABLES.md, schema/docs/FIXED-FORM-ALGORITHM.md,
    schema/internal/codegen/golang/functions.go, schema/internal/tablenames) as broken
    nova-tools paths.
- staticcheck reports SA4005 twice in the generated `TableBuildVersionMismatch.Error`
  (`pongTable.go:288`, `:292`).

## Each failure met, as a candidate invariant for the layer model

1. **ReadersBeforeWriters (text to schema cutover).** An old reader (today's `ParsePong`,
   nova-sprint's copy, every deployed `nova-friend` and coordinator) reads `pongwire ...` as
   **not a pong** (`ok=false`, measured). A coordinator on an old binary would therefore never
   see a proof from a friend on this branch, and the friend would look deaf.
   - Invariant: no writer switches a type's wire until every reader of that type dual-reads.
   - **This branch's `PongLine` breaks that by design** (the brief asked for the writer to
     switch). It must not merge or deploy ahead of the readers.
2. **AbsentIsNotZero.** A frame must not read an absent field as a valid zero. Variable tables
   do exactly that today, so every count that liveness depends on needs a presence bit
   (`?T`) or a schema-side `required`.
3. **NoClampIsAccepted.** The runtime clamps by design (a save game wants that). A liveness
   frame must refuse any clamp: a clamped nonce is a different nonce.
4. **WriterRefusesWhatReaderRefuses.** Every refusal the reader can make is checked before the
   write, so a refused pong is never sent. The Go table writer does not do this, so the facade
   does.
5. **RefusalBeforeTransition.** `ParsePong` is called before any state moves (reach proof,
   conformance check). A refused pong is never a proof and never an "ordinary message" proof.
   This matches C9's `FrameRefusedBeforeTransition`.
6. **One reader, two forms, counted.** The legacy reader stays for one release. `Pong.Legacy`
   marks which form was read, so the old-wire counter (C16) has its hook.

## Legacy producers still writing the text pong

- **nova-tools:** `PongLine` writes the legacy line only as its refusal fallback. No other
  producer is left in this tree. `cmd/nova-friend/main.go:759` (help text) still documents the
  legacy line.
- **nova-sprint:** `pkg/friend/machine.go:186` `PongLine` (its own copy), and its `ParsePong`
  at `:191` is the lenient reader.
- **Every deployed nova-friend binary** from v1.3/comms and earlier.

## What the real Layer 1 cards must do differently

- Ship the **reader first** (dual-read in both repos), and switch the writer only after the
  fleet has adopted. This tracer did both at once and is not deployable as-is.
- Settle **absent vs zero** before the definitions are fixed: `?uint32`, or a schema-side
  required-field card.
- Put the strict-read wrapper (measure-reason, load, refuse on malformed, clamped or
  kind_mismatch, tolerate unknown and widened) in **one** shared place: generated by the
  compiler, or one nova helper. Do not repeat it per type.
- Land the internal/ci generated-file exemption and the regenerate-and-diff check **before**
  the first generated package, so generated code is never allowlisted line by line.
- Decide where the pong definition lives across two repos, since both repos parse pongs (D2,
  above).
- The bounds should be justified in the `.schema` itself, as this one is. The plan's 4.2
  bounds held for every nonce seen today.

## Schema improvements wanted (candidate schema-repo cards)

1. **schema-go-table-writer-validates**
   - Evidence: the Go table `PongSave` writes values outside `min/max` and invalid UTF-8
     without complaint, and the reader then clamps or refuses. USAGE.md "Writes are the
     caller's responsibility" says Go returns `ErrValueOutOfRange`, which holds for packets,
     not tables.
   - Smallest fix: `PongSave`/`PongMeasureReason` return `TableRefuseInvalidValue` for an
     out-of-bound value or invalid UTF-8, or the doc states that the Go table writer does not
     check.
2. **schema-unsigned-range-implies-min-zero**
   - Evidence: `uint32 | max = 100000` is refused ("min without max (or vice versa) is a
     compile error (SPEC §4.6)").
   - Smallest fix: on an unsigned type, an omitted `min` is 0. Or keep the rule and make the
     message say "an unsigned field still spells min = 0".
3. **schema-cli-error-noise**
   - Evidence: each compile error line is followed by the version line (three errors print the
     version three times).
   - Smallest fix: print the version once, after the error count.
4. **schema-go-table-strict-load**
   - Evidence: a safe, refusing read takes four calls and four report checks
     (`LoadMeasureReason`, builder `Init`, `LoadBuilder`, then the `Malformed`, `Clamped` and
     `KindMismatch` fields). `PongLoad` returns nil with no reason. The cards facade
     hand-writes the same thing as `ReadStrict`.
   - Smallest fix: generate `<Name>ReadStrict(wire) (*T, error)` returning a typed reason
     (`clamped`, `kind_mismatch`, `malformed`, `unknown_form`, `wire_damaged`), plus a
     `TableReport.Err()` that covers every counter, not only `build_version_mismatch`.
5. **schema-table-required-or-presence-docs**
   - Evidence: a zero field is not written, so absent equals zero (zero pong wire is 27
     bytes). No `required` exists for variable tables.
   - Smallest fix: document "absent reads as default, indistinguishable from a written
     default; use `?T` when absence matters" in the tables section, and consider a
     `| required` attribute that a reader refuses by name when the field is missing.
6. **schema-text-transport-encoding-doc**
   - Evidence: the docs recommend no encoding for carrying a table wire over a text
     transport (bus bodies, logs, HTTP headers). The only base64 rule is the JSON text form's
     (SPEC-TABLES.md §16.2).
   - Smallest fix: a USAGE.md paragraph that says to use padded standard base64 of the binary
     wire (it keeps the FORM byte and the framing check), or the JSON text form when it must
     be human-readable, and what each loses.
7. **schema-go-unit-size**
   - Evidence: a 4-field table generates 10,804 lines and 308 KB. The cook, JSON, message,
     retain and arena code are emitted whether or not they are used.
   - Smallest fix: a `--surface wire` (or similar) generate flag that emits only Measure,
     Save, Load and reflection for a table, or a shared runtime package for the
     table-independent machinery.
8. **schema-generated-go-lint-clean**
   - Evidence: staticcheck SA4005 at `pongTable.go:288` and `:292` (in
     `TableBuildVersionMismatch.Error`, each hex loop's last shift of the value receiver's
     field is never read). The generated JSON reader's whitespace method is named like a host
     name in nova's token list, and in-tree path references in generated
     comments (schema/docs/SPEC-TABLES.md, schema/internal/codegen/golang/functions.go,
     schema/internal/tablenames, each written without the schema/ prefix) trip adopters' path and
     name linters.
   - Smallest fix: use local copies in `Error()`, and write comment references as
     `schema/docs/SPEC-TABLES.md` (or full URLs) so they read as another repo's paths.
9. **schema-go-adopter-recipe**
   - Evidence: CARDS.md "Import and module setup" describes only the local-module-plus-replace
     route. An adopter has to discover that table-only units are stdlib-only and that
     `go run <compiler module>/cmd/schema@<pseudo-version>` (the pin section's line) pins the
     compiler without touching go.mod.
   - Smallest fix: a USAGE.md "Adopting from Go" section with exactly that recipe, plus when
     serialize.go is needed (`require ... v1.9.0`, no replace). This replaces C3.
10. **schema-tables-baseline-notice-location**
    - Evidence: the notice appears on `check` and on `generate --verbose`, but not on a plain
      `generate`. A pipeline that only generates never sees it.
    - Smallest fix: print it on plain `generate` too, or document that `check` is the gate.

## Proposed diffs (outside this card's PATHS, not applied)

1. **internal/ci: exempt generated Go.** In the file walk of
   `internal/ci/generality_class_test.go` (`TestGeneralityGuardrail`) and
   `internal/ci/namedpaths_class_test.go`, skip files whose first line matches Go's standard
   marker:

   ```go
   var generatedRe = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)
   // in each walk:
   if generatedRe.Match(f.Src) { continue } // generated output is checked by regenerate-and-diff, not line linters
   ```

   Also treat a `//go:generate go run <module>@<version>` directive's module path like an
   import spec (exempt) in the generality scan. Until then, the alternative is rows in
   `internal/ci/testdata/generality/` and `testdata/namedpaths_allowlist.txt`, which I do not
   recommend because they allowlist generated lines one by one.
2. **Makefile: the CI-visible regenerate check.**

   ```make
   .PHONY: pongwire-check
   pongwire-check:
   	go generate ./pkg/friend/pongwire
   	git diff --exit-code -- pkg/friend/pongwire
   ```

   Wire it into the CI fast tier (it needs network on a cold module cache to fetch the
   compiler; a warm run takes 2.9s).
3. **`schemas/friend/tables.baseline`:**
   `schema tables-baseline --update --reason "first baseline" schemas/friend`, committed
   beside the definition.
4. **internal/docs catalog.** `TestCommittedMapMatchesTree` refuses the two new directories
   (`schemas` and `docs/research`) as uncatalogued. They need rows in
   `internal/docs/catalog.go` and a `make map` run. The root map then goes over its byte cap:
   `TestRootMapStaysUnderTheByteCap` measures 32787 bytes against 32768, and base is at 32760,
   which leaves 8 bytes of headroom. So the first schema adoption card also has to shorten a
   catalog row, or put `schemas/` under an existing catalogued directory.
5. **`cmd/nova-friend/main.go`:**
   - Call `friend.EncodePong` in the pong verb and refuse at the CLI (exit 2, the
     `PONG REFUSED` line) instead of sending `PongLine`'s legacy fallback.
   - Update the help text at :759 to name the schema pong and the legacy reader.

## Test results (go test -count=1 -p 2, GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1)

```
ok  	github.com/mas-bandwidth/nova-tools/pkg/friend	12.501s
ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-friend	10.086s
--- FAIL: TestEveryNamedRepoPathExists (2.20s)
--- FAIL: TestGeneralityGuardrail (4.95s)
FAIL	github.com/mas-bandwidth/nova-tools/internal/ci	32.839s
```

Last run, after the finding was committed:

```
ok  	github.com/mas-bandwidth/nova-tools/pkg/friend
ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-friend
--- FAIL: TestEveryNamedRepoPathExists
--- FAIL: TestGeneralityText
--- FAIL: TestGeneralityGuardrail
FAIL	github.com/mas-bandwidth/nova-tools/internal/ci
--- FAIL: TestRootMapStaysUnderTheByteCap
--- FAIL: TestCommittedMapMatchesTree
FAIL	github.com/mas-bandwidth/nova-tools/internal/docs   (not in the card's set; run for the record)
```

- `TestEveryNamedRepoPathExists` and `TestGeneralityGuardrail` name only
  `pkg/friend/pongwire/*`: the generated files, and the module path in the go:generate line.
- `TestGeneralityText` names this finding's one remaining module path, which is the pin
  itself, kept because the sibling tracer needs it verbatim.
- The internal/docs pair is proposed diff 4. Proposed diff 1 removes them. Before the commit,
`TestNoTestWritesIntoTheSourceTree` also failed on the uncommitted work tree; it passes once
the work is committed. The red baseline (step 2, today's ParsePong) was 6 of 6 hostile lines
read as pongs.

## Not done

- internal/ci is red:
  - two class tests on generated files only (proposed diff 1)
  - `TestGeneralityText` on the pin's module path in this finding
- internal/docs is red on the two new directories and the root map's byte cap (proposed
  diff 4).
- All of these fixes are outside PATHS.
- There is no CI-visible regenerate check yet (proposed diff 2) and no `tables.baseline`
  (proposed diff 3).
- The nova-sprint copy of `PongLine`/`ParsePong` is untouched, so the two repos now disagree
  on the pong wire. This branch must not merge before readers dual-read (invariant 1).
- No fuzzing. The hostile-input set is the listed cases plus every truncation. The schema
  ROADMAP's Go "Hostile-input checks" cell is still ❌ at `4d9941fb`.
- No TLA+ module. This tracer adds no state machine, and C9 (`TypedFrames`) is the place for
  invariants 1, 5 and 6.
- No per-form read counter (only the `Pong.Legacy` hook).
