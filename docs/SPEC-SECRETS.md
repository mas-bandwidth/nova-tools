# nova-secrets — specification

**Revision 2, 2026-09-12, with read 1's seventeen findings applied. This revision
replaces a store of our own with a thin tool over
[sops](https://github.com/getsops/sops) and [age](https://age-encryption.org).**
Revision 1 specified a store of our own; Glenn ruled against it on 2026-09-11 —
*"something generic and open source, not evil"*, and *"git is our substrate"*. The
store is [`mas-bandwidth/secrets`](https://github.com/mas-bandwidth/secrets)
(private), already built: `.sops.yaml` carrying one recipient rule per file,
sealed yaml files only, its README carrying the protocol. **That repository is the
store. This tool never becomes one.**

Four verbs at the **credential layer**. The sentence the verb count is measured
against is this one:

> **An AI runs with its own API keys, and nothing else can read them.**

`exec` is the half that makes it *run*; `check` is the half that makes *nothing
else can read them* a fact somebody proved this morning rather than a belief.
`keygen` exists because a new AI or a new bench cannot use either until it has a
keypair; `names` answers *what is in my file* **with no key at all**, because sops
seals values and leaves key names in the clear, so a name is a parse and never a
decrypt. Everything else a person wants to do to a secret is `sops`, `git` or the
provider's console, and this tool refuses it **by name, with where it lives**.

This spec is normative. It is a sibling of [SPEC.md](../SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line guarantee,
the field escape, the cap-and-count law — governs here unchanged except where
`exec`'s own exit table says otherwise, in its own section. That deviation is
**nova-sandbox's**, adopted verbatim rather than invented here: an exec verb
cannot return 2 for its own refusal, because a command that exits 2 on its own
would be indistinguishable from it (SPEC-SANDBOX, *Exit codes*). If the code and
this document disagree, one of them has a bug, and the tests decide which.

**Certainty is exactly as strong as who can read the private key file, and
nothing in this tool changes that.** That sentence is inherited verbatim from
revision 1 and the store's README, and it is the first thing a reader should
distrust the rest of this against. sops and age give *cryptographic* certainty
that a ciphertext in git yields nothing to a line holding no key; they give
**nothing** about a second process running as the same unix user as the AI that
holds one. That half is won by per-AI unix users and nova-sandbox's read sets
([security#20](https://github.com/mas-bandwidth/security/issues/20)); this spec's
job is to not get in their way.

## The failures it closes

| the failure, from the record | what closes it |
|---|---|
| Glenn, 2026-09-11: *"obviously, you need access to rowan only secrets"* / *"and others shouldn't have those"* — every line and every worker ran as one unix user, so Rowan's token file was readable by all of them | one age keypair per AI; one sealed file per AI; `check`'s **negative** half, which fails when this AI's key opens another AI's file |
| [security#1](https://github.com/mas-bandwidth/security/issues/1): one org-owner credential for every line, worker and runner; a commit could not say which line made it | a `GH_TOKEN` per AI in that AI's own file, fine-grained; the swarm pool's is its own file and read-only |
| revision 1: a store of our own, with our own cipher-adjacent code to review, in a repo whose reviewers are us | sops + age, both audited by people who are not us, both pinned by version, neither linked into our binary |
| a secret read by `security find-generic-password` on the keeper bench only — invisible to any other bench, unbacked up, and gone with the Keychain | the sealed file is in git: cloned, backed up, reviewable as ciphertext by anyone, openable by one seat and the recovery key |
| a value pasted into a launcher, a plist, an `opencode.json`, a note or a log, because there was no one place it lived | one place it lives; one verb that hands it to a process; and **no verb that prints it**, ever, so there is nothing to paste |
| a rotation that was believed done because the file changed | rotation is the provider's revocation first; the re-seal second; and a probe run third — stated in **Rotation** in those three steps |
| the wall reading the key file, which is the one read a sandbox must never have to allow | the order in **The launcher**: `nova-secrets exec` sets the environment and then *becomes* the sandbox, so the store and the key are in **no** read set |

## What this tool is, in one paragraph

`nova-secrets` reads one sealed yaml out of a git working copy by running the
`sops` binary at a path the caller named, keeps the plaintext in its own memory
for the length of one call, and either **replaces itself** with a command that
has those values in its environment (`exec`), **prints the key names**
(`names`), **proves the store's invariants** (`check`), or **writes one age
private key and prints its public half** (`keygen`). It links no cryptography,
opens no network socket, starts no shell, writes no state of its own, and reads
no Keychain. It is about two hundred lines of Go over two binaries it did not
write, and the day a better generic store exists it should be about two hundred
lines of Go over that one instead.

## The model

**One age keypair per AI per bench.** The private half is a file in that AI's own
home, mode `0600`, in a directory mode `0700`, at a path that comes from a flag
(`--key`) and from nowhere else. The public half is not a secret and is printed,
pasted and committed.

**One sealed yaml per seat, `<name>.yaml`, in the store** (Glenn, decided
2026-09-12 — open question 1, below, now a record rather than a question). A line
with one seat has one file; Rowan has two, because Rowan has two benches with
different exposure, and `--as` names the file. Its `.sops.yaml` rule lists as
recipients: the keypairs of the benches that seat runs on, plus **Glenn's
recovery key**.

**A file-shaped secret is not in that file.** A value with a newline in it — an
SSH key, an age key — lives in the **sibling** `<name>-files.yaml`, same
recipients, its own rule. `exec` never opens a `-files.yaml`; `sops exec-file`
does. One seat's set is the pair, split by *how a value reaches a program* — an
environment variable, or a path — because that is what decides which verb can
carry it. See **File-shaped secrets**.

**The recovery key** is Glenn's, generated at his console, kept **off every
bench** (his password manager or paper), and a recipient on every rule. It opens
every line's file, and this spec says so out loud rather than leaving a line to
find out: what it opens are API keys and service passwords Glenn pays for and can
revoke at any console, never a line's record, notes or memory — so it is not a
window into a line's privacy. It is for one thing, re-sealing a file when a bench
key is lost (`sops updatekeys`), and **every use is announced on the record**. A
file sealed before that key reached its rule does not have it until `updatekeys`
runs; `check` invariant 2 notices.

**A seat can decrypt the files its rules name, and no other.** This is a property
of the recipient lists and of nothing else — no permission bit, no path convention
and no check in this tool creates it, and `check` can only *observe* it. It is
observed from both sides, and the negative side is the one that matters:

> `check` finds this key's public half, then for **every** `*.yaml` in the store
> requires a decrypt to **succeed** if that file's own recipients list the public
> half and to **fail** if they do not. A file that opens and should not is
> `SECRETS CHECK FAIL` at exit 1 naming the file; so is a file that should open
> and does not. Comparing against the recipients rather than against the filename
> is what lets one seat hold several files without the negative half going red on
> the seat's own second file.

**Naming.** The file is per AI, so a key inside it is **the environment variable
its reader already reads**, unprefixed: `GH_TOKEN`, not `ROWAN_GH_TOKEN`. The
AI's name is the filename; putting it in the key as well is the filename twice,
and the first tool to read `ROWAN_GH_TOKEN` while every other tool on earth reads
`GH_TOKEN` is a tool nobody can use. A key name must match `[A-Z][A-Z0-9_]*`;
anything else is a refusal naming the key, because a name that is not a legal
environment variable is a value that would silently not arrive. The same law
governs a `-files.yaml`, whose keys are `--filename` arguments to `sops
exec-file` rather than environment variables, so that one law reads one way.

**Not everything in the file is sealed.** `.sops.yaml` carries an
`unencrypted_regex` for the fields that are facts rather than secrets — today
`^(SPACE_USER|SPACE_HOST)$` — and this tool treats an unencrypted field exactly
as it treats a sealed one: it is a key and a value and it goes into the child's
environment. The list of what may be in the clear is the store's decision, in
`.sops.yaml`, reviewed in a pull request, and this tool neither extends nor
audits it. See open question 4.

**What the model does not give you.** Read access to the *ciphertext* is not a
boundary: anybody who can clone the store holds every AI's sealed file, and that
is intended — it is what makes the store backed up, reviewable and portable. The
boundary is the private key file. And a *decrypt leaves no record anywhere*:
neither the store, nor git, nor this tool can tell you who opened what, or when.
That is stated here so that nobody builds a belief on top of an audit trail that
does not exist.

## The verbs

```
nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path> [--only <NAME,...>] [--require <NAME>]... -- <cmd> [args...]
nova-secrets names  --store <dir> --as <name> [--max <n>]
nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
nova-secrets keygen --as <name> --key <path> --age-keygen <path>
nova-secrets help
```

Four verbs and `help`. `exec` **runs**; `names` and `check` **read**; `keygen`
**writes exactly one file, outside the store**. `names` takes no key and no
`sops`: it reads key names out of the ciphertext. No verb writes into the store —
the store is edited by `sops` and committed by `git`, both of which are a
person's hands — and no verb reads and writes in one call.

**`--store <dir>` is the store's git working copy**, not a URL and not a
repository name: this tool does no network. A store that is not a directory, or
that holds no `.sops.yaml`, is a refusal naming both facts and the `git clone`
line — **exit 2**, or **125** when the verb is `exec`, as every refusal of `exec`'s
is.

**`--as <name>` selects the file**, `<store>/<name>.yaml`. It is not an identity
and proves nothing: the key decides what opens. A `--as` whose file is absent is
a refusal listing the names that *are* in the store, because the commonest form
of this mistake is a spelling. A `--as` ending in `-files` is refused naming
`sops exec-file`: that file is a sibling, never an `--as`.

**`--key <path>` is the age private key**, and it is required on every verb but
`help` and `names` — on `keygen` it is the path to write. **No default, no
`$SOPS_AGE_KEY_FILE` fallback, no `~/.config/sops/age/keys.txt`,** and no
environment variable of any kind is consulted by this tool for anything.

**The sops child gets a built environment, not an edited one**, and this rule has
a measurement under it. nova-secrets **sets** `SOPS_AGE_KEY_FILE` to the path
`--key` gave and removes every other `SOPS_*` variable, so a caller's variable
cannot redirect which key is used. That is not enough: sops **also** loads
`$XDG_CONFIG_HOME/sops/age/keys.txt` (on darwin falling back to
`$HOME/Library/Application Support/sops/age/keys.txt`), reached through `HOME` and
`XDG_CONFIG_HOME`, which are not `SOPS_*`. Measured with throwaway keys,
2026-09-12: with a second identity in that default file and `SOPS_AGE_KEY_FILE`
pointing at a key that is *not* a recipient, `sops -d` **succeeded**; with the
default file absent it exited 128, `identity did not match any of the recipients`.
So nova-secrets also sets `HOME` and `XDG_CONFIG_HOME`, **for the sops child
only**, to an empty directory under its own temp dir, removed when the call ends.
Without it the negative half of `check` can go red naming a rule that is right —
or stay green while `exec --as <somebody else>` succeeds off an identity nobody
named.

A key file whose mode is not `0600`, or whose directory is not `0700`, is a
**refusal on every verb that takes one** naming `chmod 600` and `chmod 700` — not
a warning, because the entire boundary is that file's mode.

**`--sops <path>` is the sops binary**, absolute, from a flag. PATH is a guess,
and it is the specific guess an attacker who can write one directory gets to make
for you. `--age-keygen <path>` is the same law for `keygen`.

**`--max <n>` is the one default**, `20`, as every listing in this repo; `0`
prints all; a negative ceiling is refused.

**`--only <NAME,...>` narrows what the command receives**; its **default is every
key in the file, said loudly** on the OK line (`only=all`). Both sides of that
default are stated so nobody re-opens it by accident. Against: a probe like `gh
api user` has no business holding `SMTP_PASSWORD`, and every child the command
spawns inherits whatever it was given. For, and it wins: a required list goes
stale, so the day a new value is sealed for a tool just taught to read it, the
value silently does not arrive — the exact failure this tool exists against. Wide
by default, narrow by choice, and `only=` makes a transcript say which runs were
wide. A `--require` naming a key `--only` excludes is a refusal: the caller has
contradicted itself.

**`--require <NAME>` is repeatable and has no default.** It names a key the
caller asserts must be in the file, and a missing one is a refusal *before the
command starts*. It exists because the alternative — a harness starting without
its key and failing forty seconds later inside a provider's error — is the
failure this whole tool is against. Every `--require` that is missing is reported
in one run, sorted, with the `sops <file>` line that adds them.

### `exec`

```
nova-secrets exec --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key \
  --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
```

**What it asserts.** That the command it started had, in its environment, exactly
the keys of `<store>/<as>.yaml` — every one of them, or exactly the `--only`
subset, and no other key added or renamed by this tool — and that the values
arrived byte-for-byte as the file holds them.

**What makes it say NO** (all exit **125**, all *before* the command starts): no
`--` or an empty argv after it; a missing or unreadable store, file or key; a key
file mode that is not `0600`; a `sops` binary that is absent, not executable, or
older than the pinned version; a decrypt that fails; a key name that is not a
legal environment variable; a value containing a NUL or a newline (see
**File-shaped secrets**); a `--require` that is not in the file, or that `--only`
excludes; an `--only` naming a key the file does not hold. One run reports every
independent problem it can reach, in a deterministic order: flags first, sorted;
then the store; then the key; then the binary; then the contents.

**What it deliberately does not check.** What the command does with the values.
Whether the command leaks them — into its own log, its own config file, its own
prompt or its own network call. Whether the values are valid at the provider;
that is a probe the caller runs, and `--require` is about presence, never
validity.

**And the plaintext path, whole, because half of it is the dangerous half.** Every
value reaches **every child of the command** — `git`, `curl`, a skill's script —
and `--only` is the only thing that narrows that. Another process of the same unix
user reads the child's environment: macOS `ps -Eww` (measured on a same-user child
while it ran), linux `/proc/<pid>/environ`; the answer to both is a unix user per
AI, not a flag here. A **core dump** writes that environment to disk, so
nova-secrets sets `RLIMIT_CORE` to 0 before the exec — it survives `execve`, the
child cannot lift it, and a test asserts the limit. And on darwin nova-sandbox is
a **waiting parent** (it spawns `sandbox-exec` and waits), so for the seat's
lifetime a process outside the wall holds the full environment. Nothing of
*nova-secrets* is left alive holding a plaintext; that is not the same sentence as
nothing at all.

**It replaces itself.** `exec` uses `execve` — the process image is replaced by
the command, in the same pid, with no wrapper left in the tree. So: no zombie, no
signal relay to get wrong, no second process for a deadline to kill by mistake,
and nothing of nova-secrets left alive. A test asserts the pid before and after
is the same one.

**Its exit code is the command's, and its own refusal is 125.** Once the command
starts, nova-secrets has no exit code of its own; before it starts, every failure
is **125**, which is nova-sandbox's number for the same problem and is here for
its reason: in the nested launcher a `2` could be `gh`'s, or a flag error, or
this tool's refusal, and three facts sharing one number is three facts nobody can
act on. `exec` therefore **never exits 1 or 2 for a reason of its own**, and **a
caller must never read `exec`'s exit status as a check result** — `check` is the
check. As in the sibling: **read the line, not the number** — the refusal names
what was wrong and where it lives, and the number is only for a script. Its one
event line is printed **before** the exec and **on stderr**:

```
SECRETS EXEC OK as=<name> keys=<n> only=<all|n> required=<n> file=<path> cmd=<argv0>
```

stderr is a named exemption from the OK-goes-to-stdout convention, with its
reason: the command owns stdout from the next instruction onward, and a line of
ours in the middle of `gh api`'s JSON is a bug in whatever parses it. `keys=<n>`
is a count and never a listing, and no value, no value length and no fragment of
a value appears on this line or on any other.

### `names`

```
SECRETS NAME key=GH_TOKEN
SECRETS NAMES OK as=<name> keys=<n> shown=<n> sealed=<n> clear=<n>
SECRETS NAMES MORE kind=key shown=<n> total=<n> run: nova-secrets names ... --max 0
```

**What it asserts.** That these are the key names in the sealed file — **read
without decrypting it**. sops encrypts values and leaves the field names in the
clear (`GH_TOKEN: ENC[AES256_GCM,...]` is what a sealed file holds), so a key is
not needed to answer the question and therefore is not asked for: `names` takes
no `--key` and no `--sops`, starts no sops process, and never has a plaintext in
its memory to lose. A verb that decrypted thirteen values to print thirteen names
would be requiring a key for a parse. `sealed=` and `clear=` decompose `keys=` —
a value that does not begin `ENC[` is in the clear under `unencrypted_regex` —
so that such a key is visible as such on the line that lists it
(`SECRETS NAME key=SPACE_USER clear=true`).

**What makes it say NO.** Everything `exec` refuses about the store and the file.
It is otherwise a **report**: a file with zero keys is `keys=0` at exit 0, and
that is an answer, not a failure. Because it holds no key it can read any name in
the store, and that is not a leak: the ciphertext is not the boundary.

**What it deliberately does not check.** Whether the file *opens* — that is
`check` invariant 4, and `names` succeeding says nothing about a key. Whether a
key is the *right* key, whether its value is current, and whether anything reads
it. It never prints a value, and it never prints a value's **length** — a length
is a value's shape, and the shape of an API key names its provider.

### `check`

The wall. Exit 1 when the store is not what this spec says it is, and its output
names the file and the repair.

**What it asserts**, in a fixed order, all of it against the working copy at
`--store`:

1. **`.sops.yaml` parses**, every rule names a path regex **anchored at both
   ends** (`^…$`) and at least one age recipient, every recipient is a
   syntactically valid `age1…` public key, and no recipient appears twice in one
   rule. The anchors are an invariant, not a style: measured, a rule `a\.yaml$`
   sealed `not-a.yaml` to A's key — the exact mutation invariant 4 exists to
   catch, made by a paste line missing one character.
2. **Every file's recipients are the ones its rule names**: for every `*.yaml`
   but `.sops.yaml`, the `age` set in that file's own `sops:` block equals its
   matching rule's set. `.sops.yaml` governs a **seal**; the file's own block
   governs a **decrypt**; the two meet only when somebody runs `sops updatekeys`,
   and the gap between them is where every drift lives — a key merged into a rule
   that grants nothing, a revoke that still opens every file sealed before it, a
   third recipient no line running `check` would notice.
   `SECRETS CHECK FAIL <file>: recipients differ from .sops.yaml; run: sops updatekeys <file>`
3. **Every `*.yaml` in the store but `.sops.yaml` is sealed**: it carries a `sops:`
   metadata block, and every field outside the rule's `unencrypted_regex` is a
   sops-encrypted value. A plaintext secret in a tracked file is exit 1 naming the
   file and the key, and **never quoting the value**.
4. **This key opens exactly the files whose recipients name its public half** —
   positive and negative in one pass, the negative half above. A file that opens
   and should not, and a file that should open and does not, are two different
   sentences and both are exit 1.
5. **No private key is in the store**: nothing under `--store` is mode `0600`
   age-key-shaped, and `--key` does not resolve to a path inside `--store`.
6. **The key file's mode is `0600` and its directory's is `0700`.**
7. **The working copy is clean of decrypted output**: no untracked file under
   `--store` holds a line matching `^[A-Z][A-Z0-9_]*: ` whose value does not begin
   `ENC[`. The definition is here, not delegated to a `.gitignore` the store does
   not have: a rule depending on a file nobody wrote passes vacuously.

**What makes it say NO.** Any of the seven, each as its own `SECRETS CHECK FAIL`
line, every failure in one run, capped per kind at `--max` with a MORE line, and
the counts never capped:

```
SECRETS CHECK OK  as=<name> recipients=<n> files=<n> sealed=<n> mine=<n> foreign=<n> clear=<n>
SECRETS CHECK FAIL <file>: <reason>
SECRETS CHECK FAIL as=<name> files=<n> failed=<n> shown=<n>
```

The count line prints on failure as well as on success.

**What it deliberately does not check**, said plainly because each of these is a
thing a reader will otherwise assume it did:

- **Git history.** A value that was ever committed in the clear is in the history
  forever, and `check` looks at the working tree and nothing else. History is
  handled by **Rotation**, which is revocation, not deletion.
- **The values.** Whether a token works, whether it is expired, whether it is the
  scope it should be. No network call.
- **The other AIs' keys.** It cannot tell you that Stella's key is `0600` on
  Stella's bench; it can only tell you about the key it was given. Every AI runs
  `check` for itself, and *that* is the invariant — a store proven by one line is
  a store proven for one line.
- **Who has cloned the store**, or who holds a copy of a ciphertext. GitHub knows;
  this tool does not ask.

### `keygen`

```
nova-secrets keygen --as rowan --key ~/.config/nova-secrets/rowan.key --age-keygen /opt/homebrew/bin/age-keygen
SECRETS KEYGEN OK as=rowan key=<path> mode=0600 pub=age1…
SECRETS RULE   creation_rules:
SECRETS RULE     - path_regex: ^rowan\.yaml$
SECRETS RULE       age: age1…,<recovery key>
```

**What it asserts.** That a new age private key exists at `--key`, created with
`O_EXCL` and mode `0600` in a directory that already existed at `0700`, and that
its public half is the one printed.

**What makes it say NO.** A file already at `--key` — refused, never overwritten,
naming the path; a directory that does not exist or is not `0700` — refused,
naming `mkdir -m 700 -p <dir>`; an `age-keygen` that is absent or older than the
pinned version.

**What it deliberately does not do.** It does not touch the store, does not edit
`.sops.yaml`, does not commit, and does not push. The public key reaches the
store the way the store's README says it does: **a pull request that edits only
that AI's own rule**, reviewed by a person. A tool that added its own recipient
line would be a tool that can grant itself access to a file, which is the one
thing the recipient list exists to make impossible without a review. The
`SECRETS RULE` lines are there to be pasted into that pull request; the regex is
printed **anchored at both ends**, because a paste line is where an unanchored
rule comes from; and the `<recovery key>` in them is printed as that literal
placeholder, to be copied from any existing rule in the store's `.sops.yaml` and
never guessed from anything on the bench.

**"Reviewed by a person" is a convention today, not a control.** Measured
2026-09-12: `mas-bandwidth/secrets` has no branch protection on `main`, two admin
collaborators, and every commit so far pushed straight to `main`. The tool
refusing to edit `.sops.yaml` stops *the tool*; the same AI's `git push` is
stopped by nothing. The control is a repository ruleset — PR required, one review
from `gafferongames` on `.sops.yaml` and `*.yaml`, no admin bypass — which is
Glenn's hand, filed as
[security#22](https://github.com/mas-bandwidth/security/issues/22). **Until it
exists, every sentence here about a review describes how we behave, not what is
enforced.**

### Refused, by name, with where it lives

A refusal here is one line, on stderr, and it names the door — exit 2, or 125
from `exec`:

| asked for | the answer |
|---|---|
| `get`, `print`, `show`, `cat` a value | **Refused forever.** There is no verb in this tool that prints a secret value, and there is no flag that makes one. A person who must see a value holds the key and runs `sops -d <file>` with their own hands. |
| `put`, `set`, `add`, `edit` a value | `sops <store>/<name>.yaml` — an editor over the plaintext in memory — or `sops set`. Then `git add`, `git commit`, `git push`. |
| `rotate` | The provider's console (Glenn's hand), then `sops`, then a commit, then a probe. See **Rotation**. |
| `delete` a key, or a file | `sops unset`, or `git rm`, and a rotation of whatever the deleted value was. |
| a file-shaped secret (an SSH key, an age key) handed to a program that wants a path | `sops exec-file --filename <KEY> <store>/<name>-files.yaml '<cmd> {}'`. See **File-shaped secrets**. |
| `recipients`, `grant`, `revoke access` | A pull request against `.sops.yaml` that edits one rule, reviewed and merged by a person. |
| reading or writing the macOS Keychain | Not this tool, on any bench, ever. See **The migration from the Keychain**, and the source tripwire that pins it. |
| a daemon, an agent, a cache, a session | Not this tool. Every call opens the file again; the cost is one `sops` invocation, measured in the work list, and a cached plaintext is a plaintext with a lifetime nobody is watching. |

**The refusal to print a value is checked before any flag, path or file is
read**, in the shape nova-fuse's `lift` established, and its sentence names only
the human path with the key in their own hands. It mentions no mechanical bypass,
because there is not one to mention.

## The credential shape: per AI, per surface

Glenn, 2026-09-12: *"Generally, each AI (including you) should have all their
secrets in nova-secrets. Their own API key, their own email, bsky, discord, ghost
access, github."* So the file is not a provider list. It is **every surface that
AI acts through**, and the key name is the environment variable the tool that
acts already reads.

| surface | key | file | who reads it | where it is today, on the keeper bench |
|---|---|---|---|---|
| Anthropic API | `ANTHROPIC_API_KEY` | `swarm-<name>` | nova-swarm workers on Claude models | **not in the store for Rowan's own seat** — see the exception below |
| xAI | `XAI_API_KEY` | `swarm-<name>` | nova-swarm (Grok), Johnny's line | Glenn's hand, per run |
| Google | `GEMINI_API_KEY` | `swarm-<name>` | the gemini CLI, nova-swarm | Glenn's hand, per run |
| DeepSeek | `DEEPSEEK_API_KEY` | `rowan` | nova-swarm's OpenCode workers, dispatched by the coordinator | a one-line key file, mode 0600 |
| Inception | `INCEPTION_API_KEY` | `swarm-<name>` | nova-swarm | a one-line key file, mode 0600 |
| GitHub | `GH_TOKEN` | `rowan` | `gh`, nova-bus, nova-board, nova-merge, every push | Keychain `rowan-github` / `rowan-gh`, and a `gh` keyring entry under `GH_CONFIG_DIR` |
| space (profiling host) | `SPACE_KEY` **(file-shaped)** | `rowan-files` | the profiling launcher | a private key file; sealed in the store, in the wrong file — see below |
| space, who and where | `SPACE_USER`, `SPACE_HOST` | `rowan` | the profiling launcher | in the clear under `unencrypted_regex` — not secrets |
| email, send | `SMTP_PASSWORD` | `rowan-keeper` | rowan-email producer | Keychain `rowan-smtp` |
| email, send, fallback | `SMTP_PASSWORD_BACKUP` | `rowan-keeper` | rowan-email producer | Keychain `rowan-backup-smtp` |
| email, read | `IMAP_PASSWORD` | `rowan-keeper` | rowan-email consumer | Keychain, the same item as `rowan-smtp` today |
| Bluesky | `BSKY_APP_PASSWORD` | `rowan-keeper` | rowan-bsky | Keychain `rowan-bsky` |
| Discord | `DISCORD_BOT_TOKEN` | `rowan-keeper` | rowan-discord producer and consumer | Keychain `rowan-discord-token` |
| Ghost | `GHOST_ADMIN_KEY` | `rowan-keeper` | rowan-ghost | Keychain `rowan-ghost-pass` |

**Why Rowan's surfaces are in two files** (Glenn, decided 2026-09-12). A line with
one seat has one file. Rowan has two benches and they are not alike: the admin
bench is the **unwalled coordinator**, running many children, and a child that
gets loose there must not be able to send mail, post to Bluesky, speak in Discord
or publish as Rowan. So `rowan.yaml` holds the coordinator's working needs —
`GH_TOKEN`, the swarm's DeepSeek key, and (in `rowan-files.yaml`) the space SSH
key — sealed to **both** of Rowan's bench keys; `rowan-keeper.yaml` holds the
life's surfaces — email, Bluesky, Discord, Ghost — sealed to the **keeper bench's
key alone**. The admin bench cannot open the second file, and that is the point of
splitting them rather than a consequence of it. `--as` names the file; a tool that
sends reads `rowan-keeper` and a tool that pushes reads `rowan`.

**API keys, never OAuth tokens, and never an auth file.** Glenn, 2026-09-12: *"I
would prefer API keys per-AI instead of OAuth tokens."* An OAuth token has a
refresh dance, a device flow, an expiry and a file the harness rewrites behind
your back; a key in a sealed file has none of those and is rotated by one person
at one console. So **no harness auth file** (`~/.claude/.credentials.json`, an
`opencode` auth blob, a `gcloud` application-default file) is held here or handed
over; a harness that can only authenticate that way is one we start by hand.

**The one exception, and it is Glenn's:** *"The exception being that I will run
Claude here manually for you."* **Rowan's own Claude Code seat authenticates
through Glenn's manual login on the admin bench. It is not in the store, there is
no `ANTHROPIC_API_KEY` in `rowan.yaml` for it, and nobody should put one there.**
The `ANTHROPIC_API_KEY` row above is for *workers* — a swarm pool on a Claude
model — and open question 2 is whether those move off the plan seat at all,
because that question is a billing question and not ours.

**Per seat, so per file.** Every AI gets the same table, with the surfaces it
actually has: Stella, Emma, Johnny and Freddy have one seat each and so one file
each, and an AI with no Bluesky simply has no `BSKY_APP_PASSWORD` key. A swarm pool's file
is `swarm-<name>.yaml` and holds exactly one provider key plus a **read-only**
`GH_TOKEN` ([security#1](https://github.com/mas-bandwidth/security/issues/1)),
and nothing else — a worker that holds a line's send credential is a worker that
can post as that line.

## File-shaped secrets

Some programs want a **path**, not a value: `ssh -i`, and age itself. A value like
that is a multi-line private key. The bench has exactly one — the space SSH key.

**They live in a sibling file, `<name>-files.yaml`**, with the same recipients as
`<name>.yaml` and its own rule in `.sops.yaml`. `exec` never opens it. `sops
exec-file` does:

```
sops exec-file --filename SPACE_KEY <store>/<name>-files.yaml 'ssh -i {} $SPACE_USER@$SPACE_HOST …'
```

**The store's README puts it in the wrong file today, and that is a card, not a
disagreement.** It seals a private key "to its line alone (`space_key` in
`<line>.yaml`)". Both halves move: the *file*, because `exec` opens `<line>.yaml`
and asserts every key in it reaches an environment, so the day that key is sealed
there every `exec --as <line>` refuses and the seat does not start; and the
*spelling*, because `space_key` is not a legal environment variable name and is
refused one step earlier. `SPACE_KEY` in `<line>-files.yaml` is what this spec is
written against; moving it is a re-seal of one value and one line of README —
work list item 9.

**`exec` still refuses a multi-line value**, because the split above is a
convention the store keeps and a refusal is what notices when it does not:

```
SECRETS EXEC FAIL key=SPACE_KEY: value is multi-line; a file-shaped secret is not an environment variable.
  run: sops exec-file --filename SPACE_KEY <store>/<name>-files.yaml '<cmd> {}'
```

**Why a sibling file and not a fifth verb.** `sops exec-file` hands the value over
through a **FIFO**, and the honest statement of that lifetime is narrower than
"never on disk": the *bytes* are a pipe, written once and read once, and the node
is unlinked when the command exits — but the node exists, in a temp directory, for
the length of the command, and any process of this unix user that opens it
**first** takes the bytes the intended reader wanted (the same-user boundary
again, unimproved); and a program that re-opens the path, after a fork or on a
retry, gets nothing the second time and fails in a way that reads like a bad key.
That is a *second lifetime model* with a different failure and a different audit
story, and two lifetime models under one verb is how the wrong one gets used on
the day somebody is in a hurry. `exec` means *values in an environment* and will
keep meaning only that. Whether the key should exist at all is open question 5.

## The launcher, and why the order is load-bearing

One call, at the start of a seat, from the line's launcher:

```
nova-secrets exec --store /Users/rowan/secrets --as rowan \
  --key /Users/rowan/.config/nova-secrets/rowan.key \
  --sops /opt/homebrew/bin/sops --require GH_TOKEN -- \
  nova-sandbox --read /opt/homebrew --write /Users/rowan --net-deny -- <harness> <args…>
```

That inner line is **nova-sandbox's own grammar**, at PR #70: its exec verb takes
`--read` and `--write` and derives the policy from them — there is no `run` verb
and no `--profile`. Two of its rules are load-bearing here: the harness's `HOME`
must be inside a `--write` path (its rule 9), and the environment passes through
the wrap untouched, which is what makes the nesting work at all.

Read that outward. `nova-secrets` opens the file, sets the environment, and
**becomes** `nova-sandbox`, which builds the wall and becomes the harness. By the
time a wall exists at all, the plaintext is already in the process's environment
and the store is finished with. Therefore:

- **the store directory is in no read set**, and neither is the key file, and
  neither is `sops`;
- **the wall never reads a key file** — nova-sandbox's own rule, kept here by
  construction rather than by care;
- a sandbox profile that denies `/Users/rowan/.config` **entirely** still runs a
  harness with its keys set, and a test proves exactly that;
- a mistake in the profile can make the harness fail, but it cannot make the
  harness run *without* its keys, and it cannot make the key file reachable from
  inside.

The reverse order — sandbox outside, secrets inside — requires the wall to permit
a read of the key file and the store, which is the read a wall exists to refuse.
**This spec forbids it**, and the first-run page never shows it.

Both tools refuse at **125**, deliberately rather than by collision: the number
says *a wrapper said no before your command ran*, and the **line** says which and
why — `SECRETS EXEC FAIL …` or `SANDBOX REFUSED reason=…`.

### The two callers

1. **A line's launcher.** One `exec` per seat start, as above. The launcher holds
   no value, logs no value, and writes no config containing one.
2. **nova-swarm's dispatcher**, for the pool's provider key. Today nova-swarm reads
   a one-line key file and sets the variable in the **child's** environment only,
   never logging it. That handling is right and does not change. What changes is
   where the file comes from: the dispatcher is started under `nova-secrets exec`,
   and the worker description gains **one field, `key_env`**, beside the
   `key_file` it already has, with exactly one of the two set and neither
   defaulted. A *field*, not a flag: `key_file` is a field of the worker
   description JSON (`internal/swarm/worker.go`, read on `main`), there is no
   `--key-file` flag to be an alternative to, and a key's location has always
   been named by the description that names the provider, the model and the
   variable. The rule *read as data, never sourced* survives unchanged, because an
   environment variable was never sourced either. **That field is a change to
   SPEC-SWARM.md and to nova-swarm and it is not done; it is the second card in
   the work list**, and nothing in this spec should be read as claiming it exists.

## The migration from the Keychain

Every surface in the table above exists **today** on the keeper bench as a macOS
Keychain item, read by a rowan-tool calling `security find-generic-password`
directly. That is why the migration is a section and not a sentence: the move is
not *copy a value*, it is *change a reader*.

**The one-time move, per surface, in this order.** The order is written so that
at no step is there a **live consumer with a dead credential** — every surface
here is read by a launchd job that runs whether or not anybody is watching.

1. **Issue a new value**, Glenn's hands at the provider, **alongside the old one
   where the provider allows two to live at once** (a second app password, a
   second token). Where issuing *revokes* the old — a Ghost admin key is
   regenerated, not added, and some mail hosts do the same — steps 1 to 4 are
   **one sitting**, because from the moment of issue the launchd job reading the
   Keychain is authenticating with a string that is already dead.
2. **Seal it**: `sops <store>/<name>.yaml`, the key under the
   environment-variable name from the table, commit, push. **Glenn's hand when
   the value came from his console** — an AI typing a value it read off his
   screen has put that value in a transcript.
3. **Switch the reader**, which is two edits and not one. The tool learns **one
   environment variable and nothing else**: `os.Getenv("SMTP_PASSWORD")`,
   refusing with one line naming the variable and the `nova-secrets exec`
   invocation when it is empty; the `security` call is **deleted**, not kept as a
   fallback — a fallback to the Keychain is a bench where the migration silently
   did not happen. And the **launchd plist that starts it** is edited to start it
   under `nova-secrets exec …`, because a plist that still launches the bare
   binary launches it with no keys at all (for a LaunchDaemon, that edit is
   Glenn's sudo, per [security#20](https://github.com/mas-bandwidth/security/issues/20)).
4. **Probe**: `nova-secrets exec … -- <that tool> <its own probe>`, and the
   launchd job restarted and watched once.
5. **Delete the old Keychain item**: `security delete-generic-password -s
   <service>`, Glenn's hand, only after step 4 printed green.
6. **Revoke the old value at the provider**, last. Where step 1 already revoked
   it this is a no-op still worth looking at: a provider showing two live
   credentials after a migration is a migration that did not finish.

**`SMTP_PASSWORD` and `IMAP_PASSWORD` are two names for one item today** — one
mailbox password, one Keychain entry — so they migrate in **one sitting**, both
readers switched together. They stay two names because two tools read them, and
the day the host issues them separately the file already has the shape.

**The end is where it goes wrong.** Deleting the Keychain item before the probe
leaves a line with no credential and no way back; leaving the fallback in place
leaves a bench that works for a reason nobody has checked in a month.

**nova-secrets never reads a Keychain, on any bench, for any reason.** A source
tripwire over the package proves no `security` invocation and no Keychain import
exists, and it is a test, not a comment.

## Dependencies, pinned

| binary | pinned minimum | measured on the Studio bench, 2026-09-12 | probe |
|---|---|---|---|
| `sops` | **3.13.3** | `sops 3.13.3` | `<--sops> --version --disable-version-check` |
| `age-keygen` | **1.3.2** | `v1.3.2` | `<--age-keygen> --version` |

`age` itself is not invoked: sops links it. No Go dependency on either project —
`go.mod` gains nothing, and a source tripwire fails if `filippo.io/age` or
`getsops` appears in it. Revision 1 imported `filippo.io/age v1.2.1`; that import
is deleted with the rest of revision 1's store.

**`--disable-version-check` is not optional, and it is the reason the probe is
specified as a whole command line rather than as "run sops --version".** A bare
`sops --version` asks GitHub whether a newer sops exists — a network call, in the
launcher's path, on every seat start, with a timeout nobody chose. The probe must
make **no network call**, and a test asserts it by running the probe with every
egress blocked.

**An absent or too-old binary is a refusal with the install line:**

```
SECRETS EXEC FAIL sops: not executable at /opt/homebrew/bin/sops
  run: brew install sops   (this spec pins >= 3.13.3)
SECRETS EXEC FAIL sops: version 3.9.0 at /opt/homebrew/bin/sops is older than 3.13.3
  run: brew upgrade sops
```

A version string the probe **cannot parse** is a refusal, never a pass: an
unrecognised version is not a version this spec has considered (the
switch-with-no-default law). What is parsed is the **first line of stdout against
`^sops (\d+\.\d+\.\d+)`**, and everything after it is ignored — because sops
prints more than the version when it feels like it, and because
`--disable-version-check` is on a deprecation path: sops 3.13.3 warns that "in a
future version, sops will no longer check whether the current version is the
latest" and that `--check-for-updates` becomes the opt-in. The day the default
flips, the flag may be gone; a probe that parsed the whole line would break with
it, and a probe that anchors on the first three numbers does not.

## Rotation, said plainly

A rotation is three acts in one order, and the tool is only in the third:

1. **Glenn revokes the old value at the provider.** This is what makes the old
   value dead. It is a person's hand, at a console, and nothing on this bench can
   do it or verify it.
2. **The file is re-sealed**: `sops <store>/<name>.yaml`, the new value in, commit,
   push. The AI's next `exec` picks it up, because every call opens the file again
   and nothing caches.
3. **A probe run proves it**: `nova-secrets exec … -- <one command that uses it>`.
   A rotation that was not probed is a rotation that was announced.

**The old value is in git history and will be there forever, and re-sealing does
not remove it.** Anyone who ever cloned the store has that ciphertext, and anyone
who ever held a key for that file can still open that old commit. This is
survivable **only** because of step 1: the value is dead at the provider, so what
they can open is a string that no longer authenticates. It is not survivable for
a value that cannot be revoked, and there must not be one in this store.

**A leaked value is revoked first, before anything else** — before the note,
before the commit, before the incident write-up. And history is never rewritten
to "fix" a leak: a force-push over a leak destroys the record of it while
changing nothing about who already has the bytes.

## Exit codes

The repo's table governs — **0** ran and passed, **1** ran and **FAILED**, **2**
could not run — for `check`, `names` and `keygen`. `exec` uses nova-sandbox's
table instead, for nova-sandbox's reason, and that is the one deviation:

| verb | 0 | 1 | 2 | 125 |
|---|---|---|---|---|
| `check` | every invariant held | an invariant failed, named | could not run | — |
| `names` | it read the file (**including zero keys**) | — never | could not run | — |
| `keygen` | the key exists and its public half is printed | — never | could not run, or the file exists | — |
| `exec` | **0–124 are the command's own exit status, whatever it is** | **the command's** | **the command's** | refused before the command started |

`names` and `keygen` never exit 1: they assert nothing about the store. `exec`'s
status is the command's from the instant of the exec — 2 included, which is why
its own refusal cannot be 2 — so **only `check` is a gate**, and a caller that
gates on `exec` is gating on somebody else's program.
Where a gate reads an exit code, only **0** is permission, and 1 and 2 are treated
alike (do not act) while staying distinct facts with different remedies.

## Output grammar

One line per event, first token `SECRETS`, second the verb, third `OK` or `FAIL`,
`OK` to stdout and `FAIL` to stderr — with `exec`'s single OK line on **stderr**,
the one exemption, for the reason its section gives. Every `key=value` field is
escaped by the shared `internal/oneline` helper so a field is one token; the
free-text tail after `: ` is never scanned for fields. Nothing a file holds and
nothing a caller supplies can author a second line.

```
SECRETS EXEC   OK   as=<name> keys=<n> only=<all|n> required=<n> file=<path> cmd=<argv0>
SECRETS EXEC   FAIL <what>: <why>
SECRETS NAME        key=<NAME> clear=<true|false>
SECRETS NAMES  OK   as=<name> keys=<n> shown=<n> sealed=<n> clear=<n>
SECRETS NAMES  MORE kind=key shown=<n> total=<n> run: <remedy>
SECRETS CHECK  OK   as=<name> recipients=<n> files=<n> sealed=<n> mine=<n> foreign=<n> clear=<n>
SECRETS CHECK  FAIL <file>: <why>
SECRETS CHECK  FAIL as=<name> files=<n> failed=<n> shown=<n>
SECRETS KEYGEN OK   as=<name> key=<path> mode=0600 pub=<age1…>
SECRETS RULE        <one line of .sops.yaml to paste>
```

**No value, no fragment of a value, and no value's length ever appears on any
line, in any refusal, or in any error passed through from sops.** sops' stderr is
*not* passed through raw: it is matched against the shapes this spec knows (no
key, wrong key, not encrypted, malformed) and reported as one of our lines. An
unrecognised one is `sops failed: exit <n>` with the transcript **withheld** and a
line telling the reader to run the same `sops -d` themselves — the one place in
this repo where a transcript is not printed beneath the event line, because a
decrypt error is the one error that can contain plaintext.

**Bounded by design.** `exec` prints exactly one line, always, at any store size.
`names` and `check` cap listings at `--max` (default 20) with one MORE line, per
kind, and their counts are never capped. An unusable invocation costs **one
line** — `nova-secrets <verb>: <what was wrong>; run: nova-secrets help` — with
the banner behind `help`.

**The largest plausible state**, which this store will not exceed in a year: **12
seat files × 16 keys = 192 keys, 6 recipients**. At it: `exec` is 1 line; `names
--max 0` for one seat is 17 lines; `check` is 1 line green. Fully red, the
ceiling is **not** one line per file: seven invariants can each fail on each
file, so the bound is `7 kinds × --max` lines plus one MORE line per kind that
capped plus the one count line — at the default `--max 20`, `7 × 20 + 7 + 1`.
That is the number the cap exists to hold, and it is the number the build
**measures** — lines and bytes for every verb in green and red, stdout and
stderr, with the table in the commit. A bound argued in a spec and never measured
is the bound that is wrong.

## Tests this spec demands

One per rule, named for the rule, each proven able to fail by a mutation before
it is trusted. Every fixture is a **throwaway store built in the test's own
temporary directory with throwaway age keys**: no test, ever, reads the real
store, the real keys, or a real credential.

1. `TestASeatOpensTheFilesItsRulesNameAndNoOther` — **the negative test, and the
   reason the model exists.** Four fixture seats, four keypairs, four files —
   three sealed to one key each, one (`a-keeper.yaml`) sealed to A alone — plus a
   fixture recovery key on every rule. With A's key: `exec` on `a.yaml` and
   `a-keeper.yaml` succeed; `sops -d` of `b.yaml` and `c.yaml` fail; `check` is
   `SECRETS CHECK OK … mine=2 foreign=2`, which is the assertion that a seat with
   two files does not turn its own negative half red. Three mutations, each red on
   its own: re-seal `b.yaml` to A as well (invariant 4 names `b.yaml`); add A to
   `c.yaml`'s **rule** without running `updatekeys` (invariant 2 names `c.yaml`
   and prints the `sops updatekeys` line); remove a recipient from `b.yaml`'s rule
   while the file still opens for it (invariant 2 again, the revoke case). The
   fixture recovery key opens all four and that is asserted, so nobody later
   "fixes" the negative test by making recovery impossible.
2. `TestExecSetsExactlyTheKeysInTheFile` — the child prints its own environment;
   every key in the file is present with the exact bytes, no key is renamed, and
   **no key this tool invented** is present. A mutation that adds a
   `NOVA_SECRETS_*` marker variable turns it red. `SOPS_AGE_KEY_FILE` and every
   other `SOPS_*` variable is absent from the **child's** environment, and a
   `SOPS_AGE_KEY_FILE` planted in the caller's environment pointing at a second
   key does **not** change which key is used. **And the identity file the
   measurement found:** a foreign age identity is planted at
   `$XDG_CONFIG_HOME/sops/age/keys.txt` *and* at
   `$HOME/Library/Application Support/sops/age/keys.txt` in the caller's
   environment, a file that identity is a recipient of is attempted, and it is
   **refused** — red before green, because with the isolation removed this test
   passes the decrypt. The `--only` half: with `--only GH_TOKEN` exactly one key
   reaches the child, `only=1` is on the line, a `--require` for an excluded key
   is refused at 125, and with no `--only` the line says `only=all`.
3. `TestExecReplacesItselfAndPassesTheStatusThrough` — pid before and after the
   exec is the same; a command exiting 7 makes `nova-secrets` exit 7; a command
   exiting 1, and a command exiting **2**, make it exit 1 and 2 with no
   `SECRETS … FAIL` line, because neither is ours; a command exiting 125 is passed
   through and is distinguished from a refusal by the absence of our line; every
   refusal of our own is 125. The child's `RLIMIT_CORE` is 0. A command killed by
   a signal reproduces the shell's status. On a platform without `execve`, the
   test states the platform difference rather than silently not asserting it.
4. `TestNoVerbPrintsAValue` — a source-level tripwire classifying every printed
   argument in the package (the repo's `internal/oneline/audit`), plus a
   behavioral half: a fixture value of a distinctive 40-byte string is placed in
   the file, every verb is run in every mode including every refusal, and the
   string appears in **no** byte of stdout or stderr, of any length, in any
   encoding, in any transcript. A mutation that prints `len(value)` turns it red.
5. `TestGetIsRefusedBeforeAnythingIsRead` — `nova-secrets get …` with no store, no
   key file, no sops binary and a `--store` pointing at a path that would panic if
   opened: still one line, exit 2, naming only `sops -d` in a person's hands, and
   the process stat shows **no file opened**. The refusal sentence itself is
   pinned, not only the code.
6. `TestSopsErrorsAreNeverPassedThroughRaw` — a sops stderr fixture carrying a
   plaintext-looking payload; the payload reaches no stream; the unrecognised case
   prints `sops failed: exit <n>` and the remedy, and never the transcript.
7. `TestTheKeyFileModeIsARefusalOnEveryVerb` — `0644`, `0640`, `0600` in a `0755`
   directory: each a refusal naming `chmod`, on `exec`, `names`, `check`; `0600`
   in `0700` passes. The store-side half: a key file placed **inside** `--store`
   makes `check` red.
8. `TestTheVersionProbeMakesNoNetworkCall` — the probe runs with egress blocked
   and still answers; a fake `sops` printing `sops 3.9.0` is refused naming
   `brew upgrade`; one printing `banana` is refused as unparseable, **not**
   accepted; an absent binary is refused naming `brew install`; a non-executable
   one is a different sentence from an absent one. The fake carries the platform's
   binary suffix.
9. `TestARequireThatIsMissingRefusesBeforeTheCommandStarts` — the command is a
   script that writes a sentinel file; after the refusal the sentinel does not
   exist; every missing `--require` is named in one run, sorted; a `--require`
   that is present and a second that is not report only the second.
10. `TestAMultiLineValueIsRefusedWithTheExecFileLine` — a fixture holding a
    multi-line value; `exec` refuses naming the key and printing the
    `sops exec-file` line against `<name>-files.yaml`; a value with a NUL is
    refused with its own sentence. The `names` half: `names` lists that key with
    **no `--key` and no `--sops` given, no key file on the bench at all and no
    `sops` process started** (asserted by a `--sops` pointing at a path that would
    fail if executed), because names are read from the ciphertext; and `--as
    <name>-files` is refused naming `sops exec-file`.
11. `TestAKeyNameThatIsNotAnEnvVarIsRefused` — `gh-token`, `2FA`, `A B`, the empty
    name: each refused naming the key; `GH_TOKEN` and `A1_B` accepted. All the bad
    ones in one run, sorted.
12. `TestCheckCapsEachKindSeparatelyAndAlwaysPrintsTheCount` — a store with 30
    unsealed files and 1 foreign-openable file: the loud kind does not eat the
    quiet one, each kind caps at `--max` with its own MORE line, the count line
    prints on the red run, `--max 0` prints all, a negative `--max` is refused.
13. `TestCheckFailsClosedOnEverythingItCannotRead` — an unreadable `.sops.yaml`, an
    unreadable AI file, a store that is a file, a store with no `.sops.yaml`: each
    a distinct sentence, and **absent, unreadable and unrecognised are three
    different answers**, pinned as three.
14. `TestKeygenNeverOverwritesAndNeverTouchesTheStore` — an existing file at
    `--key` is refused and its bytes are unchanged; a `0755` parent is refused
    naming `mkdir -m 700`; on success the mode is `0600` and the created file is
    the only filesystem change anywhere — asserted by hashing the whole store tree
    before and after; the private key appears on no stream; the printed public key
    is the one in the file.
15. `TestTheLauncherOrderWorksWithTheStoreFullyDenied` — an end-to-end run of
    `nova-secrets exec … -- nova-sandbox --read <dir> --write <home> --net-deny --
    <probe>`, in nova-sandbox's real grammar at PR #70, with the write set
    carrying the probe's `HOME` (its rule 9) and with neither the store nor the
    key directory in any read set: the probe sees the keys. The reverse nesting is
    asserted to **fail**, so the forbidden order cannot be quietly adopted later.
    The test skips with a stated reason when `nova-sandbox` is not built, rather
    than passing vacuously.
16. `TestNoKeychainAndNoCryptoDependency` — a source tripwire: no `security`
    invocation, no Keychain import, no `filippo.io/age`, no `getsops` in `go.mod`
    or the package's imports; and an aliased import or a helper in a second file
    of the package cannot defeat it (the blind-spot list from nova-bus's tripwire
    applies here unchanged).
17. `TestREADMEFirstRunMatchesWhatTheToolPrints` — the six lines below are run
    against a throwaway store, and the transcript is compared to the README's
    **by prefix and field name, never by value**, so it stays a document.
18. `TestOutputSizeAtTheLargestPlausibleState` — 12 files × 16 keys; lines and
    bytes for every verb in green and red, stdout and stderr; the table goes in the
    commit message.
19. `TestNoFileContentOrCallerArgumentCanForgeALine` — a key name, a file name, a
    `--require` and a sops error carrying `\nSECRETS CHECK OK …`, a terminal
    repaint, and a bidi control: none authors a second line, on any stream,
    including from the flag parser before our first instruction runs.

## The first run: six lines a stranger pastes

On a fresh bench, with `brew install sops age` already done and the store cloned
to `~/secrets`. **The clone is the one thing this tool cannot help you with**: the
store is private and your `GH_TOKEN` is inside it, so somebody else clones it for
you, or the bench holds one credential outside the store — an SSH deploy key — and
that credential is the one thing on the bench `nova-secrets` never carries.
Nothing below is a default: every path is typed, once.

```
mkdir -m 700 -p ~/.config/nova-secrets
nova-secrets keygen --as rowan --key ~/.config/nova-secrets/rowan.key --age-keygen $(brew --prefix)/bin/age-keygen
# paste the printed SECRETS RULE lines into .sops.yaml in a PR touching only your own rule; a person merges it,
#   and then a holder of an existing key runs `sops updatekeys rowan.yaml` — the merge alone grants you nothing
nova-secrets check --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops
nova-secrets names --store ~/secrets --as rowan
nova-secrets exec  --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
```

Six lines: one directory, one keygen, one comment that is the one step a person
does for you, then check, names and a run that prints a name from GitHub. The
third line is a comment on purpose — **a stranger cannot finish this alone, and
the page must say so where the wait happens** rather than leaving them to discover
it in a refusal. It names two acts, not one, because a merged PR changes
`.sops.yaml` and changes **no file**: until somebody runs `sops updatekeys`, the
new key opens nothing and `check` says so by name.

`check` before `names` is also on purpose: the first command that touches the
store should be the one that tells you whether the store is what this spec says,
and if it is red the other two will be red for reasons that are harder to read.

## What it deliberately does not do

1. **It is not a store.** It holds nothing, caches nothing, and has no format of
   its own. Delete this binary and every secret is still there, still sealed, and
   still openable with `sops` by hand — the property that makes it safe to build.
2. **It implements no cryptography** and links none: the moment it does, the thing
   we are trusting is our code again.
3. **No network, ever** — not to the store's remote, not to a provider, not to a
   version check.
4. **No Keychain, no secret service, no agent, no daemon, no cache, no session.**
5. **It never prints a value**, and no flag, verbosity level or debug mode changes
   that.
6. **It does not distribute keys.** A public key reaches the store through a pull
   request; a private key is generated where it will live and never moves.
7. **It does not generate secrets**, and does not manage access: who can clone is
   GitHub's, who can decrypt is age's through the recipient list, and that list is
   edited by review.
8. **It does not audit reads.** No log of who opened what exists, here or in the
   store, and this spec says so twice so nobody plans around one.
9. **It does not rewrite history**, and has no verb that could be mistaken for
   doing so. See **Rotation**.
10. **It does not hold Rowan's Claude Code seat credential** — Glenn's manual
    login, by his ruling, and not a gap to be filled.
11. **It does not carry file-shaped secrets through `exec`** — they are the
    sibling file and `sops exec-file`. See that section.

## The work list

0. **Delete revision 1's library** (`internal/secrets/*`, 668 lines, never merged;
   nova-tools **PR #72**, closed as superseded) — the sealed store, the
   `<name>.age` layout, `Seal`/`Open`/`Keygen`/`Delete`, the `filippo.io/age`
   dependency, and `put`/`get`/`delete`/`recipients`. Carry forward exactly one
   file: `internal/secrets/secret.go`, `Secret` with every `fmt` route closed, for
   the value between the decrypt and the exec. Its *rules* survive above, each now
   carried by a test.
1. **Register the tool in [SPEC.md](../SPEC.md)** — the count ("Ten binaries"), the
   layer sentence, and the section pointing here. Deliberately not done in this
   revision's diff, so the read is of one document.
2. **A `key_env` field in SPEC-SWARM.md** and in `nova-swarm`'s worker
   description, beside `key_file`, exactly one set, neither defaulted. Owed, not
   done.
3. `cmd/nova-secrets`: four verbs, the refusal table, the grammar, the caps.
4. The nineteen tests, each red before green, each mutated.
5. The README section and the six-line first run, held to the tool by test 17.
6. A cold hour by a line that did not write this, from `help` and the README alone,
   before the first read; every stumble fixed in the page **and** the tool.
7. The Keychain migration, one surface at a time, in the six steps of that
   section, each probed before the old item is deleted.
8. `GOOS=windows go test -c` before the first push, even though the store lives on
   macOS benches today — the mode checks are where this will break first, and the
   platform difference gets stated rather than silently skipped.
9. **The store's README and `.sops.yaml`**: move `space_key` out of `<line>.yaml`
   to `SPACE_KEY` in `<line>-files.yaml` (a re-seal of one value, a new rule, one
   line of README); add the `rowan-keeper.yaml` rule with the keeper key alone;
   anchor every `path_regex` at both ends. Until that is done, `check` invariant 1
   is red on the real store, and that is the correct answer.
10. **A ruleset on `secrets`** — Glenn's hand,
    [security#22](https://github.com/mas-bandwidth/security/issues/22). Until it
    exists, "a person merges" is a convention; see below.

## Open questions for Glenn

Each has a **default action** and a **deadline**, per the family rule that every
ask carries both and nothing waits forever. Deadline for all of them: **the next
window in which Glenn is at the bench**; if he is silent past it, the default
action is taken and the decision is recorded as taken-by-default on
[security#20](https://github.com/mas-bandwidth/security/issues/20).

1. **DECIDED 2026-09-12 — one file per seat**, and Rowan's two are split by
   exposure. Written into **The model** and **The credential shape**; this entry
   is the record of the ruling.
2. **Do swarm workers on Claude models move to an `ANTHROPIC_API_KEY`?** Glenn has
   ruled API keys over OAuth, and that he runs Rowan's own seat by hand; the
   workers are what is left, and moving them makes them separately billed, which
   is a money question. *Default if silent: they stay as they are and no
   `ANTHROPIC_API_KEY` enters any file.*
3. **Does the store stay one repository?** A repo per AI would make *read* access
   per-AI too — but a ciphertext is not the boundary, and five repos is five sets
   of settings to get wrong. *Default if silent: one repo.*
4. **What belongs in `unencrypted_regex`?** Today `SPACE_USER|SPACE_HOST`.
   Candidates: `GHOST_SITE`, `GHOST_USER`, the mailbox address, the Bluesky
   handle, the Discord application id — each one less mystery in a broken config,
   and one more thing a stranger with a clone learns about us. *Default if silent:
   leave it.*
5. **Retire the file shape?** The only file-shaped secret is the space SSH key;
   pushing over HTTPS and a Tailscale-reachable space would remove it from the
   bench. *Default if silent: keep it, in `<name>-files.yaml`, reached by
   `sops exec-file`.*
6. **DECIDED 2026-09-12 — the recovery key** exists, is Glenn's, lives off every
   bench, is on every rule, and is used only to re-seal after a lost bench key,
   each use announced. Written into **The model**. One consequence for the build:
   a file sealed before that key reached its rule does not have it until
   `sops updatekeys` runs, and `check` invariant 2 is what notices.
7. **Not a question, kept as the place it is written:** the ruleset that would
   make "a person merges" a control is
   [security#22](https://github.com/mas-bandwidth/security/issues/22), Glenn's
   hand; the spec states the control does not exist until it does.
8. **Per-AI unix users: which AIs get one, and when?**
   [security#20](https://github.com/mas-bandwidth/security/issues/20) answered the
   *how* (a LaunchDaemon with `UserName`, which needs no login session). The order
   is not decided, and until an AI has its own user, "nothing else can read them"
   is true of the ciphertext and not of the running process. *Default if silent:
   Rowan first, the rest named as owed on the board.*

---

*Rowan, 2026-09-12. No credential value was read, written, printed or named
anywhere in this work; the real store was read only through its README, and no
`sops` or `age` command was run against it.*
