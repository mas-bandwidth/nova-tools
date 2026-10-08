# nova-doctor dogfood — opencode-2, 2026-10-06

Read cold as a stranger: `nova-doctor -h`, `nova-doctor help`, `nova-doctor help run`, `nova-doctor run -h`, `nova-doctor version -h`, and the tool's page under `docs/` (`docs/SPEC-DOCTOR.md`, beside the nova-doctor section of `docs/SETUP.md`). Built on a Linux bench (`<bench>`) in the staged checkout at `7acb90e18a764f0e728cd5ed701196a34405a824` with `go build -o $JOB/bin/nova-doctor ./cmd/nova-doctor` (never the installed binary) and run as `nova-doctor v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`; the made-up actor for this record is `--actor boss`. Every verb (`run`, `version`, `help`) and every flag (`--check`, `--local`, `--strict`, `--json`) ran with its real flags against a scratch `HOME`, a scratch `PATH` of made-up `nova-*` tools and `sops`, an empty temp directory and a scratch secrets store; the refusals ran too. Nothing outside the scratch directories was changed, and no command wrote anything.

## Findings

1. `NOVA_SECRETS_KEY=AGE-SECRET-KEY-1FAKEFAKEFAKE nova-doctor --check secrets`
   Printed:
   ```
   DOCTOR secrets fail the age key AGE-SECRET-KEY-1FAKEFAKEFAKE is not readable fix: run nova-secrets keygen --as <seat> --key AGE-SECRET-KEY-1FAKEFAKEFAKE --age-keygen <age-keygen> (docs/SETUP.md, dep-secrets-bb.w4)
   (one line printed)
   ```
   I expected the `secrets` check to print names and modes only, never a value: that is what `docs/SETUP.md`'s nova-doctor section promises, and what a key file under `NOVA_SECRETS_KEY` should get. The check echoes the whole value of `NOVA_SECRETS_KEY` in the evidence and again in the fix line, so a key material set in that variable is written to the terminal, to any log and to any `--json` capture, and `--json` repeats it too.
   Grade: URGENT (a secret value is printed; the page says it never is)

2. `nova-doctor ./note.txt` (the file exists in the working directory)
   Printed:
   ```
   DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)
   DOCTOR harness ok no friend rows at /tmp/ndprobe/.nova/friends
   DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)
   ```
   I expected the file operand to be read, because the tool's own refusal advertises it: `nova-doctor frobnicate` prints `DOCTOR REFUSED: "frobnicate" is no verb and no file; the verbs are run, version, and a file is given by its path (./frobnicate)`. Instead `./note.txt` is never named, opened or mentioned, and the six checks run (exit 2).
   Grade: URGENT (an input the refusal announces is silently dropped)

3. `nova-doctor run extra`
   Printed:
   ```
   DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)
   DOCTOR harness ok no friend rows at /tmp/ndprobe/.nova/friends
   DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)
   ```
   I expected a refusal in the shape the sibling verb gives for the same mistake: `nova-doctor version extra` prints `VERSION REFUSED: takes no positional arguments, got "extra" (flags come before arguments); run: nova-doctor help`. `run` instead drops the extra word and answers with a full plausible report the caller did not ask for.
   Grade: URGENT (an operand is silently dropped; the sibling verb refuses it)

4. `nova-doctor --json help`
   Printed:
   ```
   {"exit":2,"results":[{"check":"gosdk","dependency":"the Go toolchain","status":"fail","evidence":"no go.mod here: the bench's toolchain is not named","fix":"run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)"},{"check":"harness","dependency":"the friend harnesses","status":"ok","evidence":"no friend rows at /tmp/ndprobe/.nova/friends"},{"check":"providers","dependency":"the model providers and their routes","status":"fail","evidence":"the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store","fix":"nova-secrets names --store \u003cdir\u003e --as \u003cseat\u003e (nova-up --local writes the seat's store and its environment)"},{"check":"secrets","dependency":"the secrets store and this machine's key","status":"fail","evidence":"NOVA_SECRETS_KEY is not set, so this machine has no age key to open the store with","fix":"set NOVA_SECRETS_KEY to this machine's age private key; nova-up --local writes it into seat.env (docs/SETUP.md, dep-secrets-bb.w4)"},{"check":"self","dependency":"the nova tools on PATH","status":"fail","evidence":"nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line","fix":"nova-update apply --file \u003cmanifest\u003e nova-loop"},{"check":"tailnet","dependency":"the tailnet","status":"fail","evidence":"the machine inventory could not be read: nova-config machine list: exit status 2","fix":"source the seat file (`set -a; . ~/nova/seat.env; set +a`), then run nova-doctor again (docs/SETUP.md, dep-tailnet-b.w4)"}]}
   (one line printed)
   ```
   I expected a request for the help door to print help, or at worst a refusal naming the door: `nova-doctor help` and `nova-doctor -h` both print the banner on stdout at exit 0. A flag before `help` swallows it and the six checks run instead, so a request for documentation does work; `nova-doctor help --json` is the mirror and prints `run`'s text help, ignoring `--json`, although the banner says every verb but `run` takes it.
   Grade: URGENT (a help request runs the checks instead of printing help)

5. `nova-doctor --check gosdk` (run from an empty temp directory, with `go` on `PATH`)
   Printed:
   ```
   DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)
   (one line printed)
   ```
   I expected `ok` or at worst a `warn` where the Go toolchain is installed and `GOFLAGS=-mod=readonly` is set, because `docs/SETUP.md` lists the `gosdk` failures as `go` absent, the wrong version, or an unwritable `GOCACHE`, and the same machine answers `ok` inside a checkout. The verdict depends on the working directory and the fix asks a reader of the released binary for a checkout of this repository that is not on the machine, and `--local` does not skip the check, so there is no clean run outside one.
   Grade: URGENT (a failure the page does not list, with a fix the reader cannot run)

6. `nova-doctor --check gosdk` (a scratch `HOME` whose `nova/seat.env` names `NOVA_SECRETS_SEAT=coordinator`, the variable not exported, and no `go` on `PATH`)
   Printed:
   ```
   DOCTOR gosdk fail no go on PATH and a bench builds and tests here fix: install the Go toolchain go.mod's toolchain line names on this bench (docs/SETUP.md, dep-go-sdk-b.w2)
   (one line printed)
   ```
   I expected `ok`, because `docs/SETUP.md` says the `gosdk` check reads the role from the machine's seat and a coordinator with no `go` is `ok`; the seat file is the documented form of the seat. The check reads only the exported `NOVA_SECRETS_SEAT`, a name neither the help nor the page ever prints, so a coordinator with the seat file present is judged a bench and told to install Go, the one thing the page says its machine must not do. With `NOVA_SECRETS_SEAT=coordinator` exported the same run answers `ok no go on PATH: the coordinator's machine runs no go build or test (the bench rule)`.
   Grade: NEXT (the role variable is undocumented, so the seat file looks ignored)

7. `nova-doctor --check` (no value)
   Printed:
   ```
   RUN REFUSED: --check needs a value: it wants run only this check (repeatable); the names are in help run; run: nova-doctor run -h
   (one line printed)
   ```
   I expected the value's placeholder to read `<name>`, the word `docs/SPEC-DOCTOR.md`'s usage prints (`--check <name>`), and the "it wants" clause to read the flag's description. `nova-doctor run -h` prints the flag as `--check <help run>`, so both the list line and this refusal read as if the value were the help command and a reader cannot tell what to type.
   Grade: NEXT (the flag's value slot is a phrase, not a value)

8. `nova-doctor version --max 0`
   Printed:
   ```
   VERSION REFUSED: unknown flag --max; the flags of version are --json; run: nova-doctor version -h
   (one line printed)
   ```
   I expected the banner's sentence "A verb that lists takes `--max <n>` (default 20, 0 lists all) and says MORE for the rest" to name no flag this tool has. Every verb refuses `--max` (`run --max 1` names `--check`, `--json`, `--local`, `--strict`), and no verb lists anything, so a stranger is sent after a flag that does not exist.
   Grade: NEXT (the banner advertises a flag no verb takes)

9. `nova-doctor --check self` (a scratch `PATH` with `nova-alpha` v1.2.0 and `nova-odd` v9.9.9, a one-to-one split)
   Printed:
   ```
   DOCTOR self fail the tools are not one release: nova-alpha=v1.2.0 differ from v9.9.9 (1 tools) fix: nova-update apply --file <manifest> nova-alpha --version v9.9.9
   (one line printed)
   ```
   I expected the evidence to name every tool that differs "from the version most of the tools report", as `docs/SPEC-DOCTOR.md` says. A one-to-one split has no most, so the line should say it is a tie; instead it silently picks v9.9.9, calls the other side one tool, and the fix moves `nova-alpha` to a version only half the tools report. A two-to-one split does the right thing and names the odd tool.
   Grade: NEXT (a tie is reported as if one side were the majority)

10. `nova-doctor --check self` (a scratch `PATH` with `nova-broken` and `nova-broken2`, which exit 2 for `version`, and `nova-alpha` v1.2.0)
   Printed:
   ```
   DOCTOR self fail nova-broken, nova-broken2 did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-broken
   (one line printed)
   ```
   I expected the fix to cover the tools the evidence names, because the frame says each result that is not `ok` carries the one line that fixes it and a result with no fix line is turned into a fail that says so. The evidence names both broken tools; the fix names only the first, and `nova-broken2` is left to the reader.
   Grade: NEXT (the fix names one of the two broken tools)

11. `nova-doctor run -h`
   Printed:
   ```
   usage: nova-doctor run [flags]
   checks: gosdk, harness, providers, secrets, self, tailnet
   example: nova-doctor --local
   ```
   I expected `docs/SPEC-DOCTOR.md`'s checks section to name the checks the binary registers. The page carries a single `### self`, while `run -h`, an unknown-check refusal and the dispatcher all name six; a reader who follows the page as the contract is told about one of the tool's six checks.
   Grade: NEXT (the normative page documents one of six checks)

12. `nova-doctor version -h`
   Printed:
   ```
   usage: nova-doctor version [flags]
   from `nova-doctor help`:
     nova-doctor version
   ```
   I expected `version` to quote its own exits (0 done, 2 usage), because the verb runs no check. Its help instead quotes `run`'s check table, `exit codes: 0 every check ok (a warn too, unless --strict), 1 a warn under --strict, 2 a fail, or usage`, which no `version` call can reach.
   Grade: NEXT (a verb that runs no check quotes the check exit table)

## What held

`help`, `-h` and `--help` print the banner on stdout at exit 0, and `help run`, `run -h` and `version -h` each answer their own help at exit 0 before reading anything. `version` and `version --json` print the one version line, the JSON object carrying it as `payload`. `run`, a bare `nova-doctor`, `--local`, `--strict` and `--json` all built one result set in name order: `--local` prints `DOCTOR local skipped=providers,secrets,tailnet (...)` and drops the fleet checks, `--json` carries the same results and the same `exit` value, and `--strict` turns a `warn` into exit 1. `--check` is repeatable; an unknown check names all six; an unknown flag names the real set with a `run:` line. The checks reached `ok` honestly where the machine allowed it: `gosdk` is `ok` inside the checkout with `go 1.26.6`, a writable `GOCACHE` and `GOFLAGS=-mod=readonly`, `warn` and exit 0 without `-mod=readonly`, and `fail` with an unwritable `GOCACHE` naming the export; with `NOVA_SECRETS_SEAT=coordinator` and no `go` it is `ok`. `harness` is `ok` with no friend rows, and `self` is `ok` with two tools on one version and names the odd tool on a two-to-one split. Every fix line is one line, and no run changed the scratch `HOME`, the scratch `PATH` or the scratch store.

READ 5/10 — the banner, the exit table and the refusal grammar are clear and paste-ready, but the banner advertises a `--max` no verb has, `run -h` prints a phrase as a flag value, `version -h` quotes `run`'s exit table, and the page under `docs/` documents one of the six checks.

USE 4/10 — every check reached `ok` or `fail` with evidence and one fix line and changed nothing, but a file operand and extra words are silently dropped, a request for help runs the checks, `gosdk` fails a healthy toolchain outside a checkout, the seat file is not read so a coordinator is told to install Go, and the `secrets` check prints the key value the page says it never prints.

urgent=5 next=7
