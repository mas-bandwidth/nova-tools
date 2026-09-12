# BOX-LOCAL — what the box recipe does once so nova-local works for every account

This is the **box's** side of [SPEC-LOCAL.md](SPEC-LOCAL.md) rule 15. `nova-local`
starts no daemon, writes no unit and moves no weights; it reads what an engine reports,
prints `store=` and `shared=` always, and refuses a store under a home only when asked
(`serve --require-shared-store`). Everything that makes `shared=yes` true is here, and
`nova-line`'s recipe will consume this document.

Why it exists, in Glenn's words (bus, 2026-09-12; no receipt in `memory/` yet):

> *"I would like for local models to be accessible both here in this admin account, and
> in your rowan account. This is a requirement for this local setup and nova-local."*

> *"I'd really not like to the local models to stay in rowans account, but move to a
> shared location explicitly."*

## Measured on this box, 2026-09-12T02:22Z

Every line below but the four rows carrying their own date was read that minute on the
Studio. **This is a snapshot, and it
moves** — the ollama store went from six tags to nine to twelve, and from 53 GB to 105
GB to 162 GB, in about ninety minutes of the same night — which is why the numbers are
dated and live here rather than in the spec.

| fact | value |
|---|---|
| ollama's service | `homebrew.mxcl.ollama`, a **per-user LaunchAgent** under `glenn` (`launchctl list` → `56334 1 homebrew.mxcl.ollama`), i.e. `brew services`, not a LaunchDaemon |
| ollama's store setting | `OLLAMA_MODELS` **unset** on the running daemon (its only `OLLAMA_*` environment is `FLASH_ATTENTION=1`, `KV_CACHE_TYPE=q8_0`), so the store is that account's `~/.ollama/models` |
| ollama's store size | `/Users/glenn/.ollama` = **162 GB**, twelve tags advertised by `/api/tags` |
| ds4's weights | **528 GB** under `/Users/rowan/rowan-working/ds4` (2026-09-12, read 4), the other account's home; no ds4 server running |
| ds4's `ds4flash.gguf` | a **hard link** to the 464 GB **Pro** file, not a separate Flash GGUF (2026-09-12, `research/2026-09-12-local-model-bakeoff.md` on standard, `c94ee4d`), so a `ds4-server` started with no `-m` loads Pro; this box's job must pass an explicit `-m` to the Flash weights |
| ds4's lock file | `/tmp/ds4.lock` is created `rowan:0600` (same source), so the second account cannot start ds4 unless `DS4_LOCK_FILE` names a group-writable path — the recipe puts it under the shared store |
| ollama's sharing today | already shared across accounts: **one** daemon on `127.0.0.1:11434`, one store, pulls done by the daemon (same source); only the store's location **under a home** is what this recipe moves |
| the two homes | `/Users/glenn` and `/Users/rowan` are both `drwxr-x---` (**750**) `<user>:staff` — each account reads the other's **only** because of that mode |
| the shared path | `/Users/Shared` is `drwxrwxrwt root:wheel` (**1777**); `/Users/Shared/nova-local` does not exist yet |

The last two rows are the whole argument. `750 <user>:staff` is not a guarantee: a home's
mode is its owner's to change, macOS's default for a new account is `700`, and one
account outside `staff` ends the sharing silently. A requirement for *both accounts*
cannot rest on it.

## What the recipe does, once

Four steps **in this order and no other**: make the store, copy, switch the daemon,
check. Copy before switch and verify before delete, because the thing being moved is
ollama's 162 GB (the table above) and nobody wants to download it twice; ds4's 528 GB is
moved by no block here — step 1 only makes `models/ds4` for it. **Never a copy per
user**: a second copy is the same weights bought twice on a box where one model is
434 GiB.

**1. Make the store.** `/Users/Shared/nova-local/models/<engine>` on darwin,
`/var/lib/nova-local/models/<engine>` on linux, one subdirectory per engine
(`models/ollama`, `models/ds4`). `/Users/Shared` is world-writable, so **any** account
can create it and the owner is whoever ran first — the recipe must therefore create it
deliberately, not leave it to first use: `root:staff`, mode `2775` (setgid, so every
file a service writes stays group `staff` and every model-running account can read it).

```
sudo mkdir -p /Users/Shared/nova-local/models/ollama /Users/Shared/nova-local/models/ds4
sudo chown -R root:staff /Users/Shared/nova-local && sudo chmod -R 2775 /Users/Shared/nova-local
```

**2. Copy the weights, before anything is switched.** The daemon keeps serving from the
old path for the whole copy, and the derived tags (SPEC-LOCAL rule 6) travel with it —
they are manifests and blobs in the store being copied. Nothing is removed here. This
copy is of a **live** store, so a tag pulled while it runs is not in it — step 3 runs the
same line again once the daemon is stopped, which is what closes that window.

```
sudo rsync -aH --progress ~/.ollama/models/ /Users/Shared/nova-local/models/ollama/
sudo chown -R root:staff /Users/Shared/nova-local/models/ollama
sudo chmod -R g+w /Users/Shared/nova-local/models/ollama
sudo find /Users/Shared/nova-local/models/ollama -type d -exec chmod g+s {} +
```

`--progress`, not `--info=progress2`: macOS ships **openrsync** (`rsync --version` →
`openrsync: protocol version 29`), which does not have rsync 3.1's `--info=`; the
`--info=progress2` line exits 1 on the first character of the copy. `--progress` is
accepted by both. The two mode lines are not decoration: `rsync -a` preserves the source's
`755` directories and `644` blobs, so step 1's `2775` reaches only the empty directories
it made, and without `g+w`/`g+s` a **non-root** service user (step 3) cannot write a new
blob and the first `ollama pull` after the switch fails.

**3. Make the engine a box service, not a per-account agent, and point it at the new
store.** On darwin that is a **LaunchDaemon** in `/Library/LaunchDaemons`, loaded at
boot, bound to loopback, with its account named in the plist's `UserName` — a LaunchDaemon
is **root** if no `UserName` is set, and the recipe should not run an engine as root. On
this box ollama is installed by Homebrew, so the switch goes through `brew services`
rather than by hand, and the existing per-user agent must be stopped first. `brew
services` writes a `UserName` **only** when given `--sudo-service-user <user>` (Homebrew
`services/cli.rb:535-541`: `plist_data["UserName"] = sudo_service_user`); a plain `sudo
brew services start ollama` only warns and runs the engine as root, which is the thing
this step forbids. **The switch unloads every loaded model once** — one restart, one cold
load on the next `serve`; until then `status` shows `loaded=0` and the derived tags are
still listed. On linux it is a **systemd system unit** with the same two facts: a service
user, and the store in the unit's environment (`OLLAMA_MODELS`) — systemd does not rewrite
a unit behind you, so on linux the environment is the right place for it. ds4 has no store
setting at all — its store is the `-m` path in whatever starts it, which is the recipe's
own plist or unit argv.

**The store is not named in the darwin plist, on purpose.** `brew services start`
regenerates the plist from the formula's own `service do` block on **every** start
(`cli.rb:531-547` removes the old file and writes `service.service_contents`; `restart` is
stop+start), and `brew cat ollama`'s block carries exactly `OLLAMA_FLASH_ATTENTION` and
`OLLAMA_KV_CACHE_TYPE` and nothing else. So an `OLLAMA_MODELS` added by hand — a
`PlistBuddy -c 'Add :EnvironmentVariables:OLLAMA_MODELS …'` line, which does work when run
— is gone at the next `brew services restart ollama` or `brew upgrade ollama`, and the
daemon comes back on the service user's own empty `~/.ollama`. Brew's documented user
override file is no better here: `$HOMEBREW_USER_CONFIG_HOME/services/ollama.env` is
merged into the plist by `service.rb:486-500`, but that method skips the file when the
definition is generated as root — *"user env overrides are not supported for root
services"* — which is exactly a `sudo brew services` daemon. **So the recipe uses the one
thing brew never rewrites: the engine's default path itself.** `~/.ollama/models` in the
service user's home becomes a symlink to the shared store; the daemon resolves it on every
start, whatever brew regenerates, and no `OLLAMA_MODELS` is needed on this box at all.

```
brew services stop ollama
sudo rsync -aH --progress ~/.ollama/models/ /Users/Shared/nova-local/models/ollama/
sudo chown -R root:staff /Users/Shared/nova-local/models/ollama && sudo chmod -R g+w /Users/Shared/nova-local/models/ollama
sudo find /Users/Shared/nova-local/models/ollama -type d -exec chmod g+s {} +
mv ~/.ollama/models ~/.ollama/models.pre-nova-local
ln -s /Users/Shared/nova-local/models/ollama ~/.ollama/models
sudo brew services start ollama --sudo-service-user glenn
```

Lines 2-4 are step 2's copy and its two mode repairs again, after the daemon is stopped:
incremental, seconds, and what catches a tag pulled during step 2's window. The mode
lines repeat because `rsync -a` re-applies the source's `755` directories, so without
them step 2's `g+w`/`g+s` is undone and the non-root service user cannot write a new
blob. `~` on lines 5 and 6 is the **service
user's** home — the account open question 1 below picks, `glenn` today because that
account owns the weights and is in `staff` — and those two lines are run in that account,
not under `sudo`, so the symlink is that user's. Nothing is deleted here either: the old
store is renamed aside and goes in step 4.

**4. Check it from both accounts, and only then remove the old copy.** The observable is
the one `nova-local` gives you: `status` from each account prints the same engines, the
same models and the same `store=` with `shared=yes`, then one `nova-local serve` and one
real task. That is rule 15's test 17, run for real instead of against a fake, and it is
where the two-account requirement is enforced on this box — with `serve
--require-shared-store` passed by whatever starts a model here thereafter. Then one more
check that belongs to step 3's choice: `brew services restart ollama` and `status` again,
which is what proves the store survived a plist regeneration rather than merely a boot.
`~/.ollama/models.pre-nova-local` goes only after both.

## What the recipe must still decide, and this document does not

These are open, and a builder will ask them before writing the plist:

- **The service account** on darwin: a dedicated `_nova-local` user, or the account that
  already owns the weights? (`nova-local` never needs to know; the store's group does.)
- **Whether `brew services` stays the installer** for ollama once the daemon is a
  LaunchDaemon, or the recipe writes its own plist and stops using brew for it.
- **The linux service user's name** — `nova-local` is the working assumption in the
  spec's path (`/var/lib/nova-local`) and nothing else depends on it.
- **What happens to the old `~/.ollama/models.pre-nova-local`** after the verify:
  removed, or left until the next reboot proves the daemon comes back on the new path.
- **Who passes `--require-shared-store` on this box** once it is set up: `nova-run`'s
  invocation of `serve`, an alias in each account, or the operator by hand. SPEC-LOCAL
  rule 15 says the flag is passed there and names the candidates; choosing one is this
  document's, not the spec's.

## What `nova-local` does about all of this

Nothing but report it — and refuse when asked, which is the first paragraph above and
not restated here. It writes exactly one file, the
`--out` path of `worker`, and never a plist, a unit, or anything under `/Library` — a
tool that wrote those would be an outbound actor, which SPEC-LOCAL rule 11 forbids.
