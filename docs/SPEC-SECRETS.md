# nova-secrets — specification

**Revision 4, 2026-09-12.** Read 3 measured the store's collaborator permissions and held the
spec on what they mean; the ruleset paragraph, invariant 1, invariant 8's wording, the keeper's
token scope and the first-run page are corrected here, and the restatement is cut. Each read's
findings and each revision's decisions are in this pull request's body, not in this document.

The tool is a thin wrapper over [sops](https://github.com/getsops/sops) and
[age](https://age-encryption.org). Revision 1 specified a store of our own; Glenn ruled
against it — *"something generic and open source, not evil"*, *"git is our substrate"*.
The store is [`mas-bandwidth/secrets`](https://github.com/mas-bandwidth/secrets)
(private), already built: `.sops.yaml` carrying one recipient rule per file, sealed yaml
files only, its README carrying the protocol. **That repository is the store. This tool
never becomes one.**

Four verbs at the **credential layer**, measured against one sentence:

> **An AI runs with its own API keys, and nothing else can read them.**

`exec` makes it *run*; `check` makes *nothing else can read them* a fact somebody proved this
morning rather than a belief; `keygen` exists because a new bench cannot use either until it
has a keypair; `names` answers *what is in my file* **with no key at all**. Everything else a
person wants to do to a secret is `sops`, `git` or the provider's console, and this tool
refuses it **by name, with where it lives**. In one paragraph: it reads one sealed yaml out
of a git working copy by running `sops` at a path the caller named, keeps the plaintext in its
own memory for the length of one call, and then execs, prints, proves or writes one key. It
links no cryptography, opens no network socket, starts no shell, writes no state of its own,
and reads no Keychain — about two hundred lines of Go over two binaries it did not write, and
the day a better generic store exists it should be two hundred lines of Go over that one.

This spec is normative, and a sibling of [SPEC.md](../SPEC.md), whose **Conventions** govern
unchanged except for `exec`'s exit table — the one deviation, argued in **Exit codes**.

**Certainty is exactly as strong as who can read the private key file, and nothing in
this tool changes that.** sops and age give *cryptographic* certainty that a ciphertext
in git yields nothing to a line holding no key; they give **nothing** about a second
process running as the same unix user as the AI that holds one. That half is won by
per-AI unix users and nova-sandbox's read sets
([security#20](https://github.com/mas-bandwidth/security/issues/20)).

## The model

**One age keypair per AI per bench.** The private half is a file in that AI's own home,
mode `0600`, in a directory mode `0700`, at a path that comes from a flag (`--key`) and
nowhere else. The public half is not a secret and is printed, pasted and committed.

**One sealed yaml per seat, `<name>.yaml`, and one bench key per file.** A line with one
seat has one file, sealed to that bench's key plus Glenn's recovery key. A line with two
benches has two files, and neither is sealed to the other bench's key — **The credential
shape** carries Glenn's ruling on which token lives in which, `--as` names the file, and
**invariant 1 holds the shape**, so the ruling is checked and not only written down.

**A file-shaped secret is not in this store.** A value with a newline in it — an SSH
private key, an age key — is **generated on the seat that uses it and never leaves that
seat**, exactly as this spec's own age keys already are; its public half is authorized on
the box that accepts it by that box's own recipe (nova-run,
[ideas#766](https://github.com/mas-bandwidth/ideas/issues/766)). The store holds no such
value in v1: no sibling file, no second lifetime model, no verb that hands a program a path,
and `exec` refuses a multi-line value **by name** with that remedy. The one on the bench today
— the space SSH key, sealed as `space_key` in `<line>.yaml` — is a line of the store's README
to remove: a work item, not a design question.

**The recovery key** is Glenn's, generated at his console, kept **off every bench** (his
password manager or paper), and a recipient on every rule. What it opens are API keys and
service passwords Glenn pays for and can revoke at any console, never a line's record, notes
or memory. It is for one thing — re-sealing a file when a bench key is lost (`sops
updatekeys`) — and **every use is announced on the record**.

**A seat can decrypt the files its rules name, and no other.** A property of the recipient
lists and of nothing else — no permission bit, no path convention and no check in this tool
creates it, and `check` only *observes* it, from both sides, the negative one mattering:

> `check` finds this key's public half, then for **every** `*.yaml` in the store requires
> a decrypt to **succeed** if that file's own recipients list the public half and to
> **fail** if they do not. A file that opens and should not is `SECRETS CHECK FAIL` at
> exit 1 naming the file; so is a file that should open and does not.

**Naming.** A key inside the file is **the environment variable its reader already reads**,
unprefixed: `GH_TOKEN`, not `ROWAN_GH_TOKEN` — the AI's name is the filename, and the first
tool to read `ROWAN_GH_TOKEN` while every other tool on earth reads `GH_TOKEN` is a tool
nobody can use. A key name must match `[A-Z][A-Z0-9_]*`; anything else is a refusal naming the
key, because a name that is not a legal environment variable would silently not arrive.

**Not everything in the file is sealed.** `.sops.yaml` carries an `unencrypted_regex` for
fields that are facts rather than secrets — today `^(SPACE_USER|SPACE_HOST)$` — and this
tool treats an unencrypted field exactly as a sealed one. What may be in the clear is the
store's decision, reviewed in a pull request; this tool neither extends nor audits it.

**What the model does not give you.** Read access to the *ciphertext* is not a boundary:
anybody who can clone the store holds every AI's sealed file, and that is intended — it is what
makes the store backed up, reviewable and portable. The boundary is the private key file. And a
*decrypt leaves no record anywhere*: neither the store, nor git, nor this tool can say who
opened what, or when, so nobody may build a belief on an audit trail that does not exist.

## The verbs

```
nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path> --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
nova-secrets names  --store <dir> --as <name> [--max <n>]
nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
nova-secrets keygen --as <name> --key <path> --age-keygen <path>
nova-secrets help
```

No verb writes into the store — the store is edited by `sops` and committed by `git`, both a
person's hands; `keygen` writes exactly one file, outside it; and no verb reads and writes in
one call.

**`--store <dir>` is the store's git working copy**, not a URL and not a repository name:
this tool does no network. A working copy in the strong sense — invariant 8 reads `.git` as
files — so a `--store` that is not a directory, holds no `.sops.yaml`, or has no `.git`, is a
refusal naming the fact and the `git clone` line: **exit 2**, or **125** from `exec`, as every
refusal of `exec`'s is.

**`--as <name>` selects the file**, `<store>/<name>.yaml`. It is not an identity and proves
nothing: the key decides what opens. A `--as` whose file is absent is a refusal listing the
names that *are* in the store, the commonest form of this mistake being a spelling.

**`--key <path>` is the age private key**, required on every verb but `help` and `names`; on
`keygen` it is the path to write. **No default, no `$SOPS_AGE_KEY_FILE` fallback, no
`~/.config/sops/age/keys.txt`,** and no environment variable of any kind is consulted by this
tool for anything. A key file whose mode is not `0600`, or whose directory is not `0700`, is a
**refusal on every verb that takes one** naming `chmod 600` and `chmod 700` — not a warning,
because the entire boundary is that file's mode.

**The sops child gets a built environment, not an edited one.** nova-secrets **sets**
`SOPS_AGE_KEY_FILE` to the path `--key` gave, removes every other `SOPS_*` variable, and
sets `HOME` and `XDG_CONFIG_HOME` — **for the sops child only** — to an empty directory
under its own temp dir, covering sops' whole documented identity lookup and not a subset;
test 2 names every file and variable in that lookup, plants one of each, and states what
each defeats.

**`--sops <path>` is the sops binary**, absolute, from a flag: PATH is a guess, and it is
the specific guess an attacker who can write one directory gets to make for you.
`--age-keygen <path>` is the same law. **`--max <n>` is the one default**, `20`; `0` prints
all; a negative ceiling is refused.

**`--only <NAME,...>` narrows what the command receives, and it has no default.** A list,
or the literal `all`; a missing `--only` is a refusal naming both, because a launcher knows
which secrets its harness needs and SPEC.md says the same of every scope: no default. Wide
must be typed — `--only all` prints `only=all` — because **every child the command spawns
inherits whatever it was given**.

**`--require <NAME>` is repeatable and has no default.** A key the caller asserts must be in
the file; a missing one is a refusal *before the command starts*, because a harness starting
without its key and failing forty seconds later inside a provider's error is the failure this
tool is against. Every missing one is reported in one run, sorted, with the `sops <file>` line
that adds them; a `--require` naming a key `--only` excludes is a refusal, the caller having
contradicted itself.

### `exec`

```
nova-secrets exec --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key \
  --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
```

**What it asserts.** That the command it started had, in its environment, exactly the keys
of `<store>/<as>.yaml` that `--only` named (`all` names every one), no other key added or
renamed by this tool, and the values byte-for-byte as the file holds them. Not what the
command *does* with them: whether it leaks them into its own log or config, and whether
they are valid at the provider, are the caller's probe.

**What makes it say NO** (all exit **125**, all *before* the command starts): no `--` or an
empty argv after it; a missing or unreadable store, file or key; a store whose `HEAD` differs
from the ref it tracks; a key file mode that is not `0600`; a `sops` binary absent, not
executable, or too old; a decrypt that fails; a key name that is not a legal environment
variable; a value with a NUL or a newline in it; a missing `--only`; a `--require` not in the
file or excluded by `--only`; an `--only` naming a key the file does not hold. One run reports
every independent problem it can reach, in a deterministic order: flags first, sorted; then the
store; the key; the binary; the contents.

**The plaintext path, whole, because half of it is the dangerous half.** Every value reaches
**every child of the command**, and `--only` is the only thing that narrows that. Another
process of the same unix user reads the child's environment (macOS `ps -Eww`, linux
`/proc/<pid>/environ`), and the answer is a unix user per AI, not a flag here; a **core dump**
writes it to disk, so `RLIMIT_CORE` is set to 0 before the exec; and on darwin nova-sandbox is
a **waiting parent**, so for the seat's lifetime a process outside the wall holds it. Nothing
of *nova-secrets* is left alive holding a plaintext; that is not nothing at all.

**It replaces itself.** `exec` uses `execve` — same pid, no wrapper left in the tree: no
zombie, no signal relay to get wrong, no second process for a deadline to kill by mistake.

**Its exit code is the command's, and its own refusal is 125**, nova-sandbox's number adopted
verbatim: in the nested launcher a `2` could be `gh`'s, a flag error, or this tool's refusal,
and three facts sharing one number is three facts nobody can act on. So `exec` **never exits 1
or 2 for a reason of its own**, **a caller must never read its exit status as a check result**,
and: **read the line, not the number**. Its one event line is printed **before** the exec and
**on stderr**, the command owning stdout from the next instruction onward:

```
SECRETS EXEC OK as=<name> keys=<n> only=<all|n> required=<n> file=<path> head=<sha> cmd=<argv0>
```

`keys=<n>` is a count and never a listing.

### `names`

```
SECRETS NAME key=GH_TOKEN
SECRETS NAMES OK as=<name> keys=<n> shown=<n> sealed=<n> clear=<n>
SECRETS NAMES MORE kind=key shown=<n> total=<n> run: nova-secrets names ... --max 0
```

**What it asserts.** That these are the key names in the sealed file — **read without
decrypting it**. sops encrypts values and leaves field names in the clear (`GH_TOKEN:
ENC[AES256_GCM,...]`), so a key is not needed for the question and is not asked for: `names`
takes no `--key` and no `--sops`, starts no sops process, and never has a plaintext in its
memory to lose. `sealed=` and `clear=` decompose `keys=`, so a key in the clear under
`unencrypted_regex` is visible as such (`SECRETS NAME key=SPACE_USER clear=true`). Zero keys is
`keys=0` at exit 0, an answer and not a failure. It says nothing about whether the file *opens*
(invariant 4).

### `check`

The wall. Exit 1 when the store is not what this spec says it is, its output naming the
file and the repair. In a fixed order, against the working copy at `--store`:

1. **`.sops.yaml` parses**, every rule names a path regex **anchored at both ends** (`^…$`)
   and at least one age recipient, every recipient is a syntactically valid `age1…` key,
   and no recipient appears twice in one rule. The anchors are an invariant, not a style:
   measured, a rule `a\.yaml$` sealed `not-a.yaml` to A's key. **And the shape of the rule set
   is checked**, because *one bench key per file* and Glenn's split are otherwise model rules
   nothing enforces: **every rule names exactly two recipients, and exactly one recipient — the
   recovery key — is common to every rule.** Two files for one seat key stays legal; two seat
   keys on one file does not, nor does a file whose recovery recipient is not everyone else's.
   Read from `.sops.yaml` alone, with no key at all, so every bench goes red on a merged rule
   that widens one file or swaps the recovery key out of it.
2. **Every file's recipients are the ones its rule names**: for every `*.yaml` but
   `.sops.yaml`, the `age` set in that file's own `sops:` block equals its matching rule's set.
   `.sops.yaml` governs a **seal**, the file's own block governs a **decrypt**, the two meet
   only when somebody runs `sops updatekeys`, and the gap is where every drift lives — a key
   merged into a rule that grants nothing, a revoke that still opens every file sealed before
   it, a third recipient nobody notices.
   `SECRETS CHECK FAIL <file>: recipients differ from .sops.yaml; run: sops updatekeys <file>`
3. **Every `*.yaml` but `.sops.yaml` is sealed**: it carries a `sops:` block and every
   field outside the rule's `unencrypted_regex` is encrypted. A plaintext secret in a
   tracked file is exit 1 naming the file and the key, **never quoting the value**.
4. **This key opens exactly the files whose recipients name its public half** — positive
   and negative in one pass, the negative half above. A file that opens and should not, and
   a file that should open and does not, are two sentences and both are exit 1.
5. **No private key is in the store**: nothing under `--store` is mode `0600`
   age-key-shaped, and `--key` does not resolve to a path inside `--store`.
6. **The key file's mode is `0600` and its directory's is `0700`.**
7. **The working copy is clean of decrypted output**: no untracked file under `--store` holds
   a line matching `^[A-Z][A-Z0-9_]*: ` whose value does not begin `ENC[` — defined here and
   not delegated to a `.gitignore` the store does not have, which would pass vacuously.
8. **The working copy is the store, not a memory of it**: `HEAD` equals the remote-tracking
   ref it tracks, read as **files** — `.git/HEAD`, the upstream named by `branch.<name>.remote`
   and `.merge` in `.git/config`, then `.git/refs/…` and `.git/packed-refs` — no `git` binary,
   no network. Behind it is a bench running a value a rotation replaced; ahead of it is a local
   edit nobody reviewed; both are exit 1 naming `git -C <store> pull --ff-only`, and `exec`
   refuses the same case at 125, the value it is about to hand a process being exactly the one
   in question. Three states are refusals naming the fact, never a stale-ref failure and never
   the "no `.git`" sentence: a **detached `HEAD`**, a **branch with no upstream**, and a `.git`
   that is a **file** (a worktree or a submodule), whose real directory this tool does not
   follow. Both OK lines carry `head=<short sha>`, so two benches compare by eye. **What it
   notices is a fetch nobody merged, never a fetch nobody ran**: a remote-tracking ref is only
   as fresh as the last fetch, so a clone nobody fetches satisfies invariant 8 forever, which
   is why every launcher line carries `git -C <store> pull --ff-only &&` — the one network call
   on this page, the launcher's and never the tool's. The cost is that a bench which cannot
   reach GitHub cannot clear the refusal at seat start; accepted, the alternative being a seat
   running a revoked value with a green `check` beside it, the row this tool exists to close.

Any of the eight is its own `SECRETS CHECK FAIL` line, every failure in one run, capped per
kind at `--max` with a MORE line, the counts never capped and printed on failure as well as
success. `mine=` counts the files whose recipients list **this key**: the same store is `mine=1
foreign=4` on both of Rowan's benches, green on both.

```
SECRETS CHECK OK  as=<name> recipients=<n> files=<n> sealed=<n> mine=<n> foreign=<n> clear=<n> head=<sha>
SECRETS CHECK FAIL <file>: <reason>
SECRETS CHECK FAIL as=<name> files=<n> failed=<n> shown=<n>
```

It does **not** check git history (a value ever committed in the clear is there forever;
that is **Rotation**, which is revocation and not deletion), the values themselves (no
network call), the other AIs' keys (every AI runs `check` for itself, because a store
proven by one line is a store proven for one line), or who has cloned the store.

### `keygen`

```
nova-secrets keygen --as rowan --key ~/.config/nova-secrets/rowan.key --age-keygen /opt/homebrew/bin/age-keygen
SECRETS KEYGEN OK as=rowan key=<path> mode=0600 pub=age1…
SECRETS RULE   creation_rules:
SECRETS RULE     - path_regex: ^rowan\.yaml$
SECRETS RULE       age: age1…,<recovery key>
```

**What it asserts.** That a new age private key exists at `--key`, created with `O_EXCL` and
mode `0600` in a directory that already existed at `0700`, and that its public half is the one
printed. It refuses a file already at `--key`, never overwritten, naming the path; a directory
absent or not `0700`, naming `mkdir -m 700 -p <dir>`; an `age-keygen` absent or too old.

**What it deliberately does not do.** Touch the store, edit `.sops.yaml`, commit, or push: a
tool that added its own recipient line could grant itself access to a file, the one thing the
recipient list exists to make impossible without a review. The `SECRETS RULE` lines are for
pasting into **a pull request that edits only that AI's own rule**, and the rule printed is the
shape invariant 1 demands — anchored at both ends, this key and the recovery key and nothing
else, `<recovery key>` a literal placeholder copied from an existing rule, never guessed.

**"Reviewed" is a control, and today the controlled account can switch it off.** Measured
2026-09-12: `mas-bandwidth/secrets` carries ruleset **22995725** (`secrets-review-required`),
active on the default branch — pull request required, one approving review, **last-push
approval required**, stale reviews dismissed on push, no deletion, no non-fast-forward,
**`bypass_actors` empty**. It has exactly two collaborators, `gafferongames` (Glenn) and
`rowan-claude` (Rowan), and **both measure `admin`**. An empty `bypass_actors` stops an admin
*bypassing* the rule; it does not stop an admin *editing* it — either account can disable or
delete the ruleset with no pull request, and one of those tokens is the `GH_TOKEN` in
`rowan.yaml`, on the unwalled coordinator bench whose loose child is the threat this split
exists for. So *every change is a pull request approved by the other account* holds while no
admin touches the ruleset, and the party it should bind can untie it. What that buys an
attacker with the cryptography intact: a direct push editing `rowan.yaml`'s own rule — that
bench holds a current recipient key, so `updatekeys` runs — swapping Glenn's recovery key out
of that one file, invariants 2 and 4 green afterwards. **Invariant 1's two-recipient clause is
what turns that red on every other bench**, not the ruleset. And which rule a pull request
touches is read by the approver, not enforced: no code owners, no required reviewers.

**The ruling, and it is Glenn's hand.** *Default if he is silent: `rowan-claude` is demoted to
`write` on `mas-bandwidth/secrets`*, one line on
[security#22](https://github.com/mas-bandwidth/security/issues/22) beside the ruleset paste it
already carries. Write opens branches, pushes them, opens pull requests, approves and merges —
every step this spec asks of that account — and cannot edit or delete a ruleset, so the control
stops being one the controlled account administers. What remains is the milder consequence:
with two collaborators and no bypass, the approver of every one of Glenn's changes, **including
one that grants a key access to a file**, is an AI — the ruleset's design, not an accident, and
a third collaborator is the repair on the day there is one to add (*default: accepted*). If
Glenn keeps `admin` on both accounts, the review is a courtesy between two administrators and
this page must stop calling it a control.

### Refused, by name, with where it lives

One line, on stderr, naming the door — exit 2, or 125 from `exec`:

| asked for | the answer |
|---|---|
| `get`, `print`, `show`, `cat` a value | **Refused forever.** No verb prints a secret value and no flag makes one. A person who must see a value holds the key and runs `sops -d <file>` with their own hands. |
| `put`, `set`, `add`, `edit` a value | `sops <store>/<name>.yaml`, or `sops set`; then `git add`, `git commit`, and a pull request the other collaborator approves. |
| `rotate` | The provider's console (Glenn's hand), then `sops`, then an approved pull request, then a pull on every bench, then a probe. See **Rotation**. |
| `delete` a key, or a file | `sops unset`, or `git rm`, and a rotation of whatever the deleted value was. |
| a file-shaped secret (an SSH key, an age key) handed to a program that wants a path | **Not in this store.** Generate it on the seat that uses it, authorize its public half on the box that accepts it (nova-run's box recipe, [ideas#766](https://github.com/mas-bandwidth/ideas/issues/766)), and never move the private half. |
| `recipients`, `grant`, `revoke access` | A pull request against `.sops.yaml` editing one rule, approved by the other collaborator and merged under the ruleset, then `sops updatekeys` in a second one. |
| reading or writing the macOS Keychain | Not this tool, on any bench, ever. See **The migration from the Keychain** and the tripwire that pins it. |
| a daemon, an agent, a cache, a session | Not this tool. Every call opens the file again; a cached plaintext is a plaintext with a lifetime nobody is watching. |

**The refusal to print a value is checked before any flag, path or file is read**, in the shape
nova-fuse's `lift` established. The multi-line refusal is written out, its remedy being no
command in this repo:

```
SECRETS EXEC FAIL key=SPACE_KEY: value is multi-line; a file-shaped secret is not an environment variable.
  generate it where it is used: this store holds no file-shaped secrets.
```

## The credential shape: per AI, per surface

Glenn, 2026-09-12: *"Generally, each AI (including you) should have all their secrets in
nova-secrets. Their own API key, their own email, bsky, discord, ghost access, github."* So the
file is not a provider list: it is **every surface that AI acts through**, and the key name is
the variable the tool that acts already reads.

| surface | key | file | who reads it |
|---|---|---|---|
| a pool's provider | `ANTHROPIC_API_KEY`, `XAI_API_KEY`, `GEMINI_API_KEY`, `INCEPTION_API_KEY` | `swarm-<name>` | nova-swarm workers and the gemini CLI — one key per pool file |
| DeepSeek | `DEEPSEEK_API_KEY` | `rowan` | nova-swarm's OpenCode workers, dispatched by the coordinator |
| GitHub, **org roles** | `GH_TOKEN` | `rowan` | `gh`, nova-bus, nova-board, nova-merge, the coordinator's pushes |
| GitHub, **the keeper's own repositories, plus the store** | `GH_TOKEN` | `rowan-keeper` | the keeper's own pushes, and his pull requests against `mas-bandwidth/secrets` — no org role |
| space, who and where | `SPACE_USER`, `SPACE_HOST` | `rowan` | the profiling launcher; the key itself lives on the seat, per **The model** |
| email, send and its fallback | `SMTP_PASSWORD`, `SMTP_PASSWORD_BACKUP` | `rowan-keeper` | rowan-email producer |
| email, read | `IMAP_PASSWORD` | `rowan-keeper` | rowan-email consumer |
| Bluesky | `BSKY_APP_PASSWORD` | `rowan-keeper` | rowan-bsky |
| Discord | `DISCORD_BOT_TOKEN` | `rowan-keeper` | rowan-discord producer and consumer |
| Ghost | `GHOST_ADMIN_KEY` | `rowan-keeper` | rowan-ghost |

**Rowan's two files, and which key opens each** — Glenn, decided 2026-09-12 after read 2,
replacing the earlier record in which `rowan.yaml` was sealed to both benches. His words:
*"you should be the only one who can do it, not even rowan keeper"*, *"rowan keeper
manages himself, autonomous rowan."*, *"NOT admin"*. So `rowan.yaml` is sealed to the
**admin bench key alone**, plus the recovery key, and holds the **admin** `GH_TOKEN` — the
one carrying org roles — with the coordinator's working needs beside it;
`rowan-keeper.yaml` is sealed to the **keeper bench key alone**, plus the recovery key, and
holds the keeper's **own** `GH_TOKEN` with the life's surfaces beside it — scoped to his own
repositories **plus `contents` write on `mas-bandwidth/secrets` and nothing else org-wide**,
that much because every re-seal of his own file is a branch pushed and a pull request opened
against the org's store, and no more than that: no org role, no admin anywhere, no second org
repository. Neither bench opens the other's file, and that is the point of the split rather
than a consequence of it: the admin bench is the unwalled coordinator running many children,
and a child that gets loose there must not be able to send mail, post to Bluesky, speak in
Discord or publish as Rowan; the keeper manages himself and holds no org role. The same key
name in both files is two different tokens on purpose: the name is the variable its reader
already reads, and the **scope** is the split. **A secret both seats would use is decided per
secret, by who acts with it**, and sealed once in that seat's file — `DEEPSEEK_API_KEY` is
dispatched by the coordinator, so it is in `rowan.yaml` and nowhere else. Two copies of one
value is two rotations, one forgotten.

**API keys, never OAuth tokens, and never an auth file.** Glenn: *"I would prefer API keys
per-AI instead of OAuth tokens."* An OAuth token has a refresh dance, a device flow, an expiry
and a file the harness rewrites behind your back. So **no harness auth file** is held here or
handed over; a harness that can only authenticate that way is one we start by hand. **The one
exception, and it is Glenn's:** *"The exception being that I will run Claude here manually for
you."* **Rowan's own Claude Code seat authenticates through Glenn's manual login on the admin
bench: not in the store, no `ANTHROPIC_API_KEY` in `rowan.yaml` for it, and nobody should put
one there.** That row is for *workers*, and whether they move off the plan seat is billing.

**Per seat, so per file.** Stella, Emma, Johnny and Freddy have one seat each and one file
each, and an AI with no Bluesky simply has no `BSKY_APP_PASSWORD`. A swarm pool's file holds
exactly one provider key plus a **read-only** `GH_TOKEN`
([security#1](https://github.com/mas-bandwidth/security/issues/1)) and nothing else: a worker
holding a line's send credential is a worker that can post as that line.

## The launcher, and why the order is load-bearing

One call, at the start of a seat, from the line's launcher:

```
git -C /Users/rowan/secrets pull --ff-only && \
nova-secrets exec --store /Users/rowan/secrets --as rowan \
  --key /Users/rowan/.config/nova-secrets/rowan.key \
  --sops /opt/homebrew/bin/sops \
  --only GH_TOKEN,DEEPSEEK_API_KEY,SPACE_USER,SPACE_HOST --require GH_TOKEN -- \
  nova-sandbox --read /opt/homebrew --write /Users/rowan --net-deny -- <harness> <args…>
```

The pull is the launcher's, never the tool's: invariant 8 notices a fetch nobody merged, never
a fetch nobody ran, and the `pull --ff-only &&` on this line is what runs it. The `--only`
names the four this harness needs, because a launcher knows that and a default cannot. The
inner line is **nova-sandbox's own grammar** at PR #70, two of whose rules are load-bearing
here: the harness's `HOME` inside a `--write` path (its rule 9), and the environment passing
through the wrap untouched.

Read that outward. `nova-secrets` opens the file, sets the environment, and **becomes**
`nova-sandbox`, which builds the wall and becomes the harness — so by the time a wall exists
the plaintext is already in the environment and the store is finished with. Therefore **the
store directory is in no read set**, and neither is the key file or `sops`; **the wall never
reads a key file**, by construction rather than by care; and a profile mistake can make the
harness fail but cannot make it run *without* its keys. The reverse order — sandbox outside,
secrets inside — needs the wall to permit the read a wall exists to refuse: **forbidden**.

**The second caller is nova-swarm's dispatcher**, for the pool's provider key: started under
`nova-secrets exec`, with one new field beside the `key_file` its worker description already
has. **That field is a change to SPEC-SWARM.md and to nova-swarm, owed and not done, and
nothing here claims it exists.**

## The migration from the Keychain

Every surface above exists **today** on the keeper bench as a macOS Keychain item read by a
rowan-tool calling `security find-generic-password`, so the move is not *copy a value*, it is
*change a reader*. The per-surface runbook belongs in **rowan-tools**; the order belongs here,
because at no step may there be a live consumer with a dead credential. **Issue** alongside the
old (where issuing *revokes* the old, as a Ghost admin key does, the job is unloaded first and
the migration is one sitting); **seal** in an approved pull request and pull on every bench,
Glenn's hand when the value came from his console; **switch the reader**, two edits and not one
— the tool learns one environment variable, the `security` call is **deleted** rather than kept
as a fallback, and the launchd plist's program becomes the **whole launcher line, `git -C
<store> pull --ff-only &&` included**, because invariant 8 cannot see a fetch nobody ran and a
reader that never pulls is green against a stale ref forever, rotation after rotation;
**probe**; **delete** the Keychain item only after green; **revoke** at the provider last.

**The window inside the switch is the whole difficulty, and it is closed by order, not by a
fallback.** Between the rebuilt binary landing and the edited plist being reloaded, an
interval or `KeepAlive` job that fires launches the new binary **bare**: no environment, a
refusal, no value at all. So `launchctl bootout` **before** the binary changes and `bootstrap`
**after** the plist changes — the two are never both loaded and disagreeing. The alternative,
letting the reader fall back to the old Keychain value until the plist reload, is refused: a
fallback to the Keychain is a bench where the migration silently did not happen. On a
LaunchDaemon under a per-AI user, that plist's `--store` and `--key` are paths in **that**
user's home, so that user has done the first run, and the edit is Glenn's sudo.

## Dependencies, pinned

| binary | pinned minimum | measured on the Studio bench, 2026-09-12 | probe |
|---|---|---|---|
| `sops` | **3.13.3** | `sops 3.13.3` | `<--sops> --version --disable-version-check` |
| `age-keygen` | **1.3.2** | `v1.3.2` | `<--age-keygen> --version` |

`age` itself is not invoked: sops links it, and `go.mod` gains nothing from either (test 16).

**`--disable-version-check` is not optional, and it is why the probe is a whole command
line.** A bare `sops --version` asks GitHub whether a newer sops exists — a network call, in
the launcher's path, on every seat start, with a timeout nobody chose. The probe must make **no
network call**, and a test asserts it with every egress blocked. What is parsed is the **first
line of stdout against `^sops (\d+\.\d+\.\d+)`**, everything after it ignored (the reason is a
comment beside the regex). A version the probe **cannot parse** is a refusal, never a pass; an
absent or too-old binary is a refusal naming `brew install sops` or `brew upgrade sops`.

## Rotation, said plainly

Four acts in one order, and the tool is only in the last. **Glenn revokes the old value at the
provider** — that is what makes it dead, a person's hand at a console. **The file is
re-sealed**, a pull request the other collaborator approves and merges under the ruleset, so a
rotation waits on a second account and the window between revoke and merge holds no working
value. **Every bench pulls**, on the launcher line
that starts it: a bench that has not pulled runs the dead value, and invariant 8 will not say
so — it sees a fetch nobody merged, never a fetch nobody ran — so what catches it is the next
`pull --ff-only`, and `head=` compared by eye. **A probe run proves it**; a rotation that was
not probed is a rotation that was announced.

**The old value is in git history forever, and re-sealing does not remove it.** Anyone who
cloned the store has that ciphertext, and anyone who held a key for that file can open that old
commit. Survivable **only** because of the first act, so there must not be a value in this
store that cannot be revoked. **A leaked value is revoked first, before anything else**, and
history is never rewritten to "fix" a leak: a force-push destroys the record while changing
nothing about who has the bytes, and the ruleset forbids it anyway.

## Exit codes

The repo's table governs — **0** ran and passed, **1** ran and **FAILED**, **2** could not
run — for `check`, `names` and `keygen`. `exec` uses nova-sandbox's table instead, adopted
verbatim, and that is the one deviation: an exec verb cannot return 2 for its own refusal,
because a command that exits 2 on its own would be indistinguishable from it.

| verb | 0 | 1 | 2 | 125 |
|---|---|---|---|---|
| `check` | every invariant held | an invariant failed, named | could not run | — |
| `names` | it read the file (**including zero keys**) | — never | could not run | — |
| `keygen` | the key exists and its public half is printed | — never | could not run, or the file exists | — |
| `exec` | **0–124 are the command's own exit status, whatever it is** | **the command's** | **the command's** | refused before the command started |

`names` and `keygen` never exit 1: they assert nothing about the store. `exec`'s status is the
command's from the instant of the exec — 2 included — so **only `check` is a gate**, and a
caller that gates on `exec` gates on somebody else's program. Where a gate reads an exit code,
only **0** is permission; 1 and 2 are treated alike (do not act) while staying distinct facts.

## Output grammar

One line per event, first token `SECRETS`, second the verb, third `OK` or `FAIL`, `OK` to
stdout and `FAIL` to stderr — with `exec`'s single OK line on **stderr**, the one exemption.
Every `key=value` field is escaped by the shared `internal/oneline` helper so a field is one
token; the free-text tail after `: ` is never scanned for fields. Nothing a file holds and no
caller argument can author a second line.

```
SECRETS EXEC   OK   as=<name> keys=<n> only=<all|n> required=<n> file=<path> head=<sha> cmd=<argv0>
SECRETS EXEC   FAIL <what>: <why>
SECRETS NAME        key=<NAME> clear=<true|false>
SECRETS NAMES  OK   as=<name> keys=<n> shown=<n> sealed=<n> clear=<n>
SECRETS NAMES  MORE kind=key shown=<n> total=<n> run: <remedy>
SECRETS CHECK  OK   as=<name> recipients=<n> files=<n> sealed=<n> mine=<n> foreign=<n> clear=<n> head=<sha>
SECRETS CHECK  FAIL <file>: <why>
SECRETS CHECK  FAIL as=<name> files=<n> failed=<n> shown=<n>
SECRETS KEYGEN OK   as=<name> key=<path> mode=0600 pub=<age1…>
SECRETS RULE        <one line of .sops.yaml to paste>
```

**No value, no fragment of a value, and no value's length ever appears on any line, in any
refusal, or in any error passed through from sops** — a length is a value's shape, and the
shape of an API key names its provider. sops' stderr is *not* passed through raw: it is matched
against the shapes this spec knows and reported as one of our lines, and an unrecognised one is
`sops failed: exit <n>` with the transcript **withheld** and a line telling the reader to run
the same `sops -d` themselves — the one place in this repo where a transcript is not printed
beneath the event line, a decrypt error being the one error that can contain plaintext.

**Bounded by design.** `exec` prints exactly one line, always, at any store size; `names` and
`check` cap listings at `--max` with one MORE line per kind and never cap counts; an unusable
invocation costs one line. Fully red the ceiling is not one line per file: eight invariants can
each fail on each file, so the bound is `8 × --max` plus one MORE line per capped kind plus the
count line — at the default, `8 × 20 + 8 + 1`. Test 18 **measures** it; a bound argued in a
spec and never measured is the bound that is wrong.

## Tests this spec demands

One per rule, named for the rule, each proven able to fail by a mutation before it is trusted.
Every fixture is a **throwaway store in the test's own temporary directory with throwaway age
keys** — no test ever reads the real store, the real keys, or a real credential — and each is a
real git working copy with one commit and a remote-tracking ref, invariant 8 reading one.

1. `TestASeatOpensTheFilesItsRulesNameAndNoOther` — **the negative test, and the reason the
   model exists.** The fixture is the two-key seat: keypairs `A_admin`, `A_keeper`, `B`, `C`
   and a recovery key `R`; files `a.yaml` → `{A_admin, R}`, `a-keeper.yaml` → `{A_keeper,
   R}`, `b.yaml` → `{B, R}`, `swarm-p.yaml` → `{B, R}`, `c.yaml` → `{C, R}`. With `A_admin`:
   `exec --as a` succeeds, `exec --as a-keeper` is **125 naming the key**, `sops -d
   a-keeper.yaml` exits 128, `check` is `mine=1 foreign=4`. With `A_keeper`, the mirror:
   `a-keeper.yaml` opens and `a.yaml` refuses. **That pair is the assertion Glenn's ruling
   needs**, and it is proven by **recipients**, not by a filename. With `B`: `mine=2 foreign=3`,
   green, because a seat with two files must not turn its own negative half red. `R` opens all
   five, asserted so nobody later "fixes" the negative test by making recovery impossible. Five
   mutations, each red on its own, each naming the invariant it must turn: `sops -r --add-age
   <A_keeper> a.yaml`, the file's block listing the keeper key while the rule does not →
   **invariant 2** with the `updatekeys` line, invariant 4 staying **green**,
   which is the point and not a gap (invariant 4 compares a decrypt against the file's *own*
   recipients, so a wrongly granted decrypt shows as block-versus-rule drift; what stands
   between the two benches is that `updatekeys` needs a **current recipient's** key — measured:
   with a non-recipient identity it exits 128 and changes nothing — and only then the review);
   adding `A_keeper` to `a.yaml`'s **rule** without `updatekeys` → **invariant 2**, the grant
   that grants nothing; that same rule change **merged and then `updatekeys`-ed with `R`**, so
   every recipient list agrees and invariants 2 and 4 are green → **invariant 1**, the ruling
   that no file carries two seat keys; removing a recipient from `b.yaml`'s rule while the file
   still opens for it → **invariant 2**, the revoke case; planting `A_admin` in the sops child's
   `keys.txt` → **invariant 4** on `a-keeper.yaml`, the only way a file whose recipients do not
   list this key can open, and the whole reason for the empty `HOME`.
2. `TestExecSetsExactlyTheKeysInTheFile` — the child prints its own environment: every key
   `--only` named present with exact bytes, none renamed, and **no key this tool invented** (a
   mutation adding a `NOVA_SECRETS_*` marker turns it red). Every `SOPS_*` variable is absent
   from the **child's** environment, and a `SOPS_AGE_KEY_FILE` planted in the caller's does
   not change which key is used. **And every identity file sops documents**: a foreign age
   identity at `$XDG_CONFIG_HOME/sops/age/keys.txt`, at `$HOME/Library/Application
   Support/sops/age/keys.txt`, and an unencrypted ed25519 key at `<HOME>/.ssh/id_ed25519` whose
   public half is a recipient of a fixture file — each attempted, each **refused**; red before
   green, because with the isolation removed these decrypts succeed. The `--only` half: `--only
   GH_TOKEN` puts exactly one key in the child and `only=1` on the line; `--only all` puts every
   key and says `only=all`; a `--require` for an excluded key is 125; and **no `--only` at all
   is 125 naming the flag**, the assertion that would go green if a wide default came back.
3. `TestExecReplacesItselfAndPassesTheStatusThrough` — pid before and after is the same; a
   command exiting 7 makes it exit 7; commands exiting 1 and **2** make it exit 1 and 2 with no
   `SECRETS … FAIL` line; a command exiting 125 is passed through, told from a refusal by the
   absence of our line; the child's `RLIMIT_CORE` is 0; a signal-killed command reproduces the
   shell's status. Without `execve` the test states the difference rather than not asserting it.
4. `TestNoVerbPrintsAValue` — a source tripwire classifying every printed argument
   (`internal/oneline/audit`), plus a behavioral half: a distinctive 40-byte fixture value,
   every verb in every mode including every refusal, and the string in **no** byte of stdout or
   stderr. A mutation printing `len(value)` turns it red.
5. `TestGetIsRefusedBeforeAnythingIsRead` — `nova-secrets get …` with no store, no key file,
   no sops binary and a `--store` that would panic if opened: one line, exit 2, naming only
   `sops -d` in a person's hands, and the process stat shows **no file opened**.
6. `TestSopsErrorsAreNeverPassedThroughRaw` — a sops stderr fixture carrying a
   plaintext-looking payload: it reaches no stream, and the unrecognised case prints `sops
   failed: exit <n>` and the remedy, never the transcript.
7. `TestTheKeyFileModeIsARefusalOnEveryVerb` — `0644`, `0640`, `0600` in a `0755` directory:
   each refused naming `chmod`, on `exec`, `names` and `check`; `0600` in `0700` passes; a key
   file **inside** `--store` makes `check` red.
8. `TestTheVersionProbeMakesNoNetworkCall` — the probe answers with egress blocked; `sops
   3.9.0` is refused naming `brew upgrade`; `banana` is refused as unparseable, **not**
   accepted; absent and non-executable are two different sentences.
9. `TestARequireThatIsMissingRefusesBeforeTheCommandStarts` — the command writes a sentinel;
   after the refusal the sentinel does not exist; every missing `--require` is named in one
   run, sorted.
10. `TestAMultiLineValueIsRefusedWithGenerateItWhereItIsUsed` — a fixture holding a multi-line
    value: `exec` refuses naming the key and printing the remedy, and the remedy names **no file
    in this store**, there being no sibling to point at; a NUL is its own sentence. The `names`
    half: that key is listed with **no `--key`, no `--sops`, no key file on the bench and no
    `sops` process started**, asserted by a `--sops` that would fail if executed.
11. `TestAKeyNameThatIsNotAnEnvVarIsRefused` — `gh-token`, `2FA`, `A B`, the empty name: each
    refused naming the key; `GH_TOKEN` and `A1_B` accepted; all the bad ones in one run,
    sorted.
12. `TestCheckCapsEachKindSeparatelyAndAlwaysPrintsTheCount` — 30 unsealed files and 1
    foreign-openable one: the loud kind does not eat the quiet one, each kind caps with its own
    MORE line, the count line prints on the red run, `--max 0` prints all, a negative `--max`
    is refused.
13. `TestCheckFailsClosedOnEverythingItCannotRead` — an unreadable `.sops.yaml`, an unreadable
    AI file, a store that is a file, a store with no `.sops.yaml`: **absent, unreadable and
    unrecognised pinned as three different answers**.
14. `TestKeygenNeverOverwritesAndNeverTouchesTheStore` — an existing file at `--key` is refused
    with its bytes unchanged; a `0755` parent is refused naming `mkdir -m 700`; on success the
    created file is the only filesystem change anywhere, asserted by hashing the whole store tree
    before and after; the private key appears on no stream. The printed `SECRETS RULE` block
    satisfies invariant 1 when pasted: anchored, two recipients, the recovery placeholder.
15. `TestTheLauncherOrderWorksWithTheStoreFullyDenied` — end to end in nova-sandbox's real
    grammar at PR #70, the write set carrying the probe's `HOME` and neither the store nor the
    key directory in any read set: the probe sees the keys. The reverse nesting is asserted to
    **fail**. It skips with a stated reason when `nova-sandbox` is not built, never vacuously.
16. `TestNoKeychainAndNoCryptoDependency` — a source tripwire: no `security` invocation, no
    Keychain import, no `filippo.io/age`, no `getsops`, and an aliased import or a helper in a
    second file cannot defeat it (nova-bus's blind-spot list applies unchanged).
17. `TestREADMEFirstRunMatchesWhatTheToolPrints` — the six lines below run against a throwaway
    store, the transcript compared to the README's **by prefix and field name, never by
    value**, so it stays a document. The `check` line on a seat with no file of its own is
    pinned as the rules give it, not as a guess: **exit 2**, then green `mine=0` once a file
    exists that this key does not open.
18. `TestOutputSizeAtTheLargestPlausibleState` — 12 files × 16 keys; lines and bytes for every
    verb in green and red, stdout and stderr; the table in the commit.
19. `TestNoFileContentOrCallerArgumentCanForgeALine` — a key name, a file name, a `--require`
    and a sops error carrying `\nSECRETS CHECK OK …`, a terminal repaint, a bidi control: none
    authors a second line, including from the flag parser before our first instruction runs.
20. `TestAStaleWorkingCopyIsRefused` — a fixture whose `HEAD` is behind its remote-tracking ref:
    `check` is exit 1 on invariant 8 naming `git -C <store> pull --ff-only`, `exec` is 125 with
    the same line, `head=` on the green run is `HEAD`'s short sha. One commit *ahead* is red too,
    as its own sentence. And the case it does **not** catch, asserted so nobody believes
    otherwise: a tracking ref left stale because nothing fetched while the remote moved on —
    **green, by design**, which is why every launcher pulls. Detached `HEAD`, no upstream, and a
    `.git` that is a file are three more fixtures, each a refusal naming its fact. Mutation:
    point the ref at `HEAD`, both green.

## The first run: six lines a stranger pastes

On a fresh bench, with `brew install sops age` done, `gh` and `nova-secrets` on PATH, and the
store cloned to `~/secrets`. **The clone is the one thing this tool cannot help you with**: the
store is private and your `GH_TOKEN` is inside it, so somebody else clones it for you, or the
bench holds the one credential the clone needs — an SSH key generated on that bench, its public
half authorized by a hand that already has access. Three things are outside the store by
design, that key, this bench's age key and Glenn's recovery key, and `nova-secrets` carries
none of them. Nothing below is a default: every path is typed, once.

```
mkdir -m 700 -p ~/.config/nova-secrets
nova-secrets keygen --as rowan --key ~/.config/nova-secrets/rowan.key --age-keygen $(brew --prefix)/bin/age-keygen
# paste the printed SECRETS RULE lines into .sops.yaml in a PR touching only your own rule; the other collaborator approves and merges it, and then a holder of an existing key seals the file in a second PR — `sops rowan.yaml` if it is new, `sops updatekeys rowan.yaml` if it exists — because the merge alone grants you nothing
nova-secrets check --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops
nova-secrets names --store ~/secrets --as rowan
nova-secrets exec  --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
```

Six lines: one directory, one keygen, one comment that is the step other people do for you,
then check, names and a run that prints a name from GitHub. The third is a comment on purpose —
**a stranger cannot finish this alone, and the page must say so where the wait happens** rather
than leave them to find it in a refusal. That pull request needs a GitHub identity the stranger
does not have yet (its token is inside the store), so Glenn opens it, and the approver is the
other collaborator, under **"Reviewed" is a control** above. `check` runs before `names`
because the first command to touch the store should be the one that says whether the store is
what this spec says. What it prints before the grant, exactly: while `rowan.yaml` does not
exist, **exit 2 listing the names that are in the store**; once it exists sealed to somebody
else's key, **green with `mine=0`**, invariant 4 passing over no file of yours. Neither is exit
1, and both are the right answer rather than a stumble.

## Owed, and where the rest lives

The full work list is in this pull request's body, and new ideas are issues. Owed before this
spec is true: revision 1's library deleted (`internal/secrets/*`, nova-tools **PR #72**,
carrying forward only `internal/secrets/secret.go`); the tool registered in
[SPEC.md](../SPEC.md); `cmd/nova-secrets` and the twenty tests, each red before green; the
README and the six-line first run, then **a cold hour by a line that did not write this**; the
Keychain migration, one surface at a time; and `GOOS=windows go test -c` before the first push.

**The store's own repair, Rowan's hand, as a pull request under the ruleset**: remove the
README's `space_key` line and unseal that value from `<line>.yaml` (generated on the seat now,
the old one revoked at the box); add the `rowan-keeper.yaml` rule with the keeper key alone and
give `rowan.yaml` the admin key alone; add the recovery recipient. That last is necessarily
Rowan's hand and Glenn's approval — adding `R` to a sealed file is `updatekeys` by a **current
recipient**, and Glenn holds no bench key — and until it lands nothing in the store is
recoverable. The store's one rule is anchored at both ends already, so invariant 1's anchor
half is green there today; its two-recipient half lands with the same pull request.

Open for Glenn, each taken by default if he is silent past the next window he is at the bench,
recorded on [security#20](https://github.com/mas-bandwidth/security/issues/20): whether swarm
workers on Claude models move to an `ANTHROPIC_API_KEY` (*default: they stay*); whether the
store stays one repository (*default: one*); what belongs in `unencrypted_regex` beyond
`SPACE_USER|SPACE_HOST` (*default: leave it*); which AIs get a unix user and when (*default:
Rowan first*); and the two the ruleset raises above — demoting `rowan-claude` to `write` on the
store, on [security#22](https://github.com/mas-bandwidth/security/issues/22) (*default:
demote*), and an AI account as the approver of record for his own store changes (*default:
accepted*).

---

*Rowan, 2026-09-12. No credential value was read, written, printed or named anywhere in this
work; the real store was read only through its `.sops.yaml`, its README, its ruleset and its
collaborator permissions, and no `sops` or `age` command was run against it.*
