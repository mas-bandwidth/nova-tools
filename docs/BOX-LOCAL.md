# BOX-LOCAL — what the box recipe does once so nova-local works for every account

This is the **box's** side of [SPEC-LOCAL.md](SPEC-LOCAL.md) rule 15. `nova-local`
starts no daemon, writes no unit and moves no weights; it reads what an engine reports,
prints `store=`, and refuses a store under a home directory. Everything that makes that
refusal unnecessary is here, and `nova-line`'s recipe will consume this document.

Why it exists, in Glenn's words (bus, 2026-09-12; no receipt in `memory/` yet):

> *"I would like for local models to be accessible both here in this admin account, and
> in your rowan account. This is a requirement for this local setup and nova-local."*

> *"I'd really not like to the local models to stay in rowans account, but move to a
> shared location explicitly."*

## Measured on this box, 2026-09-12T02:22Z

Every line below was read that minute on the Studio. **This is a snapshot, and it
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

**1. Make the store.** `/Users/Shared/nova-local/models/<engine>` on darwin,
`/var/lib/nova-local/models/<engine>` on linux, one subdirectory per engine
(`models/ollama`, `models/ds4`). `/Users/Shared` is world-writable, so **any** account
can create it and the owner is whoever ran first — the recipe must therefore create it
deliberately, not leave it to first use: `root:staff`, mode `2775` (setgid, so every
file a service writes stays group `staff` and every model-running account can read it).

**2. Make the engine a box service, not a per-account agent.** On darwin that is a
**LaunchDaemon** in `/Library/LaunchDaemons`, loaded at boot, bound to loopback, with
its account named in the plist's `UserName` (a LaunchDaemon is **root** if no `UserName`
is set, and the recipe should not run an engine as root) and its store named in the
plist's `EnvironmentVariables` (`OLLAMA_MODELS`), because a shell `export` never reaches
a launchd job. On this box ollama is installed by Homebrew, so the switch goes through
`brew services` (`sudo brew services start ollama` installs to `/Library/LaunchDaemons`)
rather than by hand, and the existing per-user agent must be stopped first. On linux it
is a **systemd system unit** with the same two facts: a service user, and the store in
the unit's environment. ds4 has no store setting at all — its store is the `-m` path in
whatever starts it, which is the recipe's own plist or unit argv.

**3. Move the weights, in this order and no other.** Copy the store to the new path;
switch the engine's own setting to it; **verify a model serves from the new path** —
`nova-local status` from *each* account showing the same `store=` and `shared=yes`, then
one `nova-local serve` and one real task; and only then remove the old copy. Copy before
switch, verify before delete, because the thing being moved is 528 GB nobody wants to
download twice. **Never a copy per user**: a second copy is the same weights bought
twice on a box where one model is 434 GiB.

**4. Check it from both accounts.** The observable is the one `nova-local` gives you:
`status` from each account prints the same engines, the same models and the same
`store=` with `shared=yes`. That is rule 15's test 17, run for real instead of against a
fake.

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

Nothing, except report and refuse. It prints `store=` and `shared=<yes|no|unknown>` on
every `LOCAL ENGINE` line, and `serve` exits 1 when the store it can read resolves under
a home directory, naming the store and pointing here. It writes exactly one file, the
`--out` path of `worker`, and never a plist, a unit, or anything under `/Library` — a
tool that wrote those would be an outbound actor, which SPEC-LOCAL rule 11 forbids.
