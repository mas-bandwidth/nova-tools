# nova-doctor dogfood, 2026-10-06 (antigravity)

Reviewer: Antigravity. Read as a stranger: `nova-doctor -h`, `nova-doctor help`, `nova-doctor <verb> -h`, and the page under `docs/` (`docs/SPEC-DOCTOR.md`, and the nova-doctor section of `docs/SETUP.md`). Built from `sprint/mechanical-2026-10-02` at `6ff31d5b369cc7402b39d36d6e898865007f3a7c` and used as `nova-doctor v1.0.1-0.20261007160313-6ff31d5b369c linux/amd64 go1.26.6`. Every verb (`run`, `version`, `help`) once with its real flags, against a scratch `HOME` and a scratch `PATH` of fake `nova-*` tools and a fake `go`, the refusals too. The sandbox tracer blocked network connects, and the seat was not loaded.

## Findings

1. A path the refusal tells you to pass runs the checks and never names the file.
   Command: `nova-doctor ./note.txt`
   Printed:
   ```
   DOCTOR gosdk fail no go on PATH and a bench builds and tests here fix: install the Go toolchain go.mod's toolchain line names on this bench (docs/SETUP.md, dep-go-sdk-b.w2)
   DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)
   DOCTOR secrets fail sops is not on PATH and NOVA_SECRETS_SOPS names no executable there fix: install sops (https://github.com/getsops/sops) and put it on PATH, or set NOVA_SECRETS_SOPS to it (docs/SETUP.md, dep-secrets-bb.w4)
   ```
   `nova-doctor nosuchverb` prints `DOCTOR REFUSED: "nosuchverb" is no verb and no file; the verbs are run, version, and a file is given by its path (./nosuchverb)`. `./note.txt` is a file. I expected that file to be read, or a refusal that says it is not a verb. The checks ran (exit 2), `note.txt` was not in the output, and the file was unchanged.
   Grade: URGENT.

2. `help --json` is run's text help, and a flag before `help` runs the checks.
   Command: `nova-doctor help --json`
   Printed:
   ```
   usage: nova-doctor run [flags]
   checks: gosdk, providers, secrets, self, tailnet
   example: nova-doctor --local
   ```
   Exit 0. The banner says every verb but `run` takes `--json`, and `nova-doctor version --json` is a JSON object. I expected JSON of the help text, or the same banner bare `nova-doctor help` prints. `nova-doctor --json help` did not print help: it ran the checks and printed the results object (exit 2). `nova-doctor help --local` printed this same run usage and did not say which checks were skipped.
   Grade: URGENT.

3. With no `go`, the machine is a bench even when the seat file is the one the setup page names.
   Command: `nova-doctor run --check gosdk`
   Printed:
   ```
   DOCTOR gosdk fail no go on PATH and a bench builds and tests here fix: install the Go toolchain go.mod's toolchain line names on this bench (docs/SETUP.md, dep-go-sdk-b.w2)
   ```
   `HOME` was a scratch directory whose `nova/seat.env` was the empty file at the path `docs/SETUP.md` tells you to source (`~/nova/seat.env`). Nothing opened or statted that file. The page says the gosdk check reads the role from the machine's seat, and that a coordinator with no `go` is ok. I expected ok, or a fail that names the seat file. I expected not to be told to install Go.
   Grade: URGENT.

4. With `--check`, `--local` omits the skipped fleet checks that bare `run --local` prints.
   Command: `nova-doctor run --check gosdk --json --local`
   Printed:
   ```
   {"exit":0,"results":[{"check":"gosdk","dependency":"the Go toolchain","status":"ok","evidence":"go 1.26.6, go.mod toolchain 1.26.6, GOCACHE /home/<user>/scratch/gocache writable, GOFLAGS -mod=readonly"}]}
   ```
   `docs/SPEC-DOCTOR.md` says `--local` prints `DOCTOR local skipped=<name,name> (...)` and `--json` includes `"skipped":[...]`. While bare `nova-doctor run --json --local` includes `"skipped":["providers","secrets","tailnet"]`, specifying `--check gosdk` with `--local` drops the `skipped` field entirely.
   Grade: NEXT.

5. A tie is reported as one tool against a release that only one tool has.
   Command: `nova-doctor run --check self` (`nova-alpha` v9.9.9 and `nova-beta` v1.2.0 on `PATH`)
   Printed:
   ```
   DOCTOR self fail the tools are not one release: nova-beta=v1.2.0 differ from v9.9.9 (1 tools) fix: nova-update apply --file <manifest> nova-beta --version v9.9.9
   ```
   The page says the evidence names every tool that differs from the version most of the tools report. Neither version is most. I expected both names, or a line that says it is a tie. A 2-to-1 split did the right thing (`nova-odd=v9.9.9 differ from v1.2.0 (2 tools)`, and the fix moves the odd one). The tie picks the higher version and says `(1 tools)`.
   Grade: NEXT.

6. The banner's `--max` is not a flag, and `run -h` names the check flag `<help run>`.
   Command: `nova-doctor version --max 0`
   Printed:
   ```
   VERSION REFUSED: unknown flag --max; the flags of version are --json; run: nova-doctor version -h
   ```
   The banner says a verb that lists takes `--max` (default 20, 0 lists all). `run --max 1` is the same refusal for run's flags, which are `--check`, `--json`, `--local`, `--strict`. I expected `--max` to exist, or the banner not to mention it. `nova-doctor run -h` prints `--check <help run>`, and `nova-doctor run --check "help run"` refuses: no check named "help run"; the checks are gosdk, providers, secrets, self, tailnet; run: nova-doctor help. The top usage says `--check <name>`.
   Grade: NEXT.

7. `run -h` lists `gosdk`, and `docs/SPEC-DOCTOR.md` does not.
   Command: `nova-doctor run -h`
   Printed:
   ```
   usage: nova-doctor run [flags]
   checks: gosdk, providers, secrets, self, tailnet
   example: nova-doctor --local
   ```
   The spec's checks section names only `self`. A bare `nova-doctor` runs `gosdk` too, and that check is what fails when `go` is missing. I expected the spec page to name `gosdk` the way `docs/SETUP.md` and this help do.
   Grade: NEXT.

8. The self fix is not a command until someone fills in `<manifest>`.
   Command: `nova-doctor run --check self` (`nova-one` and `nova-two` at v1.2.0, `nova-odd` at v9.9.9)
   Printed:
   ```
   DOCTOR self fail the tools are not one release: nova-odd=v9.9.9 differ from v1.2.0 (2 tools) fix: nova-update apply --file <manifest> nova-odd --version v1.2.0
   ```
   The odd tool is the right one. I expected a manifest path, or the page to name the file. `<manifest>` is the placeholder in `docs/SPEC-DOCTOR.md` as well, and it is not a file here. The JSON form of the no-tools fix escapes it as `\u003cmanifest\u003e`.
   Grade: NEXT.

What did hold: `version` and `version --json`. Two tools on one version, `self` is ok. A 2-to-1 split names the odd tool. An unknown check and an unknown flag name the real set and a `run:` line. In a checkout, `gosdk` is ok when `go` is 1.26.6, `GOCACHE` is writable, and `GOFLAGS=-mod=readonly`; without that flag it is a warn and exit 0, and `--strict` makes the warn exit 1. An unwritable `GOCACHE` fails and names an export. A fail does not stop the other check. Nothing in the scratch home or `GOCACHE` was written.

READ 6/10 — `run -h`, the exit table, and the SETUP section match the checks that ran, and a bad flag names the real ones; the spec omits `gosdk`, and the banner's `--json` and `--max` do not match the binary.

USE 4/10 — a matched `self` check and the `gosdk` warn path do what the page says and write nothing, but a file path and `help` after a flag run the checks, and the seat file the setup page names is ignored so a machine with no `go` is told it is a bench and to install Go.

urgent=3 next=5
