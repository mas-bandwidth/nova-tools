The stable tools point release after v1.6.11. It gives the native Windows member a wall and a shared lock count. nova-sandbox gains a Windows body, `lowil-job`, so a card on captain can run inside a wall instead of `--no-wall`. `nova-swarm member --lane-lock` lets each lane hold a slot of a machine-wide count for its whole life. nova-sprint is released on its own line.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.12 --bin <dir>` with the nova-sprint release staged beside them (see docs/SPEC-RELEASE.md). Of the tools, nova-sandbox and nova-swarm changed, and nova-friend's Windows binary picks up the new wall through pkg/sandbox.

## What changed in v1.6.12

- **The Windows wall, `backend=lowil-job` (#6037).** Before this, nova-sandbox refused every command on Windows with `no_sandbox`.
  - The child runs on a copy of the parent's token at low integrity, with every privilege removed but SeChangeNotify.
  - Only the `--write` roots, the tmp and HOME are labelled low, so they are the only places the child can write. Labels never pass through a reparse point, and each handle's final path must be inside the root. A root that is a volume, the profile, its temp or a system directory, or an ancestor of one, is refused.
  - The process tree runs in a kill-on-close Job Object, which carries the process cap and the memory cap. On Windows the Go caches are per card.
  - Reads and the network are not confined, and `--net-deny` is refused. Administrators is not made deny-only. The child can write every low-labelled object, including AppData\LocalLow and other cards' roots on the same member, so there is no isolation between cards. On an elevated member account, the low child can still start and stop services granted to Administrators. A dedicated non-admin member user removes that exposure. All of this is documented in docs/SPEC-SANDBOX.md.
  - Security read: https://github.com/mas-bandwidth/nova-tools/pull/6037#issuecomment-6281817402.
- **`nova-swarm member --lane-lock <argv>` (#6039).** Each lane starts the holder before native, and begins only once the holder's first line starts with `HELD`; any other first line refuses the lane. The wait is bounded at 2h, after which the holder is ended and the card goes back. The lane closes the holder's stdin when it ends, which releases the slot.
  - `{card}` in the argv becomes the card id.
  - A timing-gate card gets `--exclusive`. That is a card with soak, stress, repeat or load as a word of its id, or `exclusive` in its GATE-NEEDS.
  - On captain the holder is rowan-tools' `winlock.exe hold`, so the native lanes and the WSL jobs share one count of four slots.
