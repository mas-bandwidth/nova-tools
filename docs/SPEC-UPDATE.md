# nova-update — specification

Glenn, 2026-09-12: *"there are probably new versions we should check for everything
for updates regularly."* And the name: *"nova-update"*. Card #89.

One tool, three verbs.

- `nova-update check --file <path>` reads one versions file kept in git and asks of every
  dependency it names what is INSTALLED on this box and what is the LATEST its own source
  publishes. One line per finding, a count line always, exit 1 when anything is not current
  — STALE, NEWER, DIFFERENT or UNKNOWN; it never installs, never pulls.
- `nova-update apply --file <path> <name>` installs exactly the one thing named, the
  way the file says, on a person's word — and refuses a model, a name the file does
  not carry, and any run with no name at all.
- `nova-update report --file <path>` prints what this box runs — the same file, the
  `installed` side only, the whole identity of each tool beside its key — with no network,
  no recipients and no bus; `--draft` and `--send` hand that report to nova-bus, on the
  caller's word (rules 20–26; #121, the reporting surface the lines settled).

`check` is this estate's first tool that reads somebody else's server on a clock, so
every rule below is about a bound: a bounded read, a bounded budget, a bounded listing,
and a failure never allowed to read as *up to date*. SPEC.md's **Conventions** govern —
exit codes, the one-line grammar, the field law, `internal/oneline`, `internal/bounded`,
no guessed paths — and this file says only what is more. The estate runs it nightly and
reads the counts in the morning; the tool has no clock of its own — no daemon, no timer,
no `--watch`, no state file of its own (rule 25's snapshot is the caller's, named by flag)
— nothing reacts to its exit code, no verdict starts an `apply`.

## The rules, numbered

1. **One file, named by a flag, kept in git.** Every entry comes from the file
   `--file` names: no default path, no search of the cwd, no `$HOME`. A missing
   `--file` is `refusing to guess`, exit 2. It is a text file in git, so a change to
   what we depend on is a diff somebody read.
2. **Six fields per entry, and nothing implied.** One entry is one tab-separated line:
   `name`, `kind`, `installed`, `latest`, `apply`, `owner`. **The first line must equal
   the header byte for byte**, else a refusal naming the file and line 1, exit 2, remedy
   *put the header back exactly as the spec shows* — unchecked, a header-less file loses
   entry 1 to the skip. The header and every line whose first character is `#` are
   skipped, nothing else is: neither is an entry, `entries=` counts neither, rule 5 never
   reads `kind=kind`. No field may be empty; `apply` may be `none`. More or fewer fields
   is a refusal naming the line number, exit 2, never a skip. No graph, no lockfile.
3. **A command is argv, never a shell.** `installed`, `apply` and rule 6's `local:<argv>`
   — this tool's three exec sites — are split on single spaces and executed directly: no
   shell, no pipe, no glob, no `&&`, no environment expansion. An argument needing a space
   is refused at load time with the remedy *put it in a script and name the script* — as
   is a field carrying two adjacent spaces or a leading or trailing one, so the split
   never makes an empty argument. This makes rule 13 provable.
4. **The installed version is read by one fixed rule, and it is the whole identity.** Run
   the entry's `installed` argv and take the FIRST line of its stdout — stderr only when
   stdout is empty, so no race decides it — then the first whitespace-delimited token
   holding `\d\.\d` in a row, **from its first digit to the token's end**: a leading `v` or
   a glued name is no part of it, and nothing after the number is cut —
   `-0.20260912135226-0459069`, `-rc1`, `+dirty` stay, because two builds that differ only
   there are two builds, and a read that kept the dotted number alone once collapsed them
   into one (Stella, #121). A command that exits non-zero, times out, or prints no such
   token is UNKNOWN, remedy *wrap it in a script that prints the version alone*; its stdout
   and stderr go through `internal/bounded`, rule 23's 64 KB cap, a child reaching it
   UNKNOWN reason `output`, the same remedy. Before the read, a first line whose second
   token is `devel` or a bare commit (rule 21) has no release identity: under `check`, a
   `pin` aside (rule 15), UNKNOWN reason `no_release_identity`, remedy *install a stamped
   build, or read it with `report`*; the toolchain's number on that line is never the
   tool's. **argv[0] resolves
   against the PATH nova-update was started with**, echoed as `path=`; a name resolving
   nowhere on it is UNKNOWN, reason `not_found`, remedy naming argv[0] and the PATH
   searched, never the wrap-it remedy; an argv[0] carrying a `/` is that executable, no
   PATH searched (rule 20). The child inherits the environment, so the nightly's PATH is
   the unit's to state: under a launchd-default PATH, measured, none of `gh`, `go`,
   `ollama`, `node` resolve. **The same read runs on the latest side**, on whatever field
   rule 6 names: a `tag_name` `v2.101.0` and an installed `2.101.0` are one version. One
   rule; `kind` decides two exceptions below (4a, 15). The
   eleven commands this estate uses are a fixture in `testdata/`, measured 2026-09-11 and
   -12, first line then the read: `gh version 2.100.0 (2026-09-03)` 2.100.0, its second line
   and dotless date never read; `go version go1.27.1 darwin/arm64` 1.27.1;
   `ollama version is 0.33.3` 0.33.3; `v1.3.2` 1.3.2 (`age`); `1.18.30` 1.18.30
   (`opencode`); `codex-cli 0.153.4` 0.153.4; `0.46.0` 0.46.0 (`gemini`); `sops 3.13.3`
   3.13.3; `git version 2.55.0` 2.55.0; `v26.8.2` 26.8.2 (`node`); and
   `nova-bus v0.12.1-0.20260912135226-0459069+dirty darwin/arm64 go1.27.1` reads the whole
   token, `0.12.1-0.20260912135226-0459069+dirty`.
4a. **A model's version is its digest, because a model has no version.** Measured
    2026-09-11: neither `ollama show <model> --modelfile` nor the library's tags page
    carries a dotted number, so under rule 4 every model reads UNKNOWN forever — a red
    nobody can act on. A model does have a digest. **Installed**: the entry's `installed`
    argv runs like any other — rule 4's exit, timeout and PATH clauses hold — its stdout
    read as the `ollama list` table: the ID column of the row whose first token equals the
    entry's `name`, tag and all; no such row is UNKNOWN, remedy *this weight is not on this
    box: the owner pulls it, `ollama pull <name>`; `nova-local status --list` shows it*.
    It never asks `:11434`. **Latest** is one GET of
    `https://registry.ollama.ai/v2/library/<model>/manifests/<tag>`, no token, the digest
    being the SHA-256 of the body; a body not JSON, or without `schemaVersion` or `layers`,
    is a shape change: UNKNOWN, never OK (status first, rule 7). **Compared**, both sides
    cut to their first twelve lowercase hex characters, per rule 17: measured, two of three
    installed tags match and `qwen3.6:35b-a3b` reads `07d35212591f` against `096fdbd02fe6`
    — one real DIFFERENT. **DIFFERENT for a model is a new weight under the same tag**:
    a differing digest is different weights, not a newer number — a digest has no order,
    so a model is never STALE. Listed, never pulled (rules 9, 11).
5. **Five kinds, and the kind decides what may happen.** `harness` (OpenCode), `engine`
   (ollama), `model` (a weight in the ollama library — checked, never pulled, rule 4a),
   `tool` (gh), `pin` (one of our tools' pinned version of another). One specimen each,
   the versions file names the rest; an unknown kind is a refusal naming the line, exit 2.
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
   exception, because a project can tag and never release (measured 2026-09-11, antirez's
   ds4 is 404): a `github:` answered 404, and only then, asks `.../tags?per_page=1` once
   and reads `[0].name` **through rule 4's read**, so a tag `v0.11.0` against an installed
   `0.11.0` is EQUAL, never a false STALE — two bounded GETs at most — and an empty `[]` is
   UNKNOWN, reason `no release and no tag`. Otherwise no second request, no redirect beyond
   three hops, no pagination; the body is capped at 256 KB, one reaching the cap being
   UNKNOWN, not parsed from a prefix; `npm:` asks `/latest`, never the packument. Every
   line carries `source=`: the scheme and locator asked, and the endpoint that answered
   when the tags read did. **No credentials**: a source needing a token is a new spec.
7. **A source that does not answer is UNKNOWN, and UNKNOWN is not OK.** A timeout, a 5xx, a
   rate limit, a malformed body, a missing field, an empty tag — each is one `UPDATE
   UNKNOWN` line naming the source and the reason, and the run exits 1. **The status is read
   before the body**: any status but 200 is UNKNOWN naming it, rule 6's `github:` 404 the
   one exception, so a registry 404 — measured, valid JSON with no `schemaVersion` — is
   `tag_not_found`, never `shape` and never rule 4's `not_found`: two misses, two remedies,
   this one *check `https://ollama.com/library/<model>/tags`*. A 403 or a 429 quotes its
   `x-ratelimit-reset` as the time to ask again. **Network failure is never silence and
   never green.** A nightly that prints *all up to date* because GitHub was down is worse
   than no nightly: that is the rule this tool exists to hold. A dead source is read once,
   then UNKNOWN, never retried, never cached: a cache turns tonight's failure into a green.
8. **A bounded run, because a call must answer.** `--timeout <d>` bounds one source read,
   default `5s`; `--budget <d>` bounds the whole run, default `60s`; at most four reads
   are in flight at once. Entries not reached inside the budget are UNKNOWN, reason
   `budget`, and the run still prints its count line and exits 1 (the two-minute rule).
9. **`check` never installs, never pulls, never writes.** No package manager, no pull,
   nothing under `$HOME`, no cache file: a read of the world and a report.
10. **`apply` needs a name, from a person.** `nova-update apply --file <path>` with no
    name is a refusal, exit 2, saying a name is required. There is no `--all`, no
    `--stale`, no glob, no `-y`; no verdict of `check` — STALE, NEWER, DIFFERENT — is a
    name: the three verbs share a file and nothing else. One run installs one thing.
11. **`apply` refuses a model, by name, with the path.** `kind=model` is refused with the
    remedy naming the owner's own step: a weight arrives by the owner's own `ollama pull
    <name>`, after whatever evaluation the owner chose, never through this tool — SPEC-LOCAL
    cut `quarantine` and `eval` (*if we want to eval, it is another tool*), so no remedy
    here names them; `nova-local status --list` shows the weight landed. The refusal names
    the model, so a transcript says which one.
12. **`apply` refuses a name the file does not carry.** Not a near match, not a
    prefix, not a case-insensitive match — the exact name or a refusal, exit 2, naming
    the file it read and its entry count.
13. **`apply` runs exactly the entry's `apply` argv, and the only thing interpolated is the
    version.** The token `{version}`, wherever it appears, is the version being installed:
    the latest the source reported, or `--version <v>` when the person named one. A latest
    the source did not answer, with no `--version`, is a refusal, exit 2 — *latest unknown;
    pass `--version <v>`, or ask again when the source answers* — and no process starts.
    Nothing else is substituted; rule 3 leaves no shell to substitute in. An `apply` of
    `none` is refused: *installed by hand*; `--version` against an argv with no
    `{version}`: *this entry's apply does not take a version*.
14. **`apply` prints before and after, and after must equal the target.** `APPLY
    BEFORE` before the install, `APPLY AFTER` after it, both read by the entry's
    `installed` command through rule 4, and the **target** is the version echoed on
    `APPLY RUN`. After EQUAL to the target is `APPLY OK`, exit 0; everything else is
    `APPLY FAIL`, exit 1, saying which — after equal to before, so the command ran and
    nothing changed; after equal to neither, `installed <after>, asked <target>`, what a
    `{version}`-less `brew upgrade gh` does when brew is a day behind (open question 3);
    or a non-zero exit, named by code. Whether the box carries what the line asked is the
    question, and saying OK to anything else is a lie.
15. **A broken pin between two of our own tools is a bug, reported the same day by the
    entry's owner.** `kind=pin` entries print first and the count line's `pins=<n>` is the
    number of **DIFFERENT** pins, a subset of `differ=`, never the number of pin entries.
    Today's specimen: `internal/wake/bus.go`'s `AcceptBus(tool, found)` — nova-wake accepts
    exactly the `nova-bus` whose `version` equals its own: `tool != "" && found == tool`,
    string equality behind a non-empty guard, no parse, no range, no order (#104, #107;
    SPEC-WAKE, *How the checkout receives mail*). The pin is **derived**, so the depender's
    side is `nova-wake version` itself, no `--pin` verb; both sides are local commands, only
    `latest` carrying a scheme, `local:`; no network; for this kind alone both reads are the
    **second token of the first line, whole** — `BusVersion`'s own read; fewer than two
    tokens is UNKNOWN, reason `BusVersion`'s own error, wrap-it remedy — and the comparison
    is `wake.AcceptBus(installed, latest)`, nova-wake's read then the bus's, **imported,
    never copied**, so this tool and nova-wake cannot disagree about a pair: EQUAL or
    DIFFERENT, never an order: two tools have none between them. A DIFFERENT pin means
    nova-wake is refusing the bus the estate runs: the fix is there.
16. **Bounded output, per SPEC.md.** `--max <n>`, default 20, `0` means all, a negative is
    refused. The cap is **per verdict** — `stale`, `newer`, `differ` and `unknown`
    separately, so a night where six things moved does not hide the one source that
    stopped answering — and each capped verdict gets one `UPDATE MORE` line naming the
    remedy. **The count line prints on failure as well as success**, about the world.
17. **The tool stamps, and nothing read from a file or a server is a clock.** The opening
    `at=` is the tool's own. **A version is its whole identity string** after rule 4's read
    or 4a's, and two of them compare to exactly one of: **EQUAL**, the same bytes, current;
    **OLDER** or **NEWER**, an order this tool has *verified*; **DIFFERENT**, unequal with
    no order known. Order is known in one case only: both sides are release tags of the same
    entry — a release tag being, after the one leading `v`, nothing but `\d+(\.\d+)+` — of
    equal length, compared as integers component by component, so `1.10.0` is after `1.9.0`;
    unequal strings that come out equal that way are DIFFERENT (`1.09.0` against `1.9.0`);
    tags of unequal length are DIFFERENT, never ordered: `1.9` against `1.9.0` or `1.9.1` is
    DIFFERENT. Order is never known between a tag and a pseudo-version (Go would place
    `v0.12.1-0.<stamp>-<commit>` between two tags; this tool has not verified that and says
    DIFFERENT), never between two pseudo-versions, never for a digest, never across two
    tools (rule 15). **STALE is the line for verified OLDER and for nothing else**; NEWER
    and DIFFERENT print under their own names; a person looks; not EQUAL is 1.
18. **Every refusal names its remedy.** No refusal here ends at the reason: the
    missing flag, the malformed line, the script to wrap the command in, the owner's own
    pull of the weights — the next thing to type is on the line.
19. **`--kind <k>` restricts the run, not the output.** Repeatable, the only filter; absent,
    every kind runs. A kind not named is **neither read nor printed** — no `installed` argv
    starts for it and no GET is made — so the first line's `kinds=` and the count line's
    `checked=` are the filtered run while `entries=` stays the file's count. An unknown
    `--kind` is its own refusal, exit 2, naming the value and the five kinds, and no line.
20. **`report` reads the file alone: an explicit manifest, exact paths allowed, no bus.**
    `nova-update report --file <path>` takes the same six-field file (rule 2) and runs only
    each entry's `installed` argv — never `latest`, never a GET, never `apply` — so the base
    inventory needs no network, no recipients, no `--as`, no bus checkout: it prints and
    exits (#121: Freddy's stateless jobs read stdout). The manifest is explicit: only the
    file's argv run; an argv[0] carrying a `/` is that executable and no PATH is searched,
    so a bench with two installation roots names each path (Johnny, #121); a bare name
    resolves as rule 4 says; no home-directory scan, no wildcard discovery, no shell, no
    read of any private self. A shipped Nova-only example, `testdata/nova.tsv`, names the
    estate's own tools by their `version` verb; its `latest` column is
    `github:mas-bandwidth/nova-tools` on every line, a real source `report` never asks, so
    rule 2 holds as written and `check` over the same file is a check. The host is the
    caller's word: `--host <label>` prints as given, absent `-`; nothing on the box is asked
    to name it. `--kind` (rule 19) and `--max` (rule 16, per line kind: `TOOL`, `UNKNOWN`,
    `CHANGED`) hold.
    **`nova-version` is a second entry point on this one implementation** — its `report` and
    `send` are these flags under that name, the same code, no second reader and no second
    spec — so whichever name starts it, rules 20–26 and 9–13 hold for it (#121: one shared
    reader behind both; Emma's focused entry point stands).
21. **A report line carries the whole identity raw, beside the key `check` would compare.**
    One `REPORT TOOL` line per entry that answered: `version=` is the key — rule 4's read,
    or rule 15's for `kind=pin` — and `raw=` is the observed first line, whole, one
    `internal/oneline` token; `+dirty`, `-rc1` and the pseudo-version stamp stay in both.
    For a model, `raw` is the matched `ollama list` row from rule 4a, so a changed
    digest changes the retained observation; the unchanged table header cannot hide it.
    Both are on the line because they differ in what they can say: an opaque commit or a
    `devel` build is an identity this bench honestly runs though no order is known for it
    (rule 17), so it is reported, never dropped for lacking a dotted number. A first line
    whose second token is `devel` or a bare commit, `^[0-9a-f]{7,40}$`, has no key,
    `version=-`, and the raw line is the fact: `nova-wake devel darwin/arm64 go1.27.1` never
    keys the toolchain's `1.27.1` as the tool's version (the same clause in rule 4 keeps
    `check` from comparing it). The line is one observation, never a comparison with a
    latest: `report` says what is here, `check` says what is current.
22. **A tool that is missing, refuses or times out is UNKNOWN, never a version nor zero.**
    `REPORT UNKNOWN` names the reason and the remedy: rule 4's `not_found`; a non-zero exit
    named by code, `raw=` kept when a line was printed; a timeout; an empty first line. The
    count line still prints, `unknown=` counts them and `known=` does not, and the run exits
    1: a partial inventory is a report with an explicit non-success, not a shorter list that
    reads complete. A `--send` that lands proves the report was delivered, not that the
    inventory is whole or that anybody adopted anything (#121).
23. **Each subprocess is bounded in time and output.** `--timeout <d>` per argv, default
    `5s`; `--budget <d>` for the run, default `60s`, an entry not reached being UNKNOWN
    reason `budget` (rule 8); at most four children at once. A child's stdout and stderr go
    through `internal/bounded`, 64 KB each; a child reaching the cap is UNKNOWN reason
    `output`, wrap-it remedy — a tool that prints a banner is wrapped, not trusted to stop.
24. **`--draft` and `--send` are explicit; delivery is nova-bus's.** Absent both, nothing
    is composed. `--draft --as <friend> --to <who,who>` prints the report as a bus note body
    and sends nothing. The body is `From: <friend>`, `To: <who,who>`, one fixed
    `Subject: versions on <host> at <stamp>` — `host` as `--host` says or `-`, the stamp the
    run's `at=`; no flag names a Subject — a blank line, since nova-bus reads every line
    before the first blank one as a header, then the same lines the report printed, `--max`
    included: past `--max` tools the body carries the `MORE` line, a partial inventory that
    says so (rule 22), never a bug; `--max 0` sends them all. `--send` takes the draft's
    flags plus `--bus <path> --remote <r> --branch <b>`. Delivery uses the prepared
    artifact protocol in [SPEC-BUS-DELIVERY.md](SPEC-BUS-DELIVERY.md): `nova-bus prepare`
    validates and assigns identity without sending, then `nova-bus send --prepared-stdin`
    publishes or confirms the same artifact. The reporter does no Git of its own.
    Missing `--as`, `--to`, `--bus`, `--remote` or `--branch` is exit 2 naming the flag.
    No recipients come from the inventory's owner column. Only a confirmed `SEND OK`
    with `pushed=true` records delivery; failures and interrupted attempts never do.
    The whole successful SEND line is preserved as `line=`. With `--snapshot`, the
    pending artifact is saved before the sending child starts; without it, each
    explicit send is a new intention with in-process retry only. Help states that
    cross-process recovery needs the caller-named snapshot.
25. **Unchanged state is the caller's to suppress, through a snapshot file the caller
    names.** Absent `--snapshot <path>`, no file is read or written (rule 9: nothing under
    `$HOME`, no state file of this tool's own). Present, the run reads the previous
    snapshot if any, compares per `name` the `raw` identity and the status, writes the new
    one atomically (a temp file beside it, then rename) and prints `changed=<yes|no>` on the
    count line with one `REPORT CHANGED name= was= now=` per entry that moved; the first
    run is the baseline, `changed=yes`, `was=-`; a tool turning UNKNOWN, or back, moved.
    A caller-named snapshot uses a sibling `<snapshot>.lock` kernel lock to serialize
    writers across atomic renames; process death releases ownership. The stable empty
    lock file is not evidence of a running process and is not automatically deleted.
    The file is JSON with `observed`, `delivered` and `pending`. `observed` is keyed
    by `name`, each value
    `raw`, `status` (`known` or `unknown`) and `at` — the machine-readable snapshot #121
    asked for — and `delivered`, below. **`at=` is never compared**: two snapshots differing
    only in their stamps are `changed=no` — a timestamp refresh is not a changed version
    (#121). **Observed state and delivered state are two records** (Stella, #127): every run
    with `--snapshot` writes `observed`; only a `SEND OK … pushed=true` writes `delivered`,
    keyed by the send's scope — `as`, `to` sorted, absolute `bus`, `remote`, `branch`, and explicit `host`, joined — and
    holding the `observed` map the body carried, nova-bus's `id` and the `at`. A local-only
    run, a `--draft`, a refused or a failed send write no `delivered`. With `--snapshot`,
    `--send` composes and sends when the scope has no `delivered` record, when that record's
    `observed` differs from tonight's, or when a `pending` stands (below); otherwise it
    prints `REPORT NOTE unchanged since <id> to <to>; nothing sent`, `sent=no`. The
    suppression compares against what that recipient was confirmed to have, never the last
    observation, so a report before a send, a failed send before its retry, and a snapshot
    made for another recipient never quiet a send. Without `--snapshot` every `--send`
    sends. **Preparation and pending precede mutation.** Save the delivery scope,
    exact prepared artifact and observed map atomically before starting send. A failed
    preparation cannot have delivered; an interrupted send retains the prepared ID.
    On the next explicit `--send`, resolve pending first through the same prepared-send
    protocol even when the observation is unchanged. Confirmed publication, including
    already-published, advances `delivered` and clears pending atomically. If the current
    observation differs, resolve the older pending report before preparing another.
    If unresolved within budget, print its ID and an explicit pending gate, exit 1,
    and do not send a newer report. No report is silently discarded or recreated under
    another ID. Unrelated local commits are never published as a side effect of retry.
    Exit combines inventory completeness and delivery outcome; `changed=` alone never
    makes a complete invocation fail. A stateless invocation has no retained recovery
    promise across process death; the caller chooses that by omitting `--snapshot`.
26. **No hidden timer, install or automatic send.** `report` has no `--watch`, no loop, no
    daemon; it runs when a person or a unit a person wrote starts it, and ends inside its
    budget. It installs nothing, pulls nothing, and no report line is a name for `apply`
    (rules 9, 10). It sends only under `--send`; `--draft` never sends; a plain `report`
    with `--as` and `--to` given still sends nothing — the send is a flag the caller typed.

## The versions file

One header line, then one entry per line, tabs between fields; `#` opens a comment (rule
2). The header exactly as shown, then five entries as written today:

```
name	kind	installed	latest	apply	owner
gh	tool	gh --version	github:cli/cli	brew upgrade gh	rowan
sops	tool	sops --version --disable-version-check	github:getsops/sops	brew upgrade sops	rowan
opencode	harness	opencode --version	npm:opencode-ai	npm install -g opencode-ai@{version}	freddy
qwen3-coder:30b	model	ollama list	ollama:qwen3-coder:30b	none	stella
nova-wake-pin-nova-bus	pin	nova-wake version	local:nova-bus version	none	rowan
```

`owner` is the line who answers when that entry is not current, on every STALE, NEWER or
DIFFERENT line, so the morning names a person, not only a number.

## The verbs

```
nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update apply --file <path> <name> [--version <v>] [--timeout <d>]
nova-update report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who> | --send --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update help
```

Those three usage lines are the string `nova-update help` prints, byte for byte: one string
in the binary, so the spec and the help cannot drift apart. `--kind <k>` is rule 19. No
`--only-stale` (the output is only findings), no `--quiet` (the count line is the point).
`nova-version report …` and `nova-version send …` are the `report` line's flags under that
name, `send` implying `--send` (rule 20); its `help` prints those two lines the same way.
`nova-version report --as x --to y` prints the inventory and composes nothing (rule 26);
Emma's ready-to-send draft (#121) is `nova-version report --draft …`, the flag typed.

## Exit codes and the output grammar

Per SPEC.md: **0** every entry current, an `apply` that left the box on the target, or a
`report` whose every entry answered (and, under `--send`, whose note nova-bus took); **1**
the tool saying NO — anything STALE, NEWER, DIFFERENT or UNKNOWN, an `apply` whose after
is not the target, a `report` with an UNKNOWN or a send nova-bus refused or did not
confirm (`sent=uncertain`); **2** could not run, every refusal the rules name.

```
UPDATE at=<stamp> file=<path> entries=<n> kinds=<k,k,k> timeout=<d> budget=<d> max=<n>
UPDATE <STALE|NEWER|DIFFERENT> name=<name> kind=<kind> installed=<v> latest=<v> path=<path> source=<source> owner=<owner>
UPDATE UNKNOWN name=<name> kind=<kind> installed=<v|-> path=<path|-> source=<source>: <reason> (<remedy>)
UPDATE <OK|FAIL> checked=<n> current=<n> stale=<n> newer=<n> differ=<n> unknown=<n> pins=<n> took=<d> file=<path>
UPDATE MORE kind=<stale|newer|differ|unknown> shown=<n> total=<t> <remedy>
UPDATE NOTE <something true about this run that is not a finding>
UPDATE REFUSED: <reason> (<remedy>)
APPLY BEFORE name=<name> kind=<kind> installed=<v|-> path=<path|-> latest=<v> source=<source>
APPLY RUN name=<name> argv=<n> version=<v>: <command, escaped>
APPLY AFTER name=<name> installed=<v|-> was=<v|->
APPLY <OK|FAIL> name=<name> from=<v|-> to=<v|-> took=<d>[: <reason>]
APPLY REFUSED name=<name>: <reason> (<remedy>)
REPORT at=<stamp> file=<path> host=<label|-> as=<friend|-> entries=<n> kinds=<k,k,k> timeout=<d> budget=<d> max=<n> snapshot=<path|->
REPORT TOOL name=<name> kind=<kind> version=<v|-> raw=<first line, escaped> path=<path>
REPORT UNKNOWN name=<name> kind=<kind> path=<path|-> raw=<line|->: <reason> (<remedy>)
REPORT CHANGED name=<name> was=<raw|-> now=<raw|->
REPORT MORE kind=<tool|unknown|changed> shown=<n> total=<t> <remedy>
REPORT SENT to=<who,who> via=<nova-bus argv, escaped> line=<nova-bus's SEND OK line, escaped>
REPORT <OK|FAIL> checked=<n> known=<n> unknown=<n> changed=<yes|no|-> sent=<yes|no|uncertain|-> took=<d> file=<path>
REPORT NOTE <something true about this run that is not a finding>
REPORT REFUSED: <reason> (<remedy>)
```

`UPDATE`, `APPLY` and `REPORT` are the first tokens, `OK` and `FAIL` the verdicts and the
**last** line; the rest are informational second tokens, declared here as SPEC.md requires,
on stdout, `REFUSED` and `FAIL` on stderr; every value is one `internal/oneline` token.

## First run — the lines a stranger pastes

```
$ go install ./cmd/nova-update
$ nova-update check --file ./cmd/nova-update/testdata/versions.tsv --max 3
UPDATE at=2026-09-12T01:00:00Z file=./cmd/nova-update/testdata/versions.tsv entries=5 kinds=harness,model,pin,tool timeout=5s budget=60s max=3
UPDATE UNKNOWN name=nova-wake-pin-nova-bus kind=pin installed=- path=- source=local:nova-bus\x20version: not_found (nova-wake is nowhere on /Users/x/go/bin:/opt/homebrew/bin:/usr/bin:/bin — install it or add its dir to PATH)
UPDATE STALE name=gh kind=tool installed=2.100.0 latest=2.101.0 path=/opt/homebrew/bin/gh source=github:cli/cli owner=rowan
UPDATE DIFFERENT name=qwen3-coder:30b kind=model installed=07d35212591f latest=096fdbd02fe6 path=/opt/homebrew/bin/ollama source=ollama:qwen3-coder:30b owner=stella
UPDATE UNKNOWN name=opencode kind=harness installed=1.18.30 path=/opt/homebrew/bin/opencode source=npm:opencode-ai: no answer in 5s (raise --timeout, or ask again when the registry answers)
UPDATE FAIL checked=5 current=1 stale=1 newer=0 differ=1 unknown=2 pins=0 took=6.2s file=./cmd/nova-update/testdata/versions.tsv
$ nova-update apply --file ./cmd/nova-update/testdata/versions.tsv qwen3-coder:30b
APPLY REFUSED name=qwen3-coder:30b: a model is not installed by this tool (the owner pulls it after the evaluation the owner chose: ollama pull qwen3-coder:30b; nova-local status --list shows it)
$ nova-update apply --file ./cmd/nova-update/testdata/apply.tsv fixture-tool
APPLY BEFORE name=fixture-tool kind=tool installed=1.0.0 path=./testdata/fixture-tool latest=1.1.0 source=github:example/fixture
APPLY RUN name=fixture-tool argv=2 version=1.1.0: ./testdata/fake-install.sh\x201.1.0
APPLY AFTER name=fixture-tool installed=1.1.0 was=1.0.0
APPLY OK name=fixture-tool from=1.0.0 to=1.1.0 took=0.1s
$ nova-update report --file ./cmd/nova-update/testdata/nova.tsv --host studio
REPORT at=2026-09-12T01:00:00Z file=./cmd/nova-update/testdata/nova.tsv host=studio as=- entries=3 kinds=tool timeout=5s budget=60s max=20 snapshot=-
REPORT TOOL name=nova-bus kind=tool version=0.12.1-0.20260912135226-0459069+dirty raw=nova-bus\x20v0.12.1-0.20260912135226-0459069+dirty\x20darwin/arm64\x20go1.27.1 path=/Users/x/go/bin/nova-bus
REPORT TOOL name=nova-merge kind=tool version=- raw=nova-merge\x200459069 path=/Users/x/go/bin/nova-merge
REPORT UNKNOWN name=nova-wake kind=tool path=- raw=-: not_found (nova-wake is nowhere on /Users/x/go/bin:/opt/homebrew/bin:/usr/bin:/bin — install it or add its dir to PATH)
REPORT FAIL checked=3 known=2 unknown=1 changed=- sent=- took=0.4s file=./cmd/nova-update/testdata/nova.tsv
```

The report block is the fixture's shape, not a measured Studio run: nova-merge's line is
its commit-only `version` print (#121 measured one), so `version=-` and the raw line is the
report; no GET is made and nothing is sent — `--send` is absent, so nova-bus never starts.

Inside `go test` every GET source above is an `httptest` server the parser's table of hosts
points at and every argv — `installed`, `local:`, `apply` — a script under `testdata/`; the
fixture's model digests are the measured `qwen3.6:35b-a3b` pair under another name. Pasted
in a terminal, line 2 reaches GitHub twice, npm and the registry once each, installing
nothing; the last line applies a fixture, because **`apply` of a real entry runs the real
installer on the box it is pasted into**. Do both on purpose. The `kind=pin` entry is
UNKNOWN here because nova-wake is not on this box's PATH; from one tag with its bus it reads
EQUAL; a DIFFERENT pair prints first, `pins=1`.

## Tests this spec demands

One test per rule, named for it, each proven able to fail by a mutation first. Every latest
source is a local `httptest` server, and the tripwires live together: outside the parser's
table and the docs, no `api.github.com`, `registry.npmjs.org`, `formulae.brew.sh` or
`registry.ollama.ai`; nowhere `localhost:11434` — nova-update never asks it — `os.Getwd`,
`os.UserHomeDir`, a hardcoded path, `exec.Command("sh"`, `"-c"`, `ollama pull`, `brew
install` or `npm install`.

1. `TestTheFileComesFromAFlag`: no `--file` is exit 2 printing `refusing to guess` and
   naming `--file`; a `--file` that does not exist is exit 2 naming the path.
2. `TestSixFieldsOrARefusal`: five, seven and empty fields are each exit 2 naming the line
   number and the count; a first line that is not the header is exit 2 naming line 1 with
   the put-it-back remedy, entry 1 never skipped; a header plus a `#` comment loads,
   `entries=` counting neither and neither refused as an unknown kind; one refusal, not one
   per line; 500 good lines and one bad refuse on the bad one and start nothing.
3. `TestACommandIsArgvNeverAShell`: `sh -c echo 1.2.3` execs `sh` with four arguments,
   so `sh` runs `echo` with `1.2.3` as `$0` and prints an empty line: UNKNOWN, never
   `1.2.3`; `;`, `&&`, `|`, `$(`, backtick and `*` exec once with those bytes literal at
   each of the three exec sites; two adjacent spaces in a field is exit 2 naming the line.
4. `TestTheInstalledReadIsTheWholeIdentity`: rule 4's eleven fixture lines each yield the
   stated read; `gh`'s second line is not read; `go1.27.1` yields `1.27.1`; the nova-bus
   line yields `0.12.1-0.20260912135226-0459069+dirty` byte for byte, and a read that yields
   `0.12.1` is the mutation that matters; no dotted number is UNKNOWN with the wrap-it
   remedy, exit 1, as is a non-zero exit that printed one; stderr is read only when stdout
   is empty; `nosuchbinary --version` and an empty `PATH` are UNKNOWN reason `not_found`,
   remedy naming argv[0] and the PATH, never the wrap-it one; a resolved entry has `path=`.
4a. `TestAModelsVersionIsItsDigest`: `# Modelfile generated by "ollama show"` is UNKNOWN
    under rule 4, which is why 4a exists; an `ollama list` fixture yields the matching row's
    ID, no matching row and an entry named without its tag each being UNKNOWN with the
    not-on-this-box remedy; a fixture registry-v2 body yields its own SHA-256, and that body
    without `layers`, and again without `schemaVersion`, is UNKNOWN reason `shape`; equal
    lowercase twelve-hex digests are EQUAL, different ones DIFFERENT with both on the line
    and `STALE` nowhere.
5. `TestAnUnknownKindRefuses`: each of the five kinds loads; `kind=weights` is exit 2
   naming the line and listing the five.
6. `TestOneBoundedGetPerEntry`: each scheme reads its documented field and a `tag_name` of
   `v2.101.0` prints `latest=2.101.0`; one request per entry except a `github:` answered
   404, which makes exactly one more to `/tags?per_page=1`, reads `[0].name` through rule
   4's read so a `v0.11.0` there prints `latest=0.11.0` and is EQUAL to an installed
   `0.11.0`, names that endpoint in `source=`, and whose `[]` is UNKNOWN reason `no release
   and no tag`; a 257 KB body and a four-hop redirect are UNKNOWN; `npm:` requests a path
   ending `/latest`; `source=` equals `latest`.
7. `TestADeadSourceIsNeverOk`: a 500 naming it, then a 403 and a 429 each naming its
   `x-ratelimit-reset`, a registry 404 giving `tag_not_found` with the `ollama.com` remedy,
   never `shape` and never rule 4's `not_found`, a hang past the timeout, a `{}` and invalid
   JSON each give one `UPDATE UNKNOWN` and exit 1; `up to date` never appears; every source
   dead exits 1 with `current=0`; a dead source is asked once in a run and again in the
   next — the no-retry, no-cache assertion.
8. `TestTheRunIsBounded`: an injected clock, 40 entries, a server answering in 3s:
   `--budget 10s` ends inside 10s with a count line, the unreached UNKNOWN reason `budget`,
   exit 1; a counting handler sees no fifth in flight; the defaults are on the first line.
9. `TestCheckWritesNothingAndInstallsNothing`: `HOME` and the cwd are fresh temp dirs, empty
   after a `check` over every kind, and only the entries' own `installed` argvs start — over
   a file whose every entry is STALE, NEWER, DIFFERENT or UNKNOWN, no `apply` argv.
10. `TestApplyNeedsANameFromAPerson`: no name is exit 2 saying a name is required;
    `--all`, `--stale` and `-y` are unknown flags; two names is exit 2.
11. `TestApplyRefusesAModelByName`: `apply` of every `kind=model` entry is exit 2, carries
    the model's name, names `ollama pull` with that name and `nova-local status --list`,
    names no verb SPEC-LOCAL cut — `quarantine`, `eval` nowhere in the output, the mutation
    that matters — and starts no process.
12. `TestApplyRefusesAnUnnamedThing`: `g`, `GH`, `gh ` and `gh-cli` against a file
    carrying `gh` are each exit 2 naming the file and the entry count.
13. `TestApplyInterpolatesOnlyTheVersion`: an argv with `{version}` twice, a `$HOME`, a
    `{name}` and a `*` substitutes the version twice and the rest byte for byte; `--version
    9.9.9` overrides the latest and is echoed on `APPLY RUN`; a dead source with no
    `--version` is exit 2, no process started; an `apply` of `none` is exit 2, by hand.
14. `TestApplyAfterMustEqualTheTarget`: a fake installer landing exactly the target is
    `APPLY OK` exit 0; one changing nothing is `APPLY FAIL` exit 1 with `from=` equal to
    `to=`; one landing `1.1.1` against a target of `1.1.2` is `APPLY FAIL` naming both and
    **not** OK, the mutation that matters; a non-zero exit is `APPLY FAIL` naming the
    code; `--version` against an argv with no `{version}` is exit 2; after uses `installed`.
15. `TestABrokenPinPrintsFirst`: with four non-current entries of four kinds the `kind=pin`
    line precedes the rest; two pin entries, one DIFFERENT, print `pins=1`; a pin entry
    issues zero HTTP requests; `nova-wake version` and `nova-bus version` answering one
    identity — `v0.12.0` twice, then one pseudo-version twice, then `devel` twice — are
    EQUAL and current; `v0.12.0` against `v0.10.3` is DIFFERENT; two pseudo-versions
    differing only in the commit, `…-0459069` against `…-88f0b0d`, are DIFFERENT; each
    verdict equals `wake.AcceptBus` on the same two strings; `STALE` appears on no pin line.
16. `TestUpdateOutputIsBoundedAtTheLargestPlausibleState`: 200 entries, 60 stale, 30 newer,
    30 differ and 60 unknown, print at most `4 * --max + 8` lines over both streams; each of
    the four verdicts gets a `MORE` line with its true total; `--max 0` prints all 180;
    `--max -1` is exit 2.
17. `TestOrderIsVerifiedOrNotClaimed`: `1.9.0` against a latest `1.10.0` is STALE and
    `1.10.0` against `1.9.0` is NEWER, never STALE; `1.9` against `1.9.0` is DIFFERENT,
    never EQUAL, and so is `1.09.0` against `1.9.0` — equal as integers, unequal bytes;
    `v0.12.1-0.20260912135226-0459069` against the same with `88f0b0d` is
    DIFFERENT; `0.12.0` against `0.12.1-0.20260912135226-0459069` is DIFFERENT and `STALE`
    is nowhere in the output — the mutation that matters; `at=` is the clock, never a body.
18. `TestEveryRefusalNamesItsRemedy`: every refusal in the package lives in one table, which
    the test walks: each ends in a parenthesised remedy naming a command, a file, a URL,
    or the values allowed, and removing one turns the test red.
19. `TestTheKindFilterRestrictsTheRun`: `--kind tool` over a file of all five kinds starts
    only the tool entries' `installed` argvs and only their GETs — a counting handler sees
    no others — prints no line of another kind, and its `kinds=` and `checked=` are the
    filter's while `entries=` stays the file's; two `--kind` flags cover both; `--kind
    weights` is exit 2 naming the value and the five kinds, no line number.
20. `TestReportReadsTheFileAlone`: `report` over the five-entry fixture starts exactly the
    entries' `installed` argvs and makes zero HTTP requests — a counting handler on every
    source sees none — and no `apply` argv; `./testdata/fixture-tool` as argv[0] runs that
    file under an empty `PATH` with `path=` echoing it, while a bare name under the same
    `PATH` is UNKNOWN `not_found`; no `--file` is exit 2 `refusing to guess`; `--host
    studio` prints `host=studio`, absent `host=-`, and `os.Hostname` joins the tripwires;
    `nova-version report` with the same flags prints the same lines but the stamp; 200
    entries print at most `3 * --max + 8` lines with a `MORE` line per capped kind.
21. `TestTheReportKeepsRawBesideTheKey`: rule 4's eleven fixture lines each print `raw=`
    equal to the first line, escaped, and `version=` equal to the stated read;
    `nova-merge 0459069` and `nova-wake devel darwin/arm64 go1.27.1` each print `REPORT
    TOOL` with `version=-` and the whole raw line, `go1.27.1` inside `raw=` — never UNKNOWN,
    and `version=1.27.1` nowhere, the mutation that matters; under `check` the same two
    lines are UNKNOWN reason
    `no_release_identity` with the stamped-build remedy, never `installed=1.27.1`, and a
    `check` script printing 1 MB is UNKNOWN reason `output` (rule 4's cap); a `kind=pin`
    entry's key is the second token whole; `+dirty` survives in both fields.
22. `TestAMissingToolIsUnknownNeverZero`: `nosuchbinary version`, a script exiting 3 after
    printing a version (`raw=` kept, reason `exit 3`), a hang past `--timeout`, and an empty
    stdout and stderr are each one `REPORT UNKNOWN` naming reason and remedy, exit 1, the
    count line printed with them in `unknown=` and not in `known=`; `0.0.0`, `version=0`
    and `current` appear nowhere; three UNKNOWN of five is `REPORT FAIL checked=5 known=2
    unknown=3`, never a shorter `OK`.
23. `TestEachSubprocessIsBounded`: a script sleeping past `--timeout 1s` is UNKNOWN reason
    `timeout` in about a second, not the budget; 40 entries at 3s each under `--budget 10s`
    end inside 10s, the unreached UNKNOWN reason `budget`; a script printing 1 MB is
    UNKNOWN reason `output` with the wrap-it remedy; a counting fixture sees no fifth child
    alive at once.
24. `TestTheBaseReportNeedsNoBus`: with no bus checkout, no `--as`, no `--to` and a `PATH`
    without `nova-bus`, `report` prints its lines and exits by its inventory; `--draft --as
    rowan --to stella --host studio` prints a body whose first four lines are `From: rowan`,
    `To: stella`, `Subject: versions on studio at <the run's at=>` and a blank line — any
    other Subject is the mutation that matters here — then the report's lines, a 30-tool
    file under `--max 20` carrying its `REPORT MORE` line in the body, and starts no
    `nova-bus` (a fake on `PATH` counts zero runs); `--send`
    missing any one of its five flags is exit 2 naming that flag; `--send` complete runs the
    fake `nova-bus prepare` with the draft on stdin, then `nova-bus send` with
    `--prepared-stdin`, `--bus`, `--remote`, `--branch`, `--as` and the prepared artifact
    on stdin. Preparation refusal is `REPORT FAIL`, `sent=no`; interrupted or unconfirmed
    dispatch is `REPORT FAIL`, `sent=uncertain` with its prepared ID. No absent result
    line establishes that nothing was sent. A file whose owner names `stella` and no
    `--to` never sends to her.
25. `TestUnchangedStateIsTheCallersToSuppress`: without `--snapshot`, `HOME` and the cwd are
    fresh temp dirs and empty after the run; `--snapshot s.json` first writes it — JSON,
    `observed` keyed by `name`, each value exactly `raw`, `status`, `at`, and `delivered`
    and `pending` empty — and prints `changed=yes`; a second run with identical raw lines and an injected
    clock one hour on prints `changed=no` and no `REPORT CHANGED` line — the mutation that
    matters; a third with one raw differing prints one `REPORT CHANGED name= was= now=` and
    `changed=yes`; a tool turning UNKNOWN is a change, and back is another. Delivery, with a
    fake `nova-bus` on `PATH`: a `--send --snapshot s.json` the fake confirms (`SEND OK …
    pushed=true`) writes `delivered` for its scope, and the same send on the unchanged run
    starts no `nova-bus`, prints `REPORT NOTE unchanged since <id> to stella; nothing sent`
    and `sent=no` — the quiet repeat; a local-only `--snapshot` run, then the first `--send`
    for that scope, sends — a send quieted by an observation nobody was sent is the mutation
    that matters; the fake refusing (`SEND REFUSED`), then the same send unchanged, sends
    again; a send confirmed `--to stella`, then the same observation `--to emma`, sends; the
    real bare-Git cases from SPEC-BUS-DELIVERY.md prove recovery: refused push followed
    by an unchanged retry lands one original note; lost acknowledgment finds the same
    published note; child death before output reuses saved pending identity. Exercise
    death before/after note, INDEX and commit writes; changed observation behind pending;
    unrelated local work refusal and a racing remote writer. Assert remote note bytes,
    ID and INDEX count, not just a fake success line. A run killed mid-snapshot-write
    leaves the previous snapshot whole. A confirmed unchanged run invokes no bus process.
26. `TestTheReportHasNoClockNoInstallNoAutomaticSend`: `--watch`, `--every` and `--loop` are
    unknown flags costing one line; over a file whose every entry is UNKNOWN or changed, no
    `apply` argv and none of `brew`, `npm`, `go install` or `ollama pull` starts; a plain
    `report` and a `--draft`, each with `--as` and `--to` given, make zero `nova-bus` runs;
    a fixture whose every argv hangs still exits inside `--budget`.

Beside those: the first-run block runs against the fixture, compared by shape per
ONBOARDING.md 5(c); `nova-update help` prints the verbs block on stdout, exit 0; a
bare invocation or a flag typo costs one line, never a banner.

## Open questions — each with a default, and the default stands unless Glenn says otherwise

1. **Ollama publishes no JSON API, but its registry speaks registry-v2.** Default: rule 4a's
one manifest GET, digest as text; the rejected alternative, scraping
`ollama.com/library/<model>/tags`, is 50 KB of HTML, no documented shape, no dotted number.
A registry that stops answering registry-v2 JSON is UNKNOWN, never OK.
2. **Where the file lives.** Default: `versions.tsv` at the root of nova-tools, one
   for the estate, the path always from `--file`.
3. **Brew versus a release binary for the same tool.** `gh`, `sops` and `age` each
arrive either way and the two disagree by days. Default: the file names the source
that installed the copy on this box; a mismatch is a one-line fix to the file, not a
second source per entry.
4. **Prepared delivery is a required implementation dependency.** The bounded protocol
in SPEC-BUS-DELIVERY.md replaces waiting for an unrelated future push. Until that bus
mode is implemented and its real-Git recovery witnesses pass, the reporter's sending
path is not ready for adoption. Local inventory, draft and update-choice implementation
can proceed independently. No timer or reporter-owned Git is added.
