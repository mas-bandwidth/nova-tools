RESULT: dogfood-opencode-card-b sha=5844884e267c tier: flash
REPO: mas-bandwidth/nova-tools
BASE: sprint/mechanical-2026-10-02

# nova-card dogfood, 2026-10-06 (opencode)

Tool: nova-card. Build: `nova-card devel linux/amd64 go1.26.6`.
Run cold, from the tool's own help and the tool's page in `docs/CLI.md` only.

## 1. `nova-card help` shows usage banner

**Command:**

    nova-card help

**Printed:**

    nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
    nova-card is pre-alpha: not ready for production use.

**Expected:** Clear usage with examples for a new user

**Grade:** NEXT

## 2. `nova-card -h` shows usage banner

**Command:**

    nova-card -h

**Printed:**

    nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
    nova-card is pre-alpha: not ready for production use.

**Expected:** Clear usage with examples for a new user

**Grade:** NEXT

## 3. `nova-card version` prints build identity

**Command:**

    nova-card version

**Printed:**

    nova-card devel linux/amd64 go1.26.6

**Expected:** Build identity

**Grade:** NEXT

## 4. `nova-card template` prints the card template

**Command:**

    nova-card template

**Printed:**

    RESULT: <card id>
    REPO: <repo>
    BASE: <base>

**Expected:** Bare card template

**Grade:** NEXT

## 5. `nova-card lint` requires --card flag

**Command:**

    nova-card lint

**Printed:**

    nova-card lint REFUSED: wants --card <file>, one per brief, and nothing else

**Expected:** Refusal with remedy

**Grade:** NEXT

## 6. `nova-card lint --card` validates a brief

**Command:**

    nova-card lint --card ./cards/serial-tests-cmd-nova-bus-issue1451.md

**Printed:**

    LINT OK file=./cards/serial-tests-cmd-nova-bus-issue1451.md

**Expected:** Lint result

**Grade:** NEXT

## 7. `nova-card generate` requires --out flag

**Command:**

    nova-card generate --from ledger --ledger serial-tests

**Printed:**

    nova-card generate REFUSED: wants --out <dir>

**Expected:** Refusal with remedy

**Grade:** NEXT

## 8. `nova-card generate --from ledger` with --dry-run

**Command:**

    nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards --dry-run

**Printed:**

    CARDS OK dir=./cards cards=5 waves=2 tier=flash shared-paths=yes dry-run=yes (nothing written)

**Expected:** Card listing with summary

**Grade:** NEXT

## 9. `nova-card generate --from ledger` without --dry-run

**Command:**

    nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards

**Printed:**

    CARDS OK dir=./cards cards=5 waves=2 tier=flash shared-paths=yes

**Expected:** Card files written to output directory

**Grade:** NEXT

## 10. `nova-card generate --from help`

**Command:**

    nova-card generate --from help --tool nova-bus --out ./cards --dry-run

**Printed:**

    CARDS OK dir=./cards cards=1 waves=1 tier=pro dry-run=yes (nothing written)

**Expected:** Help-based card generation

**Grade:** NEXT

## 11. `nova-card help lint` shows lint help

**Command:**

    nova-card help lint

**Printed:**

    effect: inspection: reads, writes nothing

**Expected:** Verb-specific help

**Grade:** NEXT

## 12. `nova-card help generate` shows generate help

**Command:**

    nova-card help generate

**Printed:**

    effect: local write: creates --out and writes one .md per card and manifest.tsv into it; nothing when a brief is red; --dry-run plans, lints and prints the manifest, and writes nothing

**Expected:** Verb-specific help

**Grade:** NEXT

## 13. nova-card docs page read

**Command:**

    cat docs/CLI.md | grep -A 20 "nova-card"

**Printed:**

    nova-card generate --from ledger --ledger <name> --repo-dir <dir> --out <dir>
    nova-card generate --from findings --file <tsv> --out <dir>
    nova-card generate --from help --tool <name>

**Expected:** Documentation of nova-card usage

**Grade:** NEXT

READ 8/10 - The tool has all expected verbs and help is available. The TSV output for --dry-run is direct without additional summary formatting.

USE 8/10 - The tool works well for its stated purpose of generating cards from ledgers and help output. The generate, lint, new, and template verbs form a coherent workflow. Missing: --max flag could be more visible in usage.

urgent=0 next=13
