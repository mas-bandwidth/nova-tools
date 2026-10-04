# nova-work USE rating, nova-tools 1.1.0

Rater: Muse Spark, a friendly AI Assistant
Build: e7b2160bc7263dabbd5ff5f05471414de256e01f
Score: 7.5/10

## Reasons
The question is the owner's: is this a good tool for an AI to use. From the binary and its help alone, the answer is mostly yes. The first run, `nova-work version`, prints the build and exits 0. The help for each verb is long and concrete: `nova-work verify -h` gives the exact MISSING, EXTRA and DRIFT line shapes, the path grammar, the 80-byte value rule, and a copy-paste smallest tree with the offline recipe (`nova-work verify --tree a.lisp --against b.lisp`), so no guessing was needed to start. Two small real jobs ran end to end offline: identical trees give `VERIFY OK ... differences=0` at exit 0, and a copy with `:archived true` gives `VERIFY FAILED` plus one `VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"` at exit 1; a second, different job with an added repo gives `VERIFY MISSING` one way and `VERIFY EXTRA` the other. Four refusals were provoked and each names the problem and the next command: a missing required flag, an unknown flag (it lists every flag), an unknown verb (it lists every verb), and a bad value (`--timeout banana` shows the accepted shape). Where two faults hold at once, both are named at once. `--json` mirrors the typed lines as one object, and `--against` reads no network. What a 10 would need: an offline path for import (its `--dry-run` still needs a live login, so the tool's main job could not be tried here); a line, or a refusal, when the compared trees name different organizations; the repo-ordering rule stated in `verify -h`; and the repo-level want and got values explained there.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-work import --org acme --dry-run` | The run is refused without a login: `IMPORT REFUSED org=acme calls=1 points=0 gh=.../shim/gh api graphql: exit status 2: gh: REFUSED api graphql ...`, exit 2; the help says `--dry-run` reads exactly as the import does and needs the login and the network, so the tool's main job, and its dry run, cannot be tried with no login and only `verify --against` runs offline | Offer a recorded or fake transport for import, or document an offline import path the way verify documents `--against` | M |
| 2 | `nova-work verify --tree a.lisp --against c.lisp` | `c.lisp` changes `:org "acme"` to `:org "beta"` and flips `:archived`; the output counts `differences=1 ... drift=1` and prints only `VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"`, exit 1: the changed organization, the field that sets the scope, draws no line and stays silent | Emit a DRIFT line for the changed organization, or refuse on an organization mismatch before comparing | S |
| 3 | `nova-work verify --tree a.lisp --against d.lisp` | With `d.lisp` holding the same two repos in changed order the run is refused: `VERIFY REFUSED against=d.lisp: workfile: file=d.lisp repos/acme/gadgets: out of order or repeated; run: nova-work verify -h`, exit 2; the refusal names the next command, but `verify -h` never states the ordering rule, so the fault is learned only by hitting it | State the repo-ordering rule in `verify -h` next to the tree shape | S |
| 4 | `nova-work verify --tree a.lisp --against d.lisp` | With `d.lisp` holding one added repo in accepted order the added repo reads `VERIFY MISSING path=repos/acme/gadgets field=repo want="0-issues"`, exit 1 (and `VERIFY EXTRA ... got="0-issues"` the other way); `verify -h` documents the issue-level line shapes but never explains repo-level want and got values, so an AI meets a coded value it must guess at | Add one line to `verify -h` giving the repo-level want and got values and what each means | S |

## Good, keep
Every refusal names the fault, the wanted shape, and the exact next command, and two faults at once draw two lines at once.
The `--against` offline recipe ships a copy-paste smallest tree, and `--json` mirrors each typed line as one object with the same counts.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
|import cannot be tried without a login (6.5 on 2026-10-02) | STILL THERE| `nova-work import --org acme --dry-run` prints IMPORT REFUSED naming the login path, exit 2 |
|value shapes undocumented (6.5 on 2026-10-02) | CHANGED| `nova-work verify -h` documents the MISSING, EXTRA and DRIFT lines, the path grammar and the 80-byte rule; repo-level want="0-issues" stays unexplained |
|tree errors one per run (6.5 on 2026-10-02) | FIXED| `nova-work import` with no flags prints two IMPORT REFUSED lines at once, one naming --org and one naming --out, exit 2 |
|offline drift verification works (10 on 2026-10-02) | STILL THERE| `nova-work verify --tree a.lisp --against b.lisp` prints VERIFY FAILED plus one VERIFY DRIFT line for field=archived, exit 1, with no network read |
