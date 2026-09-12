# nova-secrets — specification

**Revision 2, 2026-09-12. This revision replaces a store of our own with a thin
tool over [sops](https://github.com/getsops/sops) and [age](https://age-encryption.org).**
This is the first revision to land on `main` as a file: revision 1 was a job card
and a halted build (nova-tools **PR #72**, closed the same night as superseded),
and it specified a store — our own sealed files, our own `put`/`get`/`delete`,
our own file format. Glenn ruled against it on 2026-09-11 — *"bitwarden ... or
hashicorp ... something generic and open source, not evil"*, and then, on what the
substrate is: *"I like a solution that works in git, like message bus and message
board"* / *"git is our substrate"*. The store is
[`mas-bandwidth/secrets`](https://github.com/mas-bandwidth/secrets) (private),
already built: `.sops.yaml` carrying one recipient rule per file, one sealed yaml
per AI, ciphertexts only, its README carrying the protocol. **That repository is
the store. This tool never becomes one.**

Four verbs at the **credential layer**. The sentence the verb count is measured
against is this one:

> **An AI runs with its own API keys, and nothing else can read them.**

`exec` is the half that makes it *run*; `check` is the half that makes *nothing
else can read them* a fact somebody proved this morning rather than a belief.
`keygen` exists because a new AI or a new bench cannot use either until it has a
keypair, and `names` exists because the first question every caller asks is *what
is in my file* and the only wrong answer to it is a value. Everything else a
person wants to do to a secret is `sops`, `git` or the provider's console, and
this tool refuses it **by name, with where it lives**.

This spec is normative. It is a sibling of [SPEC.md](../SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line guarantee,
the field escape, the cap-and-count law — governs here unchanged except where
`exec`'s own exit table says otherwise, in its own section, in the shape
nova-fuse established. If the code and this document disagree, one of them has a
bug, and the tests decide which.

**Certainty is exactly as strong as who can read the private key file, and
nothing in this tool changes that.** That sentence is inherited verbatim from
revision 1 and from the store's README, and it is the first thing a reader of
this spec should distrust the rest of it against. sops and age give
*cryptographic* certainty that a ciphertext in git yields nothing to a line that
holds no key; they give **nothing at all** about a second process running as the
same unix user as the AI that holds one. That second half is not this tool's to
win. It is won by per-AI unix users and by nova-sandbox's read sets
([security#20](https://github.com/mas-bandwidth/security/issues/20)), and this
spec's job is to not get in their way — see **The launcher, and why the order is
load-bearing**.

## The failures it closes

| the failure, from the record | what closes it |
|---|---|
| Glenn, 2026-09-11: *"obviously, you need access to rowan only secrets"* / *"and others shouldn't have those"* — every line and every worker ran as one unix user, so Rowan's token file was readable by all of them | one age keypair per AI; one sealed file per AI; `check`'s **negative** half, which fails when this AI's key opens another AI's file |
| [security#1](https://github.com/mas-bandwidth/security/issues/1): one org-owner credential for every line, worker and runner; a commit could not say which line made it | a `GH_TOKEN` per AI in that AI's own file, fine-grained; the swarm pool's is its own file and read-only |
| revision 1: a store of our own, with our own cipher-adjacent code to review, in a repo whose reviewers are us | sops + age, both audited by people who are not us, both pinned by version, neither linked into our binary |
| a secret read by `security find-generic-password` on the keeper bench only — invisible to any other bench, unbacked up, and gone with the Keychain | the sealed file is in git: cloned, backed up, reviewable as ciphertext by anyone, openable by exactly one AI and the recovery key |
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

**One sealed yaml per AI, `<name>.yaml`, in the store.** Its `.sops.yaml` rule
lists as recipients: every bench keypair that AI has, plus **the admin recovery
key**. So one file follows an AI to a second bench without being copied or split,
and a lost key is a rotation of that file rather than a loss of it.

**An AI can decrypt its own file and no other.** This is a property of the
recipient lists and of nothing else — no permission bit, no path convention and
no check in this tool creates it, and `check` can only *observe* it. It is
observed from both sides, and the negative side is the one that matters:

> `check` decrypts `<store>/<as>.yaml` with `--key` and **requires** it to
> succeed, then attempts every other `*.yaml` in the store with the same key and
> **requires every one of them to fail**. A foreign file that opens is
> `SECRETS CHECK FAIL` at exit 1 naming the file, because it means a recipient
> rule is wrong and has been wrong since whenever it was written.

**Naming.** The file is per AI, so a key inside it is **the environment variable
its reader already reads**, unprefixed: `GH_TOKEN`, not `ROWAN_GH_TOKEN`. The
AI's name is the filename; putting it in the key as well is the filename twice,
and the first tool to read `ROWAN_GH_TOKEN` while every other tool on earth reads
`GH_TOKEN` is a tool nobody can use. A key name must match `[A-Z][A-Z0-9_]*`;
anything else is a refusal naming the key, because a name that is not a legal
environment variable is a value that would silently not arrive.

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
nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path> [--require <NAME>]... -- <cmd> [args...]
nova-secrets names  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
nova-secrets keygen --as <name> --key <path> --age-keygen <path>
nova-secrets help
```

Four verbs and `help`. `exec` **runs**; `names` and `check` **read**; `keygen`
**writes exactly one file, outside the store**. No verb writes into the store —
the store is edited by `sops` and committed by `git`, both of which are a
person's hands — and no verb reads and writes in one call.

**`--store <dir>` is the store's git working copy**, not a URL and not a
repository name: this tool does no network. A store that is not a directory, or
that holds no `.sops.yaml`, is exit 2 naming both facts and the `git clone` line.

**`--as <name>` selects the file**, `<store>/<name>.yaml`. It is not an identity
and proves nothing: the key decides what opens. A `--as` whose file is absent is
exit 2 and lists the names that *are* in the store, because the commonest form of
this mistake is a spelling.

**`--key <path>` is the age private key**, and it is required on every verb but
`help` — on `keygen` it is the path to write. **No default, no `$SOPS_AGE_KEY_FILE`
fallback, no `~/.config/sops/age/keys.txt`,** and no environment variable of any
kind is consulted by this tool for anything. sops' own environment contract is
honoured in the one direction that matters: nova-secrets **sets**
`SOPS_AGE_KEY_FILE` in the environment of the `sops` process it starts, to the
path `--key` gave, and it removes every other `SOPS_*` variable from that
environment first, so that a variable inherited from a caller can never redirect
which key is used. A key file whose mode is not `0600`, or whose directory is not
`0700`, is a **refusal on every verb** (exit 2) naming `chmod 600` and `chmod
700` — not a warning, because the entire boundary is that file's mode.

**`--sops <path>` is the sops binary**, absolute, from a flag. PATH is a guess,
and it is the specific guess an attacker who can write one directory gets to make
for you. `--age-keygen <path>` is the same law for `keygen`.

**`--max <n>` is the one default**, `20`, as every listing in this repo; `0`
prints all; a negative ceiling is refused.

**`--require <NAME>` is repeatable and has no default.** It names a key the
caller asserts must be in the file, and a missing one is a refusal *before the
command starts*. It exists because the alternative — a harness starting without
its key and failing forty seconds later inside a provider's error — is the
failure this whole tool is against. Every `--require` that is missing is reported
in one run, sorted, with the `sops <file>` line that adds them.

### `exec`

```
nova-secrets exec --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key \
  --sops /opt/homebrew/bin/sops --require GH_TOKEN -- gh api user --jq .login
```

**What it asserts.** That the command it started had, in its environment, exactly
the keys of `<store>/<as>.yaml` — every one of them, and no other key added or
renamed by this tool — and that the values arrived byte-for-byte as the file
holds them.

**What makes it say NO** (all exit 2, all *before* the command starts): no `--`
or an empty argv after it; a missing or unreadable store, file or key; a key file
mode that is not `0600`; a `sops` binary that is absent, not executable, or older
than the pinned version; a decrypt that fails; a key name that is not a legal
environment variable; a value containing a NUL or a newline (see **File-shaped
secrets**); a `--require` that is not in the file. One run reports every
independent problem it can reach, in a deterministic order: flags first, sorted;
then the store; then the key; then the binary; then the contents.

**What it deliberately does not check.** What the command does with the values.
Whether the command leaks them — into its own log, its own config file, its own
prompt or its own network call. Whether the values are valid at the provider;
that is a probe the caller runs, and `--require` is about presence, never
validity. Whether some *other* process running as this unix user can read the
child's environment: on macOS it can, and the answer to that is a unix user per
AI, not a flag here.

**It replaces itself.** `exec` uses `execve` — the process image is replaced by
the command, in the same pid, with no wrapper left in the tree. So: no zombie, no
signal relay to get wrong, no second process for a deadline to kill by mistake,
and nothing of nova-secrets left alive holding a plaintext. A test asserts the
pid before and after is the same one.

**Its exit code is the command's, and that is a deviation stated here.** Once the
command starts, nova-secrets has no exit code of its own; before it starts, every
failure is **2**. `exec` therefore **never exits 1 for a reason of its own**, and
**a caller must never read `exec`'s exit status as a check result** — `check` is
the check. Its one event line is printed **before** the exec and **on stderr**:

```
SECRETS EXEC OK as=<name> keys=<n> required=<n> file=<path> cmd=<argv0>
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

**What it asserts.** That the file opened with this key, and that these are the
keys in it. `sealed=` and `clear=` decompose `keys=` so that a key sitting in the
clear under `unencrypted_regex` is visible as such on the line that lists it
(`SECRETS NAME key=SPACE_USER clear=true`) rather than being indistinguishable
from a sealed one.

**What makes it say NO.** Everything `exec` refuses about the store, the file,
the key and the binary. It is otherwise a **report**: a file with zero keys is
`keys=0` at exit 0, and that is an answer, not a failure.

**What it deliberately does not check.** Whether a key is the *right* key, whether
its value is current, and whether anything reads it. It never prints a value, and
it never prints a value's **length** — a length is a value's shape, and the shape
of an API key names its provider.

### `check`

The wall. Exit 1 when the store is not what this spec says it is, and its output
names the file and the repair.

**What it asserts**, in a fixed order, all of it against the working copy at
`--store`:

1. **`.sops.yaml` parses**, every rule names a path regex and at least one age
   recipient, every recipient is a syntactically valid `age1…` public key, and no
   recipient appears twice in one rule.
2. **Every `*.yaml` in the store but `.sops.yaml` is sealed**: it carries a `sops:`
   metadata block, and every field outside the rule's `unencrypted_regex` is a
   sops-encrypted value. A plaintext secret in a tracked file is exit 1 naming the
   file and the key, and **never quoting the value**.
3. **This AI's own file opens with this key.**
4. **No other AI's file opens with this key** — the negative half, above.
5. **No private key is in the store**: nothing under `--store` is mode `0600`
   age-key-shaped, and `--key` does not resolve to a path inside `--store`.
6. **The key file's mode is `0600` and its directory's is `0700`.**
7. **The store's working copy is clean of decrypted output**: no file matching the
   store's own `.gitignore` decrypt patterns is present, and no untracked file
   under `--store` contains a sops-shaped field name in the clear.

**What makes it say NO.** Any of the seven, each as its own `SECRETS CHECK FAIL`
line, every failure in one run, capped per kind at `--max` with a MORE line, and
the counts never capped:

```
SECRETS CHECK OK  as=<name> recipients=<n> files=<n> sealed=<n> foreign=<n> clear=<n>
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
SECRETS RULE     - path_regex: rowan\.yaml$
SECRETS RULE       age: age1…,<admin recovery key>
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
`SECRETS RULE` lines are there to be pasted into that pull request, and the
`<admin recovery key>` in them is printed as that literal placeholder, never
guessed from anything on the bench.

### Refused, by name, with where it lives

A refusal here is one line, on stderr, exit 2, and it names the door:

| asked for | the answer |
|---|---|
| `get`, `print`, `show`, `cat` a value | **Refused forever.** There is no verb in this tool that prints a secret value, and there is no flag that makes one. A person who must see a value holds the key and runs `sops -d <file>` with their own hands. |
| `put`, `set`, `add`, `edit` a value | `sops <store>/<name>.yaml` — an editor over the plaintext in memory — or `sops set`. Then `git add`, `git commit`, `git push`. |
| `rotate` | The provider's console (Glenn's hand), then `sops`, then a commit, then a probe. See **Rotation**. |
| `delete` a key, or a file | `sops unset`, or `git rm`, and a rotation of whatever the deleted value was. |
| a file-shaped secret (an SSH key, an age key) handed to a program that wants a path | `sops exec-file --filename <name> <file> '<cmd> {}'`. See **File-shaped secrets**. |
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

| surface | key in `<ai>.yaml` | who reads it | where it is today, on the keeper bench |
|---|---|---|---|
| Anthropic API | `ANTHROPIC_API_KEY` | nova-swarm workers on Claude models; any harness in API-key mode | **not in the store for Rowan's own seat** — see the exception below |
| xAI | `XAI_API_KEY` | nova-swarm (Grok), Johnny's line | Glenn's hand, per run |
| Google | `GEMINI_API_KEY` | the gemini CLI, nova-swarm | Glenn's hand, per run |
| DeepSeek | `DEEPSEEK_API_KEY` | nova-swarm's OpenCode workers | a one-line key file, mode 0600 |
| Inception | `INCEPTION_API_KEY` | nova-swarm | a one-line key file, mode 0600 |
| GitHub | `GH_TOKEN` | `gh`, nova-bus, nova-board, nova-merge, every push | Keychain `rowan-github` / `rowan-gh`, and a `gh` keyring entry under `GH_CONFIG_DIR` |
| email, send | `SMTP_PASSWORD` | rowan-email producer | Keychain `rowan-smtp` |
| email, send, fallback | `SMTP_PASSWORD_BACKUP` | rowan-email producer | Keychain `rowan-backup-smtp` |
| email, read | `IMAP_PASSWORD` | rowan-email consumer | Keychain, the same item as `rowan-smtp` today |
| Bluesky | `BSKY_APP_PASSWORD` | rowan-bsky | Keychain `rowan-bsky` |
| Discord | `DISCORD_BOT_TOKEN` | rowan-discord producer and consumer | Keychain `rowan-discord-token` |
| Ghost | `GHOST_ADMIN_KEY` | rowan-ghost | Keychain `rowan-ghost-pass` |
| space (profiling host) | `SPACE_KEY` **(file-shaped)** | the profiling launcher | a private key file; sealed in the store already, per its README |
| space, who and where | `SPACE_USER`, `SPACE_HOST` | the profiling launcher | in the clear under `unencrypted_regex` — not secrets |

**API keys, never OAuth tokens, and never an auth file.** Glenn, 2026-09-12: *"I
would prefer API keys per-AI instead of OAuth tokens"* / *"These should be managed
in nova-secrets."* An OAuth token is a credential with a refresh dance, a device
flow, an expiry and a file the harness rewrites behind your back; a key in a
sealed file has none of those and can be rotated by one person in one console. So:
**no harness auth file (`~/.claude/.credentials.json`, an `opencode` auth blob, a
`gcloud` application-default file) is a thing this store holds or this tool
hands over.** A harness that can only authenticate that way is a harness we start
by hand, and the store says nothing about it.

**The one exception, and it is Glenn's:** *"The exception being that I will run
Claude here manually for you."* **Rowan's own Claude Code seat authenticates
through Glenn's manual login on the admin bench. It is not in the store, there is
no `ANTHROPIC_API_KEY` in `rowan.yaml` for it, and nobody should put one there.**
The `ANTHROPIC_API_KEY` row above is for *workers* — a swarm pool on a Claude
model — and open question 2 is whether those move off the plan seat at all,
because that question is a billing question and not ours.

**Per-AI, so per file.** Every AI gets the same table, with the surfaces it
actually has: Stella, Emma, Johnny, Freddy each have their own `<name>.yaml`, and
an AI with no Bluesky simply has no `BSKY_APP_PASSWORD` key. A swarm pool's file
is `swarm-<name>.yaml` and holds exactly one provider key plus a **read-only**
`GH_TOKEN` ([security#1](https://github.com/mas-bandwidth/security/issues/1)),
and nothing else — a worker that holds a line's send credential is a worker that
can post as that line.

## File-shaped secrets

Some programs want a **path**, not a value: `ssh -i`, and age itself. A value like
that is a multi-line private key, and the store already holds one
(`SPACE_KEY` in `<line>.yaml`, per its README).

**The store covers them. This tool's `exec` does not, in this revision, and says
so.** `exec` refuses a value containing a newline, with the line that works:

```
SECRETS EXEC FAIL key=SPACE_KEY: value is multi-line; a file-shaped secret is not an environment variable.
  run: sops exec-file --filename space_key <store>/<name>.yaml 'ssh -i {} <name>@space …'
```

**Why a refusal and not a fifth verb.** `sops exec-file` hands the value over
through a **FIFO** — a named pipe with one reader, one read, and cleanup when the
command exits — so the key is never a file on disk with bytes in it. That is a
*second lifetime model*, with a different failure (a program that reads the path
twice, or re-opens it after fork, gets nothing the second time) and a different
audit story. Two lifetime models under one verb is how the wrong one gets used on
the day somebody is in a hurry. `exec` means *values in an environment*, and it
will keep meaning only that until a launcher actually needs the other, which is
open question 5 — where the better answer may be to retire the SSH key entirely
and push over HTTPS with `GH_TOKEN`, removing the file shape from the bench
rather than teaching a verb to carry it.

## The launcher, and why the order is load-bearing

One call, at the start of a seat, from the line's launcher:

```
nova-secrets exec --store /Users/rowan/secrets --as rowan \
  --key /Users/rowan/.config/nova-secrets/rowan.key \
  --sops /opt/homebrew/bin/sops --require GH_TOKEN -- \
  nova-sandbox run --profile <path> -- <harness> <args…>
```

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

### The two callers

1. **A line's launcher.** One `exec` per seat start, as above. The launcher holds
   no value, logs no value, and writes no config containing one.
2. **nova-swarm's dispatcher**, for the pool's provider key. Today nova-swarm reads
   a one-line key file (`SPEC-SWARM.md`, *The key, read as data*) and sets the
   variable in the **child's** environment only, never logging it. That handling
   is right and does not change. What changes is where the file comes from: the
   dispatcher is started under `nova-secrets exec --as swarm-<name>`, and
   nova-swarm gains **one flag, `--key-env <VAR>`**, as the alternative to
   `--key-file`, with exactly one of the two required and neither defaulted. The
   rule *read as data, never sourced* survives unchanged, because an environment
   variable was never sourced either. **That flag is a change to SPEC-SWARM.md
   and it is not done; it is the second card in the work list**, and nothing in
   this spec should be read as claiming it exists.

## The migration from the Keychain

Every surface in the table above exists **today** on the keeper bench as a macOS
Keychain item, read by a rowan-tool calling `security find-generic-password`
directly. That is why the migration is a section and not a sentence: the move is
not *copy a value*, it is *change a reader*.

**The one-time move, per surface, in this order:**

1. Glenn, with his own hands, at the provider: issue a **new** value (not a copy of
   the old one — a migration is a rotation, so the Keychain copy is dead the moment
   the move is done).
2. Glenn or the AI, with `sops <store>/<name>.yaml`: add the key under the
   environment-variable name from the table. Commit. Push.
3. The tool that reads it learns **one environment variable and nothing else**:
   `os.Getenv("SMTP_PASSWORD")`, refusing with one line naming the variable and
   the `nova-secrets exec` invocation when it is empty. The `security` call is
   **deleted**, not kept as a fallback — a fallback to the Keychain is a bench
   where the migration silently did not happen.
4. `nova-secrets exec … -- <that tool> <its own probe>` proves the new path.
5. `security delete-generic-password -s <service>` removes the old item, Glenn's
   hand, last, and only after step 4 printed green.

**Order matters and the last two steps are where it goes wrong.** Deleting the
Keychain item before the probe leaves a line with no credential and no way back;
leaving the fallback in place leaves a bench that works for a reason nobody has
checked in a month. Both have happened to us in other shapes.

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
switch-with-no-default law).

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

The repo's table governs: **0** ran and passed, **1** ran and **FAILED**, **2**
could not run. With two deviations, both stated here and in their verb's section:

| verb | 0 | 1 | 2 |
|---|---|---|---|
| `check` | every invariant held | an invariant failed, named | could not run |
| `names` | it read the file (**including zero keys**) | — never | could not run |
| `keygen` | the key exists and its public half is printed | — never | could not run, or the file exists |
| `exec` | **the command's own exit status, whatever it is** | **the command's** | refused before the command started |

`names` and `keygen` never exit 1: they assert nothing about the store. `exec`'s
status is the command's from the instant of the exec, so **only `check` is a
gate**, and a caller that gates on `exec` is gating on somebody else's program.
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
SECRETS EXEC   OK   as=<name> keys=<n> required=<n> file=<path> cmd=<argv0>
SECRETS EXEC   FAIL <what>: <why>
SECRETS NAME        key=<NAME> clear=<true|false>
SECRETS NAMES  OK   as=<name> keys=<n> shown=<n> sealed=<n> clear=<n>
SECRETS NAMES  MORE kind=key shown=<n> total=<n> run: <remedy>
SECRETS CHECK  OK   as=<name> recipients=<n> files=<n> sealed=<n> foreign=<n> clear=<n>
SECRETS CHECK  FAIL <file>: <why>
SECRETS CHECK  FAIL as=<name> files=<n> failed=<n> shown=<n>
SECRETS KEYGEN OK   as=<name> key=<path> mode=0600 pub=<age1…>
SECRETS RULE        <one line of .sops.yaml to paste>
```

**No value, no fragment of a value, and no value's length ever appears on any
line, in any refusal, in any transcript this tool emits, or in any error it
passes through from sops.** sops' own stderr is *not* passed through raw: it is
read, matched against the shapes this spec knows (no key, wrong key, not
encrypted, malformed), and reported as one of our lines. Where it is unrecognised,
it is reported as `sops failed: exit <n>` with the transcript **withheld** and a
line telling the reader to run the same `sops -d` themselves — the one place in
this repo where a transcript is *not* printed beneath the event line, and the
reason is that a decrypt error message is the one error message that can contain
plaintext.

**Bounded by design.** `exec` prints exactly one line, always, at any store size.
`names` and `check` cap listings at `--max` (default 20) with one MORE line, per
kind, and their counts are never capped. An unusable invocation costs **one
line** — `nova-secrets <verb>: <what was wrong>; run: nova-secrets help` — with
the banner behind `help`.

**The largest plausible state**, which this store will not exceed in a year: **12
AI files × 16 keys = 192 keys, 6 recipients**. At it: `exec` is 1 line; `names
--max 0` for one AI is 17 lines; `check` is 1 line green, and at most
`12 + 6 + 1` lines fully red. The build **measures** lines and bytes at that
state, stdout and stderr, and puts the table in the commit — a bound argued in a
spec and never measured is the bound that is wrong.

## Tests this spec demands

One per rule, named for the rule, each proven able to fail by a mutation before
it is trusted. Every fixture is a **throwaway store built in the test's own
temporary directory with throwaway age keys**: no test, ever, reads the real
store, the real keys, or a real credential.

1. `TestAnAIOpensItsOwnFileAndNoOther` — **the negative test, and the reason the
   model exists.** Three fixture AIs, three keypairs, three files sealed to one
   key each plus a fourth recovery key. With A's key: `names` and `exec` on
   `a.yaml` succeed; `sops -d` of `b.yaml` and `c.yaml` fail; `check` is
   `SECRETS CHECK OK … foreign=2`. Then re-seal `b.yaml` to A's key as well — the
   mutation a wrong `.sops.yaml` rule makes — and `check` goes **red** naming
   `b.yaml`, at exit 1. The recovery key opens all three, and that is asserted, so
   nobody later "fixes" the negative test by making recovery impossible.
2. `TestExecSetsExactlyTheKeysInTheFile` — the child prints its own environment;
   every key in the file is present with the exact bytes, no key is renamed, and
   **no key this tool invented** is present. A mutation that adds a
   `NOVA_SECRETS_*` marker variable turns it red. `SOPS_AGE_KEY_FILE` and every
   other `SOPS_*` variable is absent from the **child's** environment, and a
   `SOPS_AGE_KEY_FILE` planted in the caller's environment pointing at a second
   key does **not** change which key is used.
3. `TestExecReplacesItselfAndPassesTheStatusThrough` — pid before and after the
   exec is the same; a command exiting 7 makes `nova-secrets` exit 7; a command
   exiting 1 makes it exit 1 and no `SECRETS … FAIL` line is printed, because
   that 1 is not ours; a command killed by a signal reproduces the shell's
   status. On a platform without `execve`, the test states the platform
   difference rather than silently not asserting it.
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
    `sops exec-file` line; `names` still lists the key (a report does not refuse);
    a value with a NUL is refused with its own sentence.
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
    `nova-secrets exec … -- nova-sandbox … -- <probe>` under a profile that denies
    the store directory and the key directory outright: the probe sees the keys.
    The reverse nesting is asserted to **fail**, so the forbidden order cannot be
    quietly adopted later.
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

On a fresh bench, with the store cloned to `~/secrets` and `brew install sops age`
already done. Nothing here is a default: every path is typed, once.

```
mkdir -m 700 -p ~/.config/nova-secrets
nova-secrets keygen --as rowan --key ~/.config/nova-secrets/rowan.key --age-keygen $(brew --prefix)/bin/age-keygen
# paste the printed SECRETS RULE lines into .sops.yaml in a PR touching only your own rule; a person merges it and seals your file
nova-secrets check --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops
nova-secrets names --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops
nova-secrets exec  --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops --require GH_TOKEN -- gh api user --jq .login
```

Six lines: one directory, one keygen, one comment that is the one step a person
does for you, then check, names and a run that prints a name from GitHub. The
third line is a comment on purpose — **a stranger cannot finish this alone, and
the page must say so where the wait happens** rather than leaving them to discover
it in a refusal.

`check` before `names` is also on purpose: the first command that touches the
store should be the one that tells you whether the store is what this spec says,
and if it is red the other two will be red for reasons that are harder to read.

## What it deliberately does not do

1. **It is not a store.** It holds nothing, caches nothing, and has no format of
   its own. Delete this binary and every secret is still there, still sealed, and
   still openable with `sops` by hand — and that is the property that makes it
   safe to build.
2. **It implements no cryptography** and links none. It cannot, because the
   moment it does, the thing we are trusting is our code again.
3. **No network, ever** — not to the store's remote, not to a provider, not to a
   version check. `git` moves the store; a person or a launcher runs it.
4. **No Keychain, no secret service, no agent, no daemon, no cache, no session.**
5. **It never prints a value**, and there is no flag, no verbosity level and no
   debug mode that changes that.
6. **It does not distribute keys.** A public key gets into the store through a
   pull request a person merges; a private key is generated where it will live and
   never moves.
7. **It does not generate secrets.** A provider issues an API key; a mail host
   issues an app password. A tool that generated a credential would be a tool
   whose output nobody else recognises.
8. **It does not manage access.** Who can clone the store is GitHub's; who can
   decrypt is age's, through the recipient list; and the recipient list is edited
   by review.
9. **It does not audit reads.** There is no log of who opened what, here or in the
   store, and this spec says so twice so that nobody plans around one.
10. **It does not rewrite history**, and has no verb that could be mistaken for
    doing so. See **Rotation**.
11. **It does not hold Rowan's Claude Code seat credential** — Glenn's manual
    login, by his ruling, and not a gap to be filled.
12. **It does not carry file-shaped secrets in this revision** — see that section,
    and open question 5.

## What revision 1 had, and what happens to it

Revision 1 specified a store of our own and got as far as a library
(nova-tools **PR #72**, closed 2026-09-12 as superseded; `internal/secrets/*`,
668 lines, never merged into a command). **Cut entirely:** the sealed store, the
`<name>.age` file-per-secret layout, `Seal`/`Open`/`Keygen`/`Delete`/
`RecipientCount`, the `filippo.io/age` dependency, and the verbs `put`, `get`,
`delete` and `recipients`.

**Kept, because they were right about secrets and not about storage** — each one
is a rule above, and each is now enforced by a test in this spec's list:

- certainty is exactly as strong as who can read the private key file, **in those
  words** (the opening);
- the store holds only ciphertext and may be readable by anyone (**The model**);
- the private key path comes from a flag and never from a default or an
  environment variable (**The verbs**, test 2);
- a value is never in argv, a log, an error or a temp file (**Output grammar**,
  test 4);
- sealing needs only public keys, so a person can seal *for* a line without
  holding what that line holds (**The model**);
- a recovery recipient is just another recipient (**The model**, test 1);
- `Secret` as a type with every `fmt` route closed — **this one is worth porting
  even here**, for the value between the decrypt and the exec, and the build
  should carry `internal/secrets/secret.go` forward for that reason alone.

## The work list

1. **Register the tool in [SPEC.md](../SPEC.md)** — the count ("Ten binaries"), the
   layer sentence, and the section pointing here. Deliberately not done in this
   revision's diff, so the read is of one document.
2. **`--key-env <VAR>` in SPEC-SWARM.md** and in `nova-swarm`, as the alternative
   to `--key-file`, exactly one required, neither defaulted. Owed, not done.
3. `cmd/nova-secrets`: four verbs, the refusal table, the grammar, the caps.
4. The nineteen tests, each red before green, each mutated.
5. The README section and the six-line first run, held to the tool by test 17.
6. A cold hour by a line that did not write this, from `help` and the README alone,
   before the first read; every stumble fixed in the page **and** the tool.
7. The Keychain migration, one surface at a time, in the five steps of that
   section, each probed before the old item is deleted.
8. `GOOS=windows go test -c` before the first push, even though the store lives on
   macOS benches today — the mode checks are where this will break first, and the
   platform difference gets stated rather than silently skipped.

## Open questions for Glenn

Each has a **default action** and a **deadline**, per the family rule that every
ask carries both and nothing waits forever. Deadline for all of them: **the next
window in which Glenn is at the bench**; if he is silent past it, the default
action is taken and the decision is recorded as taken-by-default on
[security#20](https://github.com/mas-bandwidth/security/issues/20).

1. **The admin bench versus the keeper bench: which keys live where.** The admin
   bench (`studio.local`, user `glenn`) is where Glenn runs Claude for Rowan by
   hand; the keeper bench is user `rowan`. Proposal: **one file per AI, with both
   benches' public keys as recipients**, so the same `rowan.yaml` opens on either
   with a different private key — simple, and it means a second bench is a keygen
   and a one-line PR. The cost is stated: a compromise of either bench's key
   yields every value in that file. The alternative is a narrower
   `rowan-admin.yaml` holding only what the admin bench needs.
   *Default if silent: one file, both keys.*
2. **Do Claude Code children move from the plan seat to an `ANTHROPIC_API_KEY`?**
   Glenn has ruled that API keys per-AI are preferred over OAuth, **and** that he
   runs Rowan's own Claude seat manually. Those two together leave exactly one
   thing undecided: the **swarm workers** on Claude models. Moving them to an API
   key makes them metered and separately billed, which is a money question and
   his alone. *Default if silent: workers stay as they are and no
   `ANTHROPIC_API_KEY` enters any file.*
3. **Does the store stay one repository?** One private repo with one file per AI
   is the shape today. The alternative is a repo per AI, which would make *read*
   access per-AI as well — but read access to a ciphertext is not the boundary, and
   five repos is five sets of settings to get wrong. *Default if silent: one repo.*
4. **What belongs in `unencrypted_regex`?** Today `SPACE_USER|SPACE_HOST`.
   Candidates that are facts rather than secrets and that a reader benefits from
   seeing in a diff: `GHOST_SITE`, `GHOST_USER`, the mailbox address, the Bluesky
   handle, the Discord application id. Each one in the clear is one less mystery
   in a broken config — and one more thing a stranger with a clone learns about
   us. *Default if silent: leave it as it is.*
5. **File-shaped secrets: `exec-file`, or retire the file shape?** The only one on
   the bench is the space SSH key. Teaching `exec` a FIFO is real work and a
   second lifetime model; pushing over HTTPS with `GH_TOKEN` and using a
   Tailscale-reachable space would remove the shape from the bench entirely.
   *Default if silent: neither — `exec` keeps refusing it, and `sops exec-file`
   stays the documented path.*
6. **Is the admin recovery key on every AI's rule, and where does it live?** This
   spec assumes yes, because a lost key is otherwise a rotation of every value
   that AI has. Where that key's private half lives — which machine, what backup —
   is Glenn's answer and is not written on this bench. *Default if silent: yes on
   every rule; its location unrecorded here, deliberately.*
7. **Per-AI unix users: which AIs get one, and when?**
   [security#20](https://github.com/mas-bandwidth/security/issues/20) answered the
   *how* (a LaunchDaemon with `UserName`, which needs no login session — Glenn's
   reboot constraint is on login sessions, and age needs no Keychain). The order
   is not decided, and until an AI has its own user, "nothing else can read them"
   is true of the ciphertext and not of the running process. *Default if silent:
   Rowan first, as the one already half-moved, and the rest named as owed on the
   board.*

---

*Rowan, 2026-09-12. No credential value was read, written, printed or named
anywhere in this work; the real store was read only through its README, and no
`sops` or `age` command was run against it.*
