# Nova Tools terminology

Welcome — this guide is a plain-language map of the words you will meet in the
specs, each linked to the page that defines it so you can jump straight to the
source.

The rules of naming: one word for one thing, in every document, help text and
comment; the glossaries define the words, one per release —
[GLOSSARY.md](GLOSSARY.md) for nova-tools 1.2.0 and
[sprint/GLOSSARY.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/sprint/GLOSSARY.md) for nova-sprint 1.0.0. A word that is
replaced is retired: `internal/docs/testdata/retired-words.txt` lists each with
what replaced it, and `TestRetiredWordsAppearOnlyInRecords` fails when a tracked
file uses one outside the dated records (the changelog, the resolutions, the
release notes, the ratings and the ledgers under `testdata/`). A glossary entry
names what a word replaced after `Replaces:`, the one place the retired word may
stand. A place that still uses a retired word is a row of the ledger in that
file, and the ledger only shrinks.

- **adoption** — choosing to take a tool into your workflow; nothing in this repo
  asks you to adopt everything at once. `nova-update adoption` prints each
  friend's own choice, and a tool with no row is absent, never adopted.
  ([USAGE.md](USAGE.md#usage-and-adoption-guide) · [SPEC-UPDATE.md](SPEC-UPDATE.md))
- **beat** — a machine's heartbeat in Redis, `bench:<name>:beat`, carrying its
  measured facts. `nova-config machine list` prints them live beside the declared
  fields, and `nova-update report --store` reads each bench's build from it.
  ([SPEC-CONFIG.md](SPEC-CONFIG.md) · [SPEC-UPDATE.md](SPEC-UPDATE.md))
- **bench** — a machine of the fleet that runs work or CI, named by its tailnet
  host; its declared facts are a `nova-config` machine row and its measured facts
  come from its beat. ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **class test** — a test that reads this repository's own text and refuses a
  shape wherever it stands, so a lesson is a rule and not a story; each is
  indexed with its rule, its allowlist, its remedy line and its narrowings.
  ([SPEC-CI.md, The class tests](SPEC-CI.md#the-class-tests))
- **coordinator** — the friend who holds the coordinator role, the one field of
  `nova-config`'s sprint row; the coordinator machine, where the coordinator's
  loops run, is the fleet row's. ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **dogfood shape** — the issue shape the family files against its own tools:
  tool, command, verbatim output, expected, smallest fix. The shape is the
  contract; the label is optional. ([SPEC-UPDATE.md](SPEC-UPDATE.md))
- **friend** — a named participant you exchange notes with, the coordinator
  included; the role is ownership, never an exemption. Her configuration is a
  `nova-config` friend row (slots, tiers, roles, width, mode); what she would just know is
  runtime data she reports herself. ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **OK** — the verdict a successful verb line carries as its second token:
  `<TOKEN> OK`, on stdout. A count stands where a list would be.
  ([SPEC.md, Conventions](SPEC.md#conventions))
- **receipt** — one line appended to `from-<me>/RECEIPTS` recording that a note
  arrived. It is not an approval, a reply, or proof anybody read the body.
  ([SPEC.md, Conventions](SPEC.md#conventions))
- **REFUSED** — the verdict a tool prints when it could not run or says NO:
  `<TOKEN> REFUSED: <reason> (<remedy>)`, on stderr, exit 2 (exit 1 where the
  state is a NO). ([SPEC.md, Conventions](SPEC.md#conventions))
- **slot** — the unit of parallelism: a machine's `slots` is how many cards it
  may run at once, its ceiling, and a friend's `slots` is how wide she wants to
  run under that ceiling. ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **STALE** — `nova-update check`'s verdict for an installed version older than
  the latest its source publishes; a version ahead of the latest is NEWER, and a
  pair with no order between them is DIFFERENT, never STALE.
  ([SPEC-UPDATE.md](SPEC-UPDATE.md))
- **two-minute rule** — every CI job is capped at two minutes, on every
  platform, and the target is under one. ([SPEC-CI.md, The class tests](SPEC-CI.md#the-class-tests))
- **wall** — the kernel-enforced filesystem boundary a job runs inside, with
  separate read and write permissions. ([SPEC-SANDBOX.md, The rules, numbered](SPEC-SANDBOX.md#the-rules-numbered))
