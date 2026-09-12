# nova-update — specification

Glenn, 2026-09-12: *"there are probably new versions we should check for
everything for updates regularly."* And the name: *"nova-update"*. Card #89.

One tool, two verbs.

- `nova-update check --file <path>` reads one versions file kept in git and asks
  two questions of every dependency it names: what is INSTALLED on this box, and
  what is the LATEST its own source publishes. One line per stale item, a count
  line always, exit 1 when anything is stale or unknown; it never installs and
  never pulls.
- `nova-update apply --file <path> <name>` installs exactly the one thing named,
  the way the file says, on a person's word. It refuses a model by name, refuses
  a name the file does not carry, and never runs without a name.

`check` is this estate's first tool that reads somebody else's server on a
clock, so every rule below is about a bound: a bounded read, a bounded budget, a
bounded listing, and a failure that is never allowed to read as *up to date*.
SPEC.md's **Conventions** govern — exit codes, the one-line grammar, the field
law, `internal/oneline`, `internal/bounded`, no guessed paths — and this file
says only what is more than that.

## The rules, numbered

1. **One file, named by a flag, kept in git.** Every entry comes from the file
   `--file` names: no default path, no search of the cwd, no `$HOME`. A missing
   `--file` is `refusing to guess`, exit 2. It is a text file in the repository
   so that a change to what we depend on is a diff somebody read.

2. **Six fields per entry, and nothing implied.** One entry is one line, tab
   separated: `name`, `kind`, `installed`, `latest`, `apply`, `owner`. No field
   may be empty; `apply` may be the literal `none`, which is how "there is no
   automatic way to install this" is spelled. A line with more or fewer than six
   fields is a refusal naming the line number, exit 2 — never a skip.

3. **A command is argv, never a shell.** `installed` and `apply` are split on
   single spaces and executed directly: no shell, no pipe, no glob, no `&&`, no
   environment expansion. An argument needing a space inside it is refused at
   load time with the remedy *put it in a script and name the script.* This is
   the rule that makes rule 13 provable.

4. **The installed version is read by one fixed rule.** Run the entry's
   `installed` argv, take the FIRST line of its combined output, take the first
   substring matching `\d+\.\d+(\.\d+)*` — a leading `v` or a name glued to the
   front is not part of it. A command that exits non-zero, times out, or prints
   no such substring is UNKNOWN, with the remedy *wrap it in a script that
   prints the version alone*. No per-entry pattern field: six fields, one rule.
   The ten commands this estate uses, measured on the bench box 2026-09-11:

   | entry | command | first line | read as |
   |---|---|---|---|
   | opencode | `opencode --version` | `1.18.30` | 1.18.30 |
   | codex | `codex --version` | `codex-cli 0.153.4` | 0.153.4 |
   | gemini | `gemini --version` | `0.46.0` | 0.46.0 |
   | ollama | `ollama --version` | `ollama version is 0.33.3` | 0.33.3 |
   | sops | `sops --version --disable-version-check` | `sops 3.13.3` | 3.13.3 |
   | age | `age --version` | `v1.3.2` | 1.3.2 |
   | gh | `gh --version` | `gh version 2.100.0 (2026-09-03)` | 2.100.0 |
   | go | `go version` | `go version go1.27.1 darwin/arm64` | 1.27.1 |
   | git | `git --version` | `git version 2.55.0` | 2.55.0 |
   | node | `node --version` | `v26.8.2` | 26.8.2 |

   `gh` prints a second line and `go` glues its version to its name; both are
   why the rule is *first line, first dotted number*, and the table is a test
   fixture rather than a claim in prose.

5. **Five kinds, and the kind decides what may happen.** `harness` (OpenCode,
   Codex CLI, Pi, Goose, Qwen Code), `engine` (ollama; antirez's ds4, whose
   version is a git tag), `model` (a weight listed in the ollama library —
   checked, never pulled), `tool` (sops, age, gh, go, node, git), `pin` (one of
   our own tools' pinned version of another of ours). An unknown kind is a
   refusal naming the line, exit 2.

6. **Four latest sources, each one bounded GET, each one echoed.** `latest` is
   `<scheme>:<locator>`:

   | scheme | asks | reads |
   |---|---|---|
   | `github:owner/repo` | `GET https://api.github.com/repos/owner/repo/releases/latest` | `tag_name` |
   | `npm:package` | `GET https://registry.npmjs.org/package/latest` | `version` |
   | `brew:formula` | `GET https://formulae.brew.sh/api/formula/formula.json` | `versions.stable` |
   | `ollama:model` | `GET https://ollama.com/library/model/tags` | the newest tag |
   | `local:<argv>` | runs that argv here | rule 4's read |

   One GET per entry, no redirect chain beyond three, no pagination, no second
   request; the body is capped at 256 KB and a body that reaches the cap is
   UNKNOWN rather than parsed from a prefix. `npm:` asks the `/latest` document,
   never the packument, which is megabytes. Every printed line carries `source=`
   with the scheme and locator it asked, so a reader never guesses who answered.

7. **A source that does not answer is UNKNOWN, and UNKNOWN is not OK.** A
   timeout, a 5xx, a rate limit, a malformed body, a missing field, an empty tag
   — each is one `UPDATE UNKNOWN` line naming the source and the reason, and the
   run exits 1. **Network failure is never silence and never green.** A nightly
   that prints *all up to date* because GitHub was down is worse than no
   nightly, and that is the rule this tool exists to hold.

8. **A bounded run, because a call must answer.** `--timeout <d>` bounds one
   source read, default `5s`; `--budget <d>` bounds the whole run, default
   `60s`; at most four reads are in flight at once. Entries not reached inside
   the budget are UNKNOWN with the reason `budget`, and the run still prints its
   count line and exits 1. The two-minute rule is a design constraint here.

9. **`check` never installs, never pulls, never writes.** No package manager, no
   pull, nothing written under `$HOME`, no cache file. `check` is a read of the
   world and a report.

10. **`apply` needs a name, from a person.** `nova-update apply --file <path>`
    with no name is a refusal, exit 2, naming that a name is required. There is
    no `--all`, no `--stale`, no glob, no `-y`. One run installs one thing.

11. **`apply` refuses a model, by name, with the path.** `kind=model` is refused
    with the remedy naming nova-local: a weight arrives through nova-local's
    quarantine and its eval, never through this tool. The refusal names the
    model so the person reading a transcript knows which one.

12. **`apply` refuses a name the file does not carry.** Not a near match, not a
    prefix, not a case-insensitive match — the exact name or a refusal, exit 2,
    naming the file it read and the count of entries in it.

13. **`apply` runs exactly the entry's `apply` argv, and the only thing
    interpolated is the version.** The token `{version}`, wherever it appears in
    the argv, becomes the version being installed — the latest the source just
    reported, or `--version <v>` when the person named one. Nothing else is
    substituted, and rule 3 means there is no shell to substitute in. An entry
    whose `apply` is `none` is refused: *this one is installed by hand.*

14. **`apply` prints before and after, both read by the entry's own command.**
    `APPLY BEFORE` before the install, `APPLY AFTER` after it, both through rule
    4. An apply whose after equals its before is `APPLY FAIL`, exit 1 — the
    command ran and the box did not change, which is the tool saying NO.

15. **A stale pin between two of our own tools is a bug, reported the same
    day.** `kind=pin` entries print first and the count line carries `pins=<n>`
    separately. Today's specimen: `internal/wake/bus.go` pins
    `PinnedBusVersion = "v0.10.3"` and nova-wake refuses any nova-bus that is
    not it (#82). A pin is `local:` on both sides — the depender's declared pin
    against the dependee's own `version` verb — so it costs no network at all.

16. **Bounded output, per SPEC.md.** `--max <n>`, default 20, `0` means all, a
    negative is refused. The cap is **per kind** — `stale` and `unknown`
    separately, so a night where six things moved does not hide the one source
    that stopped answering — and each capped kind gets one `UPDATE MORE` line
    naming the remedy. **The count line prints on failure as well as success**,
    and the counts are about the world, never about the output.

17. **The tool stamps, and nothing read from a file or a server is a clock.**
    The opening `at=` is the tool's own. Versions are compared as text after
    rule 4's read: equal is OK, different is STALE. nova-update does not order
    versions, does not know 1.10 is after 1.9, and does not rule a difference a
    downgrade. A person looks. (Open question 5.)

18. **Every refusal names its remedy.** No refusal here ends at the reason: the
    missing flag, the malformed line, the script to wrap the command in, the
    tool that owns the weights — the next thing to type is on the line.

## The versions file

One header line, then one entry per line, tabs between fields; `#` in column one
is a comment. The header is exactly:

```
name	kind	installed	latest	apply	owner
```

Five entries, as they would be written today:

```
gh	tool	gh --version	github:cli/cli	brew upgrade gh	rowan
sops	tool	sops --version --disable-version-check	github:getsops/sops	brew upgrade sops	rowan
opencode	harness	opencode --version	npm:opencode-ai	npm install -g opencode-ai@{version}	freddy
qwen3-coder:30b	model	ollama show qwen3-coder:30b --modelfile	ollama:qwen3-coder	none	stella
nova-wake-pin-nova-bus	pin	nova-wake version --pin nova-bus	local:nova-bus version	none	rowan
```

`owner` is the line who answers for that entry when it goes stale. It is printed
on every STALE line, so the morning line names a person and not only a number.

## The verbs

```
nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update apply --file <path> <name> [--version <v>] [--timeout <d>]
nova-update help
```

`--kind <k>` restricts a run to one kind and is the only filter; it is repeatable
and its absence means every kind. There is no `--only-stale` (the output is
already only stale) and no `--quiet` (the count line is the point).

## Exit codes

| code | meaning |
|------|---------|
| 0 | `check`: every entry was read and every one is current. `apply`: the named thing was installed and its installed version changed. |
| 1 | `check` **said NO**: at least one entry is STALE or UNKNOWN. `apply` said NO: the command ran and the installed version did not change. |
| 2 | could not run: a missing flag, an unreadable or malformed file, an unknown verb, an unknown kind, `apply` with no name, `apply` of a model, `apply` of a name the file does not carry, `apply` of an entry whose `apply` is `none`. |

## Output grammar

```
UPDATE at=<stamp> file=<path> entries=<n> kinds=<k,k,k> timeout=<d> budget=<d> max=<n>
UPDATE STALE name=<name> kind=<kind> installed=<v> latest=<v> source=<source> owner=<owner>
UPDATE UNKNOWN name=<name> kind=<kind> installed=<v|-> source=<source>: <reason> (<remedy>)
UPDATE OK checked=<n> current=<n> stale=<n> unknown=<n> pins=<n> took=<d> file=<path>
UPDATE FAIL checked=<n> current=<n> stale=<n> unknown=<n> pins=<n> took=<d> file=<path>
UPDATE MORE kind=<stale|unknown> shown=<n> total=<t> <remedy>
UPDATE NOTE <something true about this run that is not a finding>
UPDATE REFUSED: <reason> (<remedy>)
APPLY BEFORE name=<name> kind=<kind> installed=<v|-> latest=<v> source=<source>
APPLY RUN name=<name> argv=<n> version=<v>: <command, escaped>
APPLY AFTER name=<name> installed=<v|-> was=<v|->
APPLY OK name=<name> from=<v|-> to=<v> took=<d>
APPLY FAIL name=<name> from=<v> to=<v>: <reason>
APPLY REFUSED name=<name>: <reason> (<remedy>)
```

`UPDATE` and `APPLY` are the two first tokens; `OK` and `FAIL` are the verdicts
and are the **last** line; `STALE`, `UNKNOWN`, `MORE`, `NOTE`, `BEFORE`, `RUN`
and `AFTER` are informational second tokens, declared here as SPEC.md requires,
all on stdout, with `REFUSED` and `FAIL` on stderr. Every name, path, reason,
command and version goes through `internal/oneline`, and every `key=value` value
is one whitespace-free token, so a version carrying a space arrives escaped
rather than forging a field.

## The nightly use, and what it is not

The estate runs `nova-update check --file ./versions.tsv` once a night, and the
morning line carries the stale count, the unknown count, and the pin count when
it is not zero. Nothing in the estate reacts to the exit code automatically.
What this tool deliberately does not do:

- **No auto-apply.** Nothing is installed because a check was red. Applying is a
  person's sentence with a name in it.
- **No clock of its own.** No daemon, no timer, no `--watch`, no state file. The
  estate's scheduler runs it; the tool does not schedule itself.
- **No model pulls.** Not by `check`, not by `apply`, not with a flag. Weights go
  through nova-local's quarantine and eval; this tool only says one is listed.
- **No dependency resolution.** No graph, no transitive versions, no lockfile, no
  "these four must move together". Six fields and one line each.
- **No version ordering** (rule 17), and **no credentials**: every source is a
  public unauthenticated GET, and a source that needs a token is a new spec.

## First run — six lines a stranger pastes

```
$ nova-update help
nova-update — check every dependency's installed version against its latest
  check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  apply --file <path> <name> [--version <v>]

$ nova-update check --file ./cmd/nova-update/testdata/versions.tsv --max 3
UPDATE at=2026-09-12T01:00:00Z file=./cmd/nova-update/testdata/versions.tsv entries=5 kinds=harness,model,pin,tool timeout=5s budget=60s max=3
UPDATE STALE name=nova-wake-pin-nova-bus kind=pin installed=0.10.3 latest=0.11.0 source=local:nova-bus\x20version owner=rowan
UPDATE STALE name=gh kind=tool installed=2.100.0 latest=2.101.0 source=github:cli/cli owner=rowan
UPDATE UNKNOWN name=qwen3-coder:30b kind=model installed=- source=ollama:qwen3-coder: no answer in 5s (raise --timeout, or ask again when the library answers)
UPDATE FAIL checked=5 current=2 stale=2 unknown=1 pins=1 took=6.2s file=./cmd/nova-update/testdata/versions.tsv

$ echo $?
1

$ nova-update check --file ./cmd/nova-update/testdata/versions.tsv --kind tool
UPDATE at=2026-09-12T01:00:12Z file=./cmd/nova-update/testdata/versions.tsv entries=2 kinds=tool timeout=5s budget=60s max=20
UPDATE STALE name=gh kind=tool installed=2.100.0 latest=2.101.0 source=github:cli/cli owner=rowan
UPDATE FAIL checked=2 current=1 stale=1 unknown=0 pins=0 took=0.9s file=./cmd/nova-update/testdata/versions.tsv

$ nova-update apply --file ./cmd/nova-update/testdata/versions.tsv qwen3-coder:30b
APPLY REFUSED name=qwen3-coder:30b: a model is not installed by this tool (weights go through nova-local's quarantine and its eval: nova-local quarantine --help)

$ nova-update apply --file ./cmd/nova-update/testdata/versions.tsv gh
APPLY BEFORE name=gh kind=tool installed=2.100.0 latest=2.101.0 source=github:cli/cli
APPLY RUN name=gh argv=3 version=2.101.0: brew\x20upgrade\x20gh
APPLY AFTER name=gh installed=2.101.0 was=2.100.0
APPLY OK name=gh from=2.100.0 to=2.101.0 took=41.3s
```

The fixture's sources are served by a test HTTP server, so the block above is
reproducible and no test in this repo reaches the real internet.

## Tests this spec demands

One test per rule, named for the rule, each proven able to fail by a mutation
before it is trusted. Every latest source in every test is a local `httptest`
server; a tripwire finds no literal `api.github.com`, `registry.npmjs.org`,
`formulae.brew.sh` or `ollama.com` outside the parser's own table and the docs.

1. `TestTheFileComesFromAFlag`: no `--file` is exit 2 printing `refusing to
   guess` and naming `--file`; a `--file` that does not exist is exit 2 naming
   the path; a tripwire finds no `os.Getwd`, no `os.UserHomeDir` and no
   hardcoded path in the package.
2. `TestSixFieldsOrARefusal`: a five-field line, a seven-field line and a line
   with an empty field are each exit 2 naming the line number and the field
   count; the refusal appears once, not once per line; a file of 500 good lines
   and one bad one refuses on the bad one and checks nothing.
3. `TestACommandIsArgvNeverAShell`: an entry whose `installed` is
   `sh -c echo\x201.2.3` runs `sh` with those three arguments and does not
   expand; an entry containing `;`, `&&`, `|`, `$(`, backtick or `*` execs once
   with those bytes literal; a tripwire finds no `exec.Command("sh"` and no
   `"-c"` in the package.
4. `TestTheInstalledReadIsOneFixedRule`: rule 4's ten measured outputs are a
   fixture and each yields the version in its last column; `gh`'s second line is
   not read; `go1.27.1` yields `1.27.1`; a command printing no dotted number is
   UNKNOWN with the wrap-it remedy, and one exiting 1 while printing a version
   is UNKNOWN — neither is ever OK.
5. `TestAnUnknownKindRefuses`: each of the five kinds loads; `kind=weights` is
   exit 2 naming the line and listing the five.
6. `TestOneBoundedGetPerEntry`: each scheme reads its documented field from a
   fixture body; one request per entry; a 257 KB body is UNKNOWN with reason
   `body over 256KB`; a four-hop redirect is UNKNOWN; `npm:` requests a path
   ending `/latest`; `source=` equals the entry's `latest` byte for byte.
7. `TestADeadSourceIsNeverOk`: a 500, a 403 rate-limit body, a hang past the
   timeout, a `{}` and invalid JSON each produce one `UPDATE UNKNOWN` line and
   exit 1; `up to date` and `current` never appear for those entries; a run
   where every source is dead exits 1 with `current=0`.
8. `TestTheRunIsBounded`: with an injected clock and 40 entries against a server
   answering after 3s, `--budget 10s` returns inside 10s, prints a count line,
   marks unreached entries UNKNOWN with reason `budget`, exits 1; at most four
   requests are in flight at once; the defaults 5s and 60s are on the first line.
9. `TestCheckWritesNothingAndInstallsNothing`: a `check` over every kind leaves
   the filesystem byte-identical outside the temp dir, starts only the entries'
   own `installed` argvs, and trips no `ollama pull`, `brew install`, `npm
   install` in the package.
10. `TestApplyNeedsANameFromAPerson`: no name is exit 2 saying a name is
    required; `--all`, `--stale` and `-y` are unknown flags, exit 2; two names is
    exit 2.
11. `TestApplyRefusesAModelByName`: `apply` of every `kind=model` entry is exit
    2, carries the model's name, names nova-local's quarantine and eval, and
    starts no process.
12. `TestApplyRefusesAnUnnamedThing`: `g`, `GH`, `gh ` and `gh-cli` against a
    file carrying `gh` are each exit 2 naming the file and the entry count.
13. `TestApplyInterpolatesOnlyTheVersion`: an argv carrying `{version}` twice, a
    `$HOME`, a `{name}` and a `*` substitutes the version twice and the other
    three bytes for byte; `--version 9.9.9` overrides the reported latest and is
    echoed on `APPLY RUN`; an `apply` of `none` is exit 2 with the by-hand remedy.
14. `TestApplyPrintsBeforeAndAfterFromTheEntrysOwnCommand`: a fake installer that
    changes the version gives `APPLY OK` exit 0; one that changes nothing gives
    `APPLY FAIL` exit 1 with `from=` equal to `to=`; one that exits non-zero
    names the exit code; the after read uses the entry's `installed` argv.
15. `TestAStalePinPrintsFirst`: with four stale entries of four kinds the
    `kind=pin` line precedes the rest; the count line carries `pins=1`; a pin
    entry issues zero HTTP requests; `v0.10.3` pinned against a `nova-bus
    version` answering `v0.11.0` is STALE.
16. `TestUpdateOutputIsBoundedAtTheLargestPlausibleState`: 200 entries, 120 stale
    and 60 unknown, print at most `2 * --max + 6` lines over stdout and stderr;
    `stale` and `unknown` each get a `MORE` line carrying the true total; `--max
    0` prints all 180; `--max -1` is exit 2; the count line is correct in each.
17. `TestDifferentIsStaleAndNothingIsOrdered`: `1.9.0` against `1.10.0` is STALE
    and so is `1.10.0` against `1.9.0`; `newer`, `older`, `downgrade` and `ahead`
    appear nowhere in the output; `at=` is the injected clock, never a body.
18. `TestEveryRefusalNamesItsRemedy`: every refusal string in the package,
    collected by the test into a table, ends in a parenthesised remedy carrying a
    command to run or a file to edit; removing one remedy turns the test red.

Beside those: the `### First run` block above runs against the fixture and is
compared by shape per ONBOARDING.md point 5(c); `nova-update help` is stdout,
exit 0; a bare invocation or a flag typo costs one line and never the banner.

## What a prototype must not do

Named before anybody writes one, because each is a thing a first draft does:

- read the versions file from a default path when `--file` is absent;
- shell out with `sh -c` so that a command with a pipe in it "just works";
- treat a failed GET, a rate limit or an empty body as *current*;
- retry a dead source in a loop until the budget is gone, rather than once, then
  UNKNOWN;
- print one line per entry rather than one line per finding plus a count;
- add `--yes`, `--all` or an auto-apply of everything stale;
- pull a model to find out whether a newer one exists;
- sort versions and rule a difference "only a downgrade" — the first step toward
  a green run over a box that moved backwards;
- cache the latest answers, which turns a network failure back into a silent OK
  the next night.

## Open questions for Glenn — each with a default, and the default stands unless he says otherwise

1. **The ollama library has no documented JSON API.** Default: `ollama:<model>`
   is one bounded GET of `https://ollama.com/library/<model>/tags` and the
   newest tag on the page; if the page's shape changes the entry is UNKNOWN and
   never OK, so a silent break costs us an exit 1 and not a false green.
2. **Nothing today prints a pin.** `PinnedBusVersion` lives in
   `internal/wake/bus.go`, readable only from source. Default: nova-wake grows
   `nova-wake version --pin <tool>`, one line, the week nova-update is built;
   until then pin entries read UNKNOWN, which exits 1, so we cannot forget.
3. **Where the file lives.** Default: `versions.tsv` at the root of nova-tools,
   one file for the whole estate, the path always from `--file` so a second
   estate needs no code change.
4. **What `apply` installs with no `--version`.** Default: the latest the source
   reported in that same run, echoed before anything is installed — not
   "whatever the package manager feels like", not a version from a past run.
5. **Ordering.** Default: none, per rule 17 — different is STALE. The
   alternative is a semver comparison, which is a parser, a fallback for the
   things that are not semver (`go1.27.1`, an ollama tag, a git tag) and a new
   class of wrong answers.
6. **Brew versus a release binary for the same tool.** `gh`, `sops` and `age`
   each arrive either way and the two sources disagree by days. Default: the
   file names the source that actually installed the copy on this box, and a
   mismatch is a one-line fix to the file, not a second source per entry.
