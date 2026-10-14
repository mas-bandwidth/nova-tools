SOURCE: mas-bandwidth/barrio README.md at main d2dc14e9d269 (2026-10-07), copied with personal and machine names replaced by roles. Staged as the seed of the card nova-local-is-born-from-barrio; the card adapts it into docs/nova-local/ and deletes this directory.

# barrio

Local serving of DeepSeek V4.1 Flash on Apple Silicon (Mac Studio, 512 GB unified memory), built as a pro-class, sovereign friend for the nova fleet.

Started 2026-10-07 by the owner and the coordinator, from a reading of [<recipe-author>/ds4-recipe](https://tangled.org/<recipe-author>/ds4-recipe) (DeepSeek V4.1 Flash on AMD Strix Halo). The recipe's method is kept; its hardware-specific machinery (expert cache, NVMe miss handling, HIP kernels) is not needed when the experts fit in memory.

## The use case (the owner, 2026-10-07 ~22:10 ET)

**100% utilisation, flash-tier work offloaded from the fleet machines to nova-local.** The fleet's flash cards (wide, mechanical, latency-tolerant) go to the box on many slow lanes instead of to the API; friends get a small fast pool beside it. Flexible and multi-lane either way: pools are a nova-config setting. Deployment shape (the owner, ~22:15 ET): **the box is itself a fleet machine** in nova-config with width n, every lane on the `local` route, running exclusively local-model work; the harness children run on its own cores beside the engine. No cross-machine routing is needed; the dealer sees one more machine with a big width and $0 routes. Local is a configuration, not a tier (the owner, ~22:20 ET; the tier call left to the coordinator): a route row has provider `local` and a tier set on the row, defaulting to the loaded model's class. The two pools are two rows on the same model: `fleet` tier flash, 128 lanes (the flash cards; a pro-class model doing flash work is the point); `friends` tier pro, 4 lanes (V4.1 Flash is what Zhi runs, classed pro). 

## The one number

Decode is bandwidth-bound. Tokens per second per lane = sustained memory bandwidth / bytes read per generated token. Every lever here either shrinks the bytes or yields more than one token per read.

| Machine | Memory | Bandwidth (sustained est.) | Fits experts resident |
|---|---|---|---|
| Mac Studio M3 Ultra 512 GB (the keeper machine, in hand) | 512 GB | ~650–700 GB/s | yes |
| Mac Studio M5 Ultra 512 GB (not owned; possible) | 512 GB | ~1 TB/s | yes |

Model: 552B total + 196B Engram, 8B active prefill / 16B active decode, 40 layers, 384 experts (6 active). On disk ~0.51 TB; experts ~289 GB MXFP4, backbone ~10 GB, Engram ~203 GB (sparse lookups; stays on SSD if it must).

## Layers, bottom up

Each layer is secured (measured, modelled where it has state, read cold) before the next starts. Stop at any layer where the measurement does not match the arithmetic, and say so.

0. **Verify on the model card:** V4.1 Flash uses sparse attention (bounded KV read per token at any context) and ships a draft (MTP) head. Without sparse attention, long sessions are KV-bound and nothing below matters at depth.
1. **Load and one correct token.** antirez/ds4 as the kernel layer (decided in E00-F02, 2026-10-07; MLX is the fallback). Output checked against DeepSeek's reference.
2. **Memory ledger.** Experts, backbone, Engram hot set, KV, per-slot budgets. TLA+ model of the allocator.
3. **Decode at the floor.** Measure bytes/token and tok/s against the bandwidth floor. Report the gap.
4. **Backbone to 4-bit.** `[approx]` until KL < 0.01 against the reference; then kept.
5. **Speculative decoding** with the model's draft head, then a draft tree. `[exact]`: verification never changes outputs; that invariant is the TLA+ model.
6. **Persistent KV with suffix reuse.** TLA+ for the cache.
7. **Multi-lane serving** with shared prefill. The backbone read is shared across lanes; experts grow with distinct positions.

## Arithmetic (estimates, not measurements)

M5 Ultra, one lane: ~15 GB/step → ~65 tok/s floor; backbone 4-bit → ~100; tree speculation → ~250+ on code, ~150 prose. Four lanes ~300–500 tok/s aggregate on code. M3 Ultra: scale by ~0.65.

Economics: never cheaper per token than the DeepSeek API. The case is sovereignty (no rate limits, no credits to run out, model-agnostic 512 GB), and width added to the pro tier.

## Exactness tags (from the recipe)

`[exact]` bit-identical outputs; `[reference-defining]` chosen once then reproduced; `[approx — approved]` quality-checked with a KL number; `[policy]` scheduling only. Every change carries one. Results go in `RESULTS.md`, written when measured, never from memory.
