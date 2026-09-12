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

Every line below but the ds4 row was read that minute on the Studio, and that row
carries its own date. **This is a snapshot, and it
moves** — the ollama store went from six tags to nine to twelve, and from 53 GB to 105
GB to 162 GB, in about ninety minutes of the same night — which is why the numbers are
dated and live here rather than in the spec.

| fact | value |
|---|---|
| ollama's service | `homebrew.mxcl.ollama`, a **per-user LaunchAgent** under `glenn` (`launchctl list` → `56334 1 homebrew.mxcl.ollama`), i.e. `brew services`, not a LaunchDaemon |
| ollama's store setting | `OLLAMA_MODELS` **unset** on the running daemon (its only `OLLAMA_*` environment is `FLASH_ATTENTION=1`, `KV_CACHE_TYPE=q8_0`), so the store is that account's `~/.ollama/models` |
| ollama's store size | `/Users/glenn/.ollama` = **162 GB**, twelve tags advertised by `/api/tags` |
| ds4's weights | **528 GB** under `/Users/rowan/rowan-working/ds4` (2026-09-12, read 4), the other account's home; no ds4 server running |
| the two homes | `/Users/glenn` and `/Users/rowan` are both `drwxr-x---` (**750**) `<user>:staff` — each account reads the other's **only** because of that mode |
| the shared path | `/Users/Shared` is `drwxrwxrwt root:wheel` (**1777**); `/Users/Shared/nova-local` does not exist yet |

The last two rows are the whole argument. `750 <user>:staff` is not a guarantee: a home's
mode is its owner's to change, macOS's default for a new account is `700`, and one
account outside `staff` ends the sharing silently. A requirement for *both accounts*
cannot rest on it.

## What the recipe does, once

Four steps **in this order and no other**: make the store, copy, switch the daemon,
check. Copy before switch and verify before delete, because the thing being moved is
528 GB nobody wants to download twice. **Never a copy per user**: a second copy is the
same weights bought twice on a box where one model is 434 GiB.

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
they are manifests and blobs in the store being copied. Nothing is removed here.

```
sudo rsync -aH --info=progress2 ~/.ollama/models/ /Users/Shared/nova-local/models/ollama/
sudo chown -R root:staff /Users/Shared/nova-local/models/ollama
```

**3. Make the engine a box service, not a per-account agent, and point it at the new
store.** On darwin that is a **LaunchDaemon** in `/Library/LaunchDaemons`, loaded at
boot, bound to loopback, with its account named in the plist's `UserName` (a LaunchDaemon
is **root** if no `UserName` is set, and the recipe should not run an engine as root) and
its store named in the plist's `EnvironmentVariables` (`OLLAMA_MODELS`), because a shell
`export` never reaches a launchd job. On this box ollama is installed by Homebrew, so the
switch goes through `brew services` rather than by hand, and the existing per-user agent
must be stopped first. **The switch unloads every loaded model once** — one restart, one
cold load on the next `serve`; until then `status` shows `loaded=0` and the derived tags
are still listed. On linux it is a **systemd system unit** with the same two facts: a
service user, and the store in the unit's environment. ds4 has no store setting at all —
its store is the `-m` path in whatever starts it, which is the recipe's own plist or unit
argv.

```
brew services stop ollama
sudo brew services start ollama
sudo /usr/libexec/PlistBuddy -c 'Add :EnvironmentVariables:OLLAMA_MODELS string /Users/Shared/nova-local/models/ollama' /Library/LaunchDaemons/homebrew.mxcl.ollama.plist
sudo launchctl bootout system/homebrew.mxcl.ollama; sudo launchctl bootstrap system /Library/LaunchDaemons/homebrew.mxcl.ollama.plist
```

**4. Check it from both accounts, and only then remove the old copy.** The observable is
the one `nova-local` gives you: `status` from each account prints the same engines, the
same models and the same `store=` with `shared=yes`, then one `nova-local serve` and one
real task. That is rule 15's test 17, run for real instead of against a fake, and it is
where the two-account requirement is enforced on this box — with `serve
--require-shared-store` passed by whatever starts a model here thereafter. `~/.ollama`
goes only after that verify.

## What the recipe must still decide, and this document does not

These are open, and a builder will ask them before writing the plist:

- **The service account** on darwin: a dedicated `_nova-local` user, or the account that
  already owns the weights? (`nova-local` never needs to know; the store's group does.)
- **Whether `brew services` stays the installer** for ollama once the daemon is a
  LaunchDaemon, or the recipe writes its own plist and stops using brew for it.
- **The linux service user's name** — `nova-local` is the working assumption in the
  spec's path (`/var/lib/nova-local`) and nothing else depends on it.
- **What happens to the old `~/.ollama`** after the verify: removed, or left until the
  next reboot proves the daemon comes back on the new path.

## What `nova-local` does about all of this

Nothing but report it — and refuse when asked, which is the first paragraph above and
not restated here. It writes exactly one file, the
`--out` path of `worker`, and never a plist, a unit, or anything under `/Library` — a
tool that wrote those would be an outbound actor, which SPEC-LOCAL rule 11 forbids.
