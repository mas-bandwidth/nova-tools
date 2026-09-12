# nova-update — specification

Glenn, 2026-09-12: *"there are probably new versions we should check for everything
for updates regularly."* And the name: *"nova-update"*. Card #89.

One tool, two verbs.

- `nova-update check --file <path>` reads one versions file kept in git and asks of
  every dependency it names what is INSTALLED on this box and what is the LATEST its
  own source publishes. One line per finding, a count line always, exit 1 when
  anything is stale or unknown; it never installs, never pulls.
- `nova-update apply --file <path> <name>` installs exactly the one thing named, the
  way the file says, on a person's word — and refuses a model, a name the file does
  not carry, and any run with no name at all.

`check` is this estate's first tool that reads somebody else's server on a clock, so
every rule below is about a bound: a bounded read, a bounded budget, a bounded
listing, and a failure never allowed to read as *up to date*. SPEC.md's
**Conventions** govern — exit codes, the one-line grammar, the field law,
`internal/oneline`, `internal/bounded`, no guessed paths — and this file says only
what is more. The estate runs it nightly and reads the counts in the morning; the tool
has no clock of its own — no daemon, no timer, no `--watch`, no state file — and
nothing reacts to its exit code automatically.

## The rules, numbered

1. **One file, named by a flag, kept in git.** Every entry comes from the file
   `--file` names: no default path, no search of the cwd, no `$HOME`. A missing
   `--file` is `refusing to guess`, exit 2. It is a text file in git, so a change to
   what we depend on is a diff somebody read.
2. **Six fields per entry, and nothing implied.** One entry is one tab-separated line:
   `name`, `kind`, `installed`, `latest`, `apply`, `owner`. **The first line must equal the
   header byte for byte**, else a refusal naming the file and line 1, exit 2, remedy *put
   the header back exactly as the spec shows* — unchecked, a header-less file loses entry 1
   to the skip. The header is skipped, as is every line whose first character is `#`, and
   nothing else is: neither is an entry, `entries=` counts neither, and rule 5 never reads
   the header's `kind=kind`. No field may be empty; `apply` may be `none`. A line with more
   or fewer fields is a refusal naming the line number, exit 2, never a skip. Six fields and
   one line each is the dependency model: no graph, no lockfile.
3. **A command is argv, never a shell.** `installed`, `apply` and rule 6's `local:<argv>`
   — this tool's three exec sites — are split on single spaces and executed directly: no
   shell, no pipe, no glob, no `&&`, no environment expansion. An argument needing a space
   is refused at load time with the remedy *put it in a script and name the script* — as
   is a field carrying two adjacent spaces or a leading or trailing one, so the split
   never makes an empty argument. This makes rule 13 provable.
4. **The installed version is read by one fixed rule.** Run the entry's `installed` argv and
   take the FIRST line of its stdout — stderr only when stdout is empty, so no race decides
   it — then the first substring matching `\d+\.\d+(\.\d+)*`, a leading `v` or a glued name
   being no part of it. A command that exits non-zero, times out, or prints no such
   substring is UNKNOWN, remedy *wrap it in a script that prints the version alone*.
   **argv[0] resolves against the PATH nova-update was started with**, echoed as `path=` on
   the entry's line; a name resolving nowhere on it is UNKNOWN, reason `not_found` (rule 7's
   `tag_not_found` is the other miss), remedy naming the entry's argv[0] and the PATH
   searched, never the wrap-it remedy. The child inherits the environment, so the nightly's
   PATH is the unit's to state (`EnvironmentVariables` in the plist today, nova-daemon's
   later): under a launchd-default PATH, measured, none of `gh`, `go`, `ollama`, `node`
   resolve. **The same read runs on the latest side**, on whatever field rule 6 names: a
   `tag_name` `v2.101.0` and an installed `2.101.0` are one version. No per-entry pattern
   field: six fields, one rule, one exception `kind` decides below. The ten commands this
   estate uses, measured on the bench box 2026-09-11:

   first line, then what rule 4 reads — `gh version 2.100.0 (2026-09-03)` 2.100.0, with
   `https://github.com/cli/cli/releases/tag/v2.100.0` on the second line and a dotless date
   beside it, neither read; `go version go1.27.1 darwin/arm64` 1.27.1, the version glued to
   the name; `ollama version is 0.33.3` 0.33.3; `v1.3.2` 1.3.2 for `age`. Those four are why
   the rule is *first line, first dotted number*; `opencode`, `codex`, `gemini`, `sops`,
   `git` and `node` print `1.18.30`, `codex-cli 0.153.4`, `0.46.0`, `sops 3.13.3`, `git
   version 2.55.0` and `v26.8.2`. All ten are a fixture in `testdata/`, not prose.

4a. **A model's version is its digest, because a model has no version.** Measured
    2026-09-11: neither `ollama show <model> --modelfile` nor the library's tags page
    carries a dotted number, so under rule 4 every model reads UNKNOWN forever — a red
    nobody can act on. A model does have a digest. **Installed**: the entry's `installed`
    argv runs like any other — rule 4's exit, timeout and PATH clauses hold — its stdout
    read as the `ollama list` table: the ID column of the row whose first token equals the
    entry's `name`, tag and all, `ollama list` printing `gemma4:latest` in full; no such row
    is UNKNOWN, remedy *this weight is not on this box (nova-local quarantine --help)*.
    nova-update itself never asks `http://localhost:11434`, where the same digest sits in
    full at `/api/tags`. **Latest** is one bounded GET of the registry-v2 manifest endpoint
    `https://registry.ollama.ai/v2/library/<model>/manifests/<tag>`, no token, the tag's
    digest being the SHA-256 of the body. A body that is not JSON, or carries no
    `schemaVersion`, or no `layers` — any one — is a shape change: UNKNOWN, never OK (status
    first, rule 7). **Compared**, both sides cut to their first twelve hex characters, per
    rule 17: measured, two of three installed tags match the registry and `qwen3.6:35b-a3b`
    reads `07d35212591f` against `096fdbd02fe6` — one real STALE. **STALE for a model is a
    new weight under the same tag**: a tag moves, and a differing digest is different
    weights, not a newer number. It is *listed as available*, never pulled — an invitation,
    not an install (rules 9, 11).

5. **Five kinds, and the kind decides what may happen.** `harness` (OpenCode),
   `engine` (ollama), `model` (a weight in the ollama library — checked, never pulled,
   rule 4a), `tool` (gh), `pin` (one of our tools' pinned version of another). One
   specimen each, the versions file names the rest; an unknown kind is a refusal
   naming the line, exit 2.
6. **Five latest sources: four bounded GETs and one local argv, each echoed.**
   `latest` is `<scheme>:<locator>`:

   | scheme | asks | reads |
   |---|---|---|
   | `github:owner/repo` | `GET https://api.github.com/repos/owner/repo/releases/latest` | `tag_name` |
   | `npm:package` | `GET https://registry.npmjs.org/package/latest` | `version` |
   | `brew:formula` | `GET https://formulae.brew.sh/api/formula/formula.json` | `versions.stable` |
   | `ollama:model:tag` | `GET https://registry.ollama.ai/v2/library/model/manifests/tag` | the body's SHA-256 (rule 4a) |
   | `local:<argv>` | runs that argv here | rule 4's read |

   The field the table names goes through rule 4's read — a model's digest through rule
   4a's — so nothing downstream sees a leading `v`. One GET per entry, with one documented
   exception, because a project can tag and never release: measured 2026-09-11, `GET
   /repos/antirez/ds4/releases/latest` is 404. A `github:` answered 404, and only then,
   asks `GET .../repos/owner/repo/tags?per_page=1` once and reads `[0].name` **through
   rule 4's read, like every field this table names**, so a tag `v0.11.0` against an
   installed `0.11.0` is OK and never a false STALE — two bounded GETs at most — and an
   empty `[]` is UNKNOWN, reason `no release and no tag`, as ds4 reads today until antirez
   tags. Otherwise no second request, no redirect beyond three hops, no pagination; the
   body is capped at 256 KB, one reaching the cap being UNKNOWN, not parsed from a prefix;
   `npm:` asks `/latest`, never the packument, which is megabytes. Every line carries
   `source=` with the scheme and locator asked, and the endpoint that answered when the
   tags read did. **No credentials**: every source is a public unauthenticated GET, one
   needing a token is a new spec.
7. **A source that does not answer is UNKNOWN, and UNKNOWN is not OK.** A timeout, a 5xx, a
   rate limit, a malformed body, a missing field, an empty tag — each is one `UPDATE
   UNKNOWN` line naming the source and the reason, and the run exits 1. **The status is read
   before the body**: any status but 200 is UNKNOWN naming it, rule 6's `github:` 404 the
   one exception, so a registry 404 — measured, valid JSON with no `schemaVersion` — is
   `tag_not_found`, never `shape` and never rule 4's `not_found`, which is argv[0] missing
   from the PATH: two misses, two remedies, this one *check the tag at
   `https://ollama.com/library/<model>/tags`*. A 403 or a 429 quotes its `x-ratelimit-reset`
   as the time to ask again. **Network failure is never silence and never green.** A nightly
   that prints *all up to date* because GitHub was down is worse than no nightly: that is
   the rule this tool exists to hold. A dead source is read once, then UNKNOWN, never
   retried until the budget is gone, and never cached: a cache turns tonight's failure into
   silent OK.
8. **A bounded run, because a call must answer.** `--timeout <d>` bounds one source
   read, default `5s`; `--budget <d>` bounds the whole run, default `60s`; at most
   four reads are in flight at once. Entries not reached inside the budget are
   UNKNOWN, reason `budget`, and the run still prints its count line and exits 1. The
   two-minute rule is a design constraint here.
9. **`check` never installs, never pulls, never writes.** No package manager, no pull,
   nothing written under `$HOME`, no cache file. `check` is a read of the world and a
   report.
10. **`apply` needs a name, from a person.** `nova-update apply --file <path>` with no
    name is a refusal, exit 2, saying a name is required. There is no `--all`, no
    `--stale`, no glob, no `-y`. One run installs one thing.
11. **`apply` refuses a model, by name, with the path.** `kind=model` is refused with
    the remedy naming nova-local: a weight arrives through nova-local's quarantine and
    its eval, never through this tool. The refusal names the model, so a transcript
    says which one.
12. **`apply` refuses a name the file does not carry.** Not a near match, not a
    prefix, not a case-insensitive match — the exact name or a refusal, exit 2, naming
    the file it read and its entry count.
13. **`apply` runs exactly the entry's `apply` argv, and the only thing interpolated is the
    version.** The token `{version}`, wherever it appears, is the version being installed:
    the latest the source just reported, or `--version <v>` when the person named one. A
    latest the source did not answer, with no `--version`, is a refusal, exit 2 — *latest
    unknown; pass `--version <v>`* — and no process starts. Nothing else is substituted, and
    rule 3 leaves no shell to substitute in. An entry whose `apply` is `none` is refused:
    *this one is installed by hand* — as is `--version <v>` against an argv with no
    `{version}`: *this entry's apply does not take a version*, the flag a lie.
14. **`apply` prints before and after, and after must equal the target.** `APPLY
    BEFORE` before the install, `APPLY AFTER` after it, both read by the entry's
    `installed` command through rule 4, and the **target** is the version echoed on
    `APPLY RUN`. After equal to the target is `APPLY OK`, exit 0; everything else is
    `APPLY FAIL`, exit 1, saying which — after equal to before, so the command ran and
    nothing changed; after equal to neither, `installed <after>, asked <target>`, what
    a `{version}`-less argv like `brew upgrade gh` does when brew is a day behind
    (open question 4); or a non-zero exit, named by code. A *changed* version is not
    the question; whether the box carries what the line asked is, and saying OK to
    anything else is the tool lying.
15. **A stale pin between two of our own tools is a bug, reported the same day by the
    entry's owner.** `kind=pin` entries print first and the count line's `pins=<n>` is
    the number of **stale** pins, a subset of `stale=`, never the number of pin
    entries. Today's specimen: `internal/wake/bus.go` pins `PinnedBusVersion =
    "v0.10.3"` and nova-wake refuses any nova-bus that is not it (#82). A pin is
    `local:` on both sides — the depender's declared pin against the dependee's
    `version` verb — so it costs no network. A stale pin means nova-wake is refusing
    the bus the estate is running: the fix is in nova-wake.
16. **Bounded output, per SPEC.md.** `--max <n>`, default 20, `0` means all, a
    negative is refused. The cap is **per kind** — `stale` and `unknown` separately,
    so a night where six things moved does not hide the one source that stopped
    answering — and each capped kind gets one `UPDATE MORE` line naming the remedy.
    **The count line prints on failure as well as success**, and the counts are about
    the world, never about the output.
17. **The tool stamps, and nothing read from a file or a server is a clock.** The
    opening `at=` is the tool's own. Versions are compared as text after rule 4's
    read, or 4a's: equal is OK, different is STALE. nova-update does not order
    versions, does not know 1.10 is after 1.9, and does not rule a difference a
    downgrade. A person looks.
18. **Every refusal names its remedy.** No refusal here ends at the reason: the
    missing flag, the malformed line, the script to wrap the command in, the tool that
    owns the weights — the next thing to type is on the line.
19. **`--kind <k>` restricts the run, not the output.** Repeatable, the only filter; absent,
    every kind runs. A kind not named is **neither read nor printed** — no `installed` argv
    starts for it and no GET is made — so the first line's `kinds=` and the count line's
    `checked=` are the filtered run while `entries=` stays the file's count: the difference
    is the filter. An unknown `--kind` is its own refusal, exit 2, naming the value and the
    five kinds — rule 5's refusal names a line; a flag has no line.

## The versions file

One header line, then one entry per line, tabs between fields; a first character of `#` is a
comment, and rule 2 skips it and the header, refusing a first line that is not the header
byte for byte. The header exactly as shown, then five entries as written today:

```
name	kind	installed	latest	apply	owner
gh	tool	gh --version	github:cli/cli	brew upgrade gh	rowan
sops	tool	sops --version --disable-version-check	github:getsops/sops	brew upgrade sops	rowan
opencode	harness	opencode --version	npm:opencode-ai	npm install -g opencode-ai@{version}	freddy
qwen3-coder:30b	model	ollama list	ollama:qwen3-coder:30b	none	stella
nova-wake-pin-nova-bus	pin	nova-wake version --pin nova-bus	local:nova-bus version	none	rowan
```

`owner` is the line who answers when that entry goes stale, on every STALE line, so
the morning names a person, not only a number.

## The verbs

```
nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update apply --file <path> <name> [--version <v>] [--timeout <d>]
nova-update help
```

Those two usage lines are the string `nova-update help` prints, byte for byte: one
string in the binary, so the spec and the help cannot drift apart. `--kind <k>` is rule 19.
There is no `--only-stale` (the output is already only findings) and no `--quiet` (the
count line is the point).

## Exit codes and the output grammar

Per SPEC.md: **0** every entry current, or an `apply` that left the box on the target;
**1** the tool saying NO — anything STALE or UNKNOWN, or an `apply` whose after is not
the target; **2** could not run, every refusal the rules name.

```
UPDATE at=<stamp> file=<path> entries=<n> kinds=<k,k,k> timeout=<d> budget=<d> max=<n>
UPDATE STALE name=<name> kind=<kind> installed=<v> latest=<v> path=<path> source=<source> owner=<owner>
UPDATE UNKNOWN name=<name> kind=<kind> installed=<v|-> path=<path|-> source=<source>: <reason> (<remedy>)
UPDATE <OK|FAIL> checked=<n> current=<n> stale=<n> unknown=<n> pins=<n> took=<d> file=<path>
UPDATE MORE kind=<stale|unknown> shown=<n> total=<t> <remedy>
UPDATE NOTE <something true about this run that is not a finding>
UPDATE REFUSED: <reason> (<remedy>)
APPLY BEFORE name=<name> kind=<kind> installed=<v|-> path=<path|-> latest=<v> source=<source>
APPLY RUN name=<name> argv=<n> version=<v>: <command, escaped>
APPLY AFTER name=<name> installed=<v|-> was=<v|->
APPLY <OK|FAIL> name=<name> from=<v|-> to=<v|-> took=<d>[: <reason>]
APPLY REFUSED name=<name>: <reason> (<remedy>)
```

`UPDATE` and `APPLY` are the first tokens, `OK` and `FAIL` the verdicts and the
**last** line; the rest are informational second tokens, declared here as SPEC.md
requires, on stdout, with `REFUSED` and `FAIL` on stderr. Every name, path, reason,
command and version goes through `internal/oneline` as one whitespace-free token, so a
version carrying a space arrives escaped.

## First run — four lines a stranger pastes

```
$ go install ./cmd/nova-update
$ nova-update check --file ./cmd/nova-update/testdata/versions.tsv --max 3
UPDATE at=2026-09-12T01:00:00Z file=./cmd/nova-update/testdata/versions.tsv entries=5 kinds=harness,model,pin,tool timeout=5s budget=60s max=3
UPDATE UNKNOWN name=nova-wake-pin-nova-bus kind=pin installed=- path=- source=local:nova-bus\x20version: not_found (nova-wake is nowhere on /opt/homebrew/bin:/usr/bin:/bin — install it or add its dir to PATH)
UPDATE STALE name=gh kind=tool installed=2.100.0 latest=2.101.0 path=/opt/homebrew/bin/gh source=github:cli/cli owner=rowan
UPDATE STALE name=qwen3-coder:30b kind=model installed=07d35212591f latest=096fdbd02fe6 path=/opt/homebrew/bin/ollama source=ollama:qwen3-coder:30b owner=stella
UPDATE UNKNOWN name=opencode kind=harness installed=1.18.30 path=/opt/homebrew/bin/opencode source=npm:opencode-ai: no answer in 5s (raise --timeout, or ask again when the registry answers)
UPDATE FAIL checked=5 current=1 stale=2 unknown=2 pins=0 took=6.2s file=./cmd/nova-update/testdata/versions.tsv
$ nova-update apply --file ./cmd/nova-update/testdata/versions.tsv qwen3-coder:30b
APPLY REFUSED name=qwen3-coder:30b: a model is not installed by this tool (weights go through nova-local's quarantine and its eval: nova-local quarantine --help)
$ nova-update apply --file ./cmd/nova-update/testdata/apply.tsv fixture-tool
APPLY BEFORE name=fixture-tool kind=tool installed=1.0.0 path=./testdata/fixture-tool latest=1.1.0 source=github:example/fixture
APPLY RUN name=fixture-tool argv=2 version=1.1.0: ./testdata/fake-install.sh\x201.1.0
APPLY AFTER name=fixture-tool installed=1.1.0 was=1.0.0
APPLY OK name=fixture-tool from=1.0.0 to=1.1.0 took=0.1s
```

Inside `go test` every GET source above is an `httptest` server the parser's table of hosts
points at and every argv — `installed`, `local:`, `apply` — a script under `testdata/`, the
apply writing a version to a temp dir; the fixture's model digests are the measured
`qwen3.6:35b-a3b` pair under another name. Pasted in a terminal, line 2 reaches GitHub twice
— `gh` and `sops` are both `github:`, one GET per entry — npm and the registry once each,
installing nothing: which is why the last line applies a fixture, not `gh`, since **`apply`
of a real entry runs the real installer on the box it is pasted into** and `check` of a real
file reaches the real internet. Do both on purpose. The `kind=pin` entry is UNKNOWN twice
over here: nova-wake grows `version --pin` only with open question 2, and is not on the
nightly's PATH. Once both hold it reads STALE, first per rule 15, with `pins=1`.

## Tests this spec demands

One test per rule, named for it, each proven able to fail by a mutation first. Every
latest source is a local `httptest` server, and the tripwires live together: outside
the parser's table and the docs, no `api.github.com`, `registry.npmjs.org`,
`formulae.brew.sh` or `registry.ollama.ai`; nowhere `localhost:11434` — nothing asks
it — `os.Getwd`, `os.UserHomeDir`, a hardcoded path, `exec.Command("sh"`, `"-c"`,
`ollama pull`, `brew install` or `npm install`.

1. `TestTheFileComesFromAFlag`: no `--file` is exit 2 printing `refusing to guess` and
   naming `--file`; a `--file` that does not exist is exit 2 naming the path.
2. `TestSixFieldsOrARefusal`: five, seven and empty fields are each exit 2 naming the line
   number and the count; a first line that is not the header is exit 2 naming line 1 with
   the put-it-back remedy, entry 1 never skipped; a header plus a `#` comment loads,
   `entries=` counting neither and neither refused as an unknown kind; the refusal appears
   once, not per line; 500 good lines and one bad refuse on the bad one only.
3. `TestACommandIsArgvNeverAShell`: `sh -c echo 1.2.3` execs `sh` with four arguments,
   so `sh` runs `echo` with `1.2.3` as `$0` and prints an empty line: UNKNOWN, never
   `1.2.3`; `;`, `&&`, `|`, `$(`, backtick and `*` exec once with those bytes literal
   at each of the three exec sites (`installed`, `apply`, `local:`); two adjacent
   spaces in a field is exit 2 naming the line.
4. `TestTheInstalledReadIsOneFixedRule`: rule 4's ten measured outputs are a fixture, each
   yielding its stated read; `gh`'s second line is not read; `go1.27.1` yields `1.27.1`; no
   dotted number is UNKNOWN with the wrap-it remedy and so is exit 1 with a version printed;
   stderr is read only when stdout is empty; `nosuchbinary --version` and an empty `PATH`
   are UNKNOWN reason `not_found`, remedy naming argv[0] and the PATH searched, never the
   wrap-it one, and a resolved entry prints its `path=`.
4a. `TestAModelsVersionIsItsDigest`: `# Modelfile generated by "ollama show"` is UNKNOWN
    under rule 4, which is why 4a exists; an `ollama list` fixture yields the matching row's
    ID, no matching row and an entry named without its tag each being UNKNOWN with the
    not-on-this-box remedy; a fixture registry-v2 body yields its own SHA-256, and that body
    without `layers`, and again without `schemaVersion`, is UNKNOWN reason `shape`; equal
    twelve-hex digests are OK, different ones STALE with both on the line.
5. `TestAnUnknownKindRefuses`: each of the five kinds loads; `kind=weights` is exit 2
   naming the line and listing the five.
6. `TestOneBoundedGetPerEntry`: each scheme reads its documented field and a `tag_name` of
   `v2.101.0` prints `latest=2.101.0`; one request per entry except a `github:` answered
   404, which makes exactly one more to `/tags?per_page=1`, reads `[0].name` through rule
   4's read so a `v0.11.0` there prints `latest=0.11.0` and reads OK against an installed
   `0.11.0`, names that endpoint in `source=`, and whose `[]` is UNKNOWN reason `no
   release and no tag`; a 257 KB body and a four-hop redirect are UNKNOWN; `npm:` requests
   a path ending `/latest`; `source=` equals `latest`.
7. `TestADeadSourceIsNeverOk`: a 500, a 403 and a 429 each naming its `x-ratelimit-reset`, a
   registry 404 giving `tag_not_found` with the library page as its remedy, never `shape`
   and never rule 4's `not_found`, a hang past the timeout, a `{}` and invalid JSON each
   give one `UPDATE UNKNOWN` and exit 1; `up to date` never appears; every source dead exits
   1 with `current=0`; a dead source is asked once in a run and again in the next — the
   no-retry, no-cache assertion.
8. `TestTheRunIsBounded`: with an injected clock and 40 entries against a server
   answering after 3s, `--budget 10s` returns inside 10s, prints a count line, marks
   unreached entries UNKNOWN reason `budget` and exits 1; an httptest handler counting
   concurrent entries never sees a fifth; the defaults are on the first line.
9. `TestCheckWritesNothingAndInstallsNothing`: `HOME` and the cwd are fresh temp dirs,
   empty after a `check` over every kind, and only the entries' own `installed` argvs
   start.
10. `TestApplyNeedsANameFromAPerson`: no name is exit 2 saying a name is required;
    `--all`, `--stale` and `-y` are unknown flags; two names is exit 2.
11. `TestApplyRefusesAModelByName`: `apply` of every `kind=model` entry is exit 2,
    carries the model's name, names nova-local's quarantine and eval, and starts no
    process.
12. `TestApplyRefusesAnUnnamedThing`: `g`, `GH`, `gh ` and `gh-cli` against a file
    carrying `gh` are each exit 2 naming the file and the entry count.
13. `TestApplyInterpolatesOnlyTheVersion`: an argv with `{version}` twice, a `$HOME`, a
    `{name}` and a `*` substitutes the version twice and the rest byte for byte; `--version
    9.9.9` overrides the latest and is echoed on `APPLY RUN`; a dead source with no
    `--version` is exit 2, no process started; an `apply` of `none` is exit 2, by-hand
    remedy.
14. `TestApplyAfterMustEqualTheTarget`: a fake installer landing exactly the target is
    `APPLY OK` exit 0; one changing nothing is `APPLY FAIL` exit 1 with `from=` equal
    to `to=`; one landing `1.1.1` against a target of `1.1.2` is `APPLY FAIL` naming
    both and **not** OK, the mutation that matters; a non-zero exit is `APPLY FAIL`
    naming the code; `--version` against an argv with no `{version}` is exit 2; the
    after read uses `installed`.
15. `TestAStalePinPrintsFirst`: with four stale entries of four kinds the `kind=pin`
    line precedes the rest; two pin entries, one stale, print `pins=1`; a pin entry
    issues zero HTTP requests; `v0.10.3` against a `nova-bus version` answering
    `v0.11.0` is STALE.
16. `TestUpdateOutputIsBoundedAtTheLargestPlausibleState`: 200 entries, 120 stale and
    60 unknown, print at most `2 * --max + 6` lines over both streams; `stale` and
    `unknown` each get a `MORE` line with the true total; `--max 0` prints all 180;
    `--max -1` is exit 2.
17. `TestDifferentIsStaleAndNothingIsOrdered`: `1.9.0` against `1.10.0` is STALE and
    so is `1.10.0` against `1.9.0`; `newer`, `older`, `downgrade` and `ahead` appear
    nowhere in the output; `at=` is the injected clock, never a body.
18. `TestEveryRefusalNamesItsRemedy`: every refusal in the package lives in one table,
    which the test walks: each ends in a parenthesised remedy naming a command or a
    file, and removing one turns the test red.
19. `TestTheKindFilterRestrictsTheRun`: `--kind tool` over a file of all five kinds starts
    only the tool entries' `installed` argvs and only their GETs — a counting handler sees
    no others — prints no line of another kind, and its `kinds=` and `checked=` are the
    filter's while `entries=` stays the file's; two `--kind` flags cover both; `--kind
    weights` is exit 2 naming the value and the five kinds, no line number.

Beside those: the first-run block runs against the fixture, compared by shape per
ONBOARDING.md 5(c); `nova-update help` prints the verbs block on stdout, exit 0; a
bare invocation or a flag typo costs one line, never a banner.

## Open questions for Glenn — each with a default, and the default stands unless he says otherwise

1. **Ollama publishes no JSON API, but its registry speaks registry-v2.** Default:
rule 4a's one manifest GET, digest as text; the rejected alternative, scraping
`ollama.com/library/<model>/tags`, is 50 KB of HTML with no documented shape and no
dotted number anyway. A registry that stops answering registry-v2 JSON is UNKNOWN,
never OK: a silent break costs an exit 1.
2. **Nothing today prints a pin.** `PinnedBusVersion` lives in `internal/wake/bus.go`,
   readable only from source. Default: nova-wake grows `nova-wake version --pin
   <tool>` the week nova-update is built; until then pin entries read UNKNOWN, which
   exits 1, so we cannot forget.
3. **Where the file lives.** Default: `versions.tsv` at the root of nova-tools, one
   for the estate, the path always from `--file`.
4. **Brew versus a release binary for the same tool.** `gh`, `sops` and `age` each
arrive either way and the two disagree by days. Default: the file names the source
that installed the copy on this box; a mismatch is a one-line fix to the file, not a
second source per entry.
