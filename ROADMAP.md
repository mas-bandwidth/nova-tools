# nova-tools roadmap

Generated from [docs/roadmap.sexp](docs/roadmap.sexp), the roadmap's data: each planned card is kept there with its whole brief. Edit the data, not this page.

## v1.2.0 (current release)

In progress: the friend communications core (delivery, proof of life, limits, receipts), nova doctor, the generated CLI docs and the glossary, and the security fixes in flight, with the work already in review and merging.

## v1.3.0 (planned)

507 cards in 32 streams, moved out of the v1.2.0 sprint on 2026-10-04 to be done after it.

| stream | cards |
|---|---:|
| [rerate-v1-2-0](#rerate-v1-2-0) | 102 |
| [shrink](#shrink) | 66 |
| [use](#use) | 50 |
| [debt](#debt) | 47 |
| [prose](#prose) | 32 |
| [harness](#harness) | 18 |
| [reference](#reference) | 18 |
| [fleetnames](#fleetnames) | 17 |
| [read](#read) | 17 |
| [friends-general-v1-2-0](#friends-general-v1-2-0) | 15 |
| [tenv](#tenv) | 14 |
| [tools-v1-2-0-setup](#tools-v1-2-0-setup) | 12 |
| [classes](#classes) | 11 |
| [security2](#security2) | 10 |
| [busdogfood2](#busdogfood2) | 8 |
| [contract](#contract) | 8 |
| [dead](#dead) | 8 |
| [ttime](#ttime) | 8 |
| [tla](#tla) | 7 |
| [tools-v1-2-0-docs](#tools-v1-2-0-docs) | 7 |
| [machinery2](#machinery2) | 5 |
| [split](#split) | 5 |
| [lint](#lint) | 4 |
| [sandbox-v1-2-0](#sandbox-v1-2-0) | 3 |
| [toolkit](#toolkit) | 3 |
| [frictions2](#frictions2) | 2 |
| [friends-v1-2-0-reliability](#friends-v1-2-0-reliability) | 2 |
| [heavy-emma](#heavy-emma) | 2 |
| [heavy-johnny](#heavy-johnny) | 2 |
| [tools-v1-2-0-stranger](#tools-v1-2-0-stranger) | 2 |
| [heavy-alex](#heavy-alex) | 1 |
| [modeltests](#modeltests) | 1 |

### rerate-v1-2-0

- `rerate-alex-swarm` Rate nova-swarm as released in nova-tools v1.2.0: read its help (`nova-swarm help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-freddy-version` Rate nova-version as released in nova-tools v1.2.0: read its help (`nova-version help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-johnny-update` Rate nova-update as released in nova-tools v1.2.0: read its help (`nova-update help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-johnny-check` Rate nova-check as released in nova-tools v1.2.0: read its help (`nova-check help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-johnny-sandbox` Rate nova-sandbox as released in nova-tools v1.2.0: read its help (`nova-sandbox help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-freddy-redis` Rate nova-redis as released in nova-tools v1.2.0: read its help (`nova-redis help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-stella-sandbox` Rate nova-sandbox as released in nova-tools v1.2.0: read its help (`nova-sandbox help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-johnny-table` Rate nova-table as released in nova-tools v1.2.0: read its help (`nova-table help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-stella-self-talk` Rate nova-self-talk as released in nova-tools v1.2.0: read its help (`nova-self-talk help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live 
- `rerate-freddy-cairn` Rate nova-cairn as released in nova-tools v1.2.0: read its help (`nova-cairn help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-zhi-version` Rate nova-version as released in nova-tools v1.2.0: read its help (`nova-version help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-johnny-swarm` Rate nova-swarm as released in nova-tools v1.2.0: read its help (`nova-swarm help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-alex-cairn` Rate nova-cairn as released in nova-tools v1.2.0: read its help (`nova-cairn help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-freddy-self-talk` Rate nova-self-talk as released in nova-tools v1.2.0: read its help (`nova-self-talk help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live 
- `rerate-zhi-table` Rate nova-table as released in nova-tools v1.2.0: read its help (`nova-table help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-self-talk` Rate nova-self-talk as released in nova-tools v1.2.0: read its help (`nova-self-talk help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live 
- `rerate-stella-check` Rate nova-check as released in nova-tools v1.2.0: read its help (`nova-check help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-freddy-tokens` Rate nova-tokens as released in nova-tools v1.2.0: read its help (`nova-tokens help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-stella-swarm` Rate nova-swarm as released in nova-tools v1.2.0: read its help (`nova-swarm help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-swarm` Rate nova-swarm as released in nova-tools v1.2.0: read its help (`nova-swarm help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-memory` Rate nova-memory as released in nova-tools v1.2.0: read its help (`nova-memory help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-johnny-bus` Rate nova-bus as released in nova-tools v1.2.0: read its help (`nova-bus help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never
- `rerate-johnny-tokens` Rate nova-tokens as released in nova-tools v1.2.0: read its help (`nova-tokens help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-alex-fuse` Rate nova-fuse as released in nova-tools v1.2.0: read its help (`nova-fuse help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, nev
- `rerate-emma-check` Rate nova-check as released in nova-tools v1.2.0: read its help (`nova-check help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-zhi-decide` Rate nova-decide as released in nova-tools v1.2.0: read its help (`nova-decide help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-zhi-secrets` Rate nova-secrets as released in nova-tools v1.2.0: read its help (`nova-secrets help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-stella-memory` Rate nova-memory as released in nova-tools v1.2.0: read its help (`nova-memory help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-emma-config` Rate nova-config as released in nova-tools v1.2.0: read its help (`nova-config help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-zhi-sandbox` Rate nova-sandbox as released in nova-tools v1.2.0: read its help (`nova-sandbox help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-johnny-config` Rate nova-config as released in nova-tools v1.2.0: read its help (`nova-config help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-freddy-bus` Rate nova-bus as released in nova-tools v1.2.0: read its help (`nova-bus help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never
- `rerate-stella-version` Rate nova-version as released in nova-tools v1.2.0: read its help (`nova-version help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-alex-check` Rate nova-check as released in nova-tools v1.2.0: read its help (`nova-check help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-stella-bus` Rate nova-bus as released in nova-tools v1.2.0: read its help (`nova-bus help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never
- `rerate-freddy-swarm` Rate nova-swarm as released in nova-tools v1.2.0: read its help (`nova-swarm help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-alex-sandbox` Rate nova-sandbox as released in nova-tools v1.2.0: read its help (`nova-sandbox help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-emma-ci` Rate nova-ci as released in nova-tools v1.2.0: read its help (`nova-ci help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never a
- `rerate-emma-fuse` Rate nova-fuse as released in nova-tools v1.2.0: read its help (`nova-fuse help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, nev
- `rerate-johnny-version` Rate nova-version as released in nova-tools v1.2.0: read its help (`nova-version help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-emma-sandbox` Rate nova-sandbox as released in nova-tools v1.2.0: read its help (`nova-sandbox help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-freddy-secrets` Rate nova-secrets as released in nova-tools v1.2.0: read its help (`nova-secrets help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-stella-decide` Rate nova-decide as released in nova-tools v1.2.0: read its help (`nova-decide help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-zhi-check` Rate nova-check as released in nova-tools v1.2.0: read its help (`nova-check help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-johnny-cairn` Rate nova-cairn as released in nova-tools v1.2.0: read its help (`nova-cairn help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-decide` Rate nova-decide as released in nova-tools v1.2.0: read its help (`nova-decide help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-stella-update` Rate nova-update as released in nova-tools v1.2.0: read its help (`nova-update help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-alex-bus` Rate nova-bus as released in nova-tools v1.2.0: read its help (`nova-bus help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never
- `rerate-alex-table` Rate nova-table as released in nova-tools v1.2.0: read its help (`nova-table help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-zhi-cairn` Rate nova-cairn as released in nova-tools v1.2.0: read its help (`nova-cairn help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-update` Rate nova-update as released in nova-tools v1.2.0: read its help (`nova-update help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-zhi-memory` Rate nova-memory as released in nova-tools v1.2.0: read its help (`nova-memory help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-zhi-config` Rate nova-config as released in nova-tools v1.2.0: read its help (`nova-config help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-alex-secrets` Rate nova-secrets as released in nova-tools v1.2.0: read its help (`nova-secrets help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-stella-table` Rate nova-table as released in nova-tools v1.2.0: read its help (`nova-table help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-zhi-swarm` Rate nova-swarm as released in nova-tools v1.2.0: read its help (`nova-swarm help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-alex-ci` Rate nova-ci as released in nova-tools v1.2.0: read its help (`nova-ci help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never a
- `rerate-alex-decide` Rate nova-decide as released in nova-tools v1.2.0: read its help (`nova-decide help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-stella-ci` Rate nova-ci as released in nova-tools v1.2.0: read its help (`nova-ci help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never a
- `rerate-freddy-check` Rate nova-check as released in nova-tools v1.2.0: read its help (`nova-check help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-johnny-memory` Rate nova-memory as released in nova-tools v1.2.0: read its help (`nova-memory help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-alex-tokens` Rate nova-tokens as released in nova-tools v1.2.0: read its help (`nova-tokens help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-johnny-secrets` Rate nova-secrets as released in nova-tools v1.2.0: read its help (`nova-secrets help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-emma-table` Rate nova-table as released in nova-tools v1.2.0: read its help (`nova-table help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-secrets` Rate nova-secrets as released in nova-tools v1.2.0: read its help (`nova-secrets help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-alex-memory` Rate nova-memory as released in nova-tools v1.2.0: read its help (`nova-memory help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-freddy-update` Rate nova-update as released in nova-tools v1.2.0: read its help (`nova-update help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-johnny-redis` Rate nova-redis as released in nova-tools v1.2.0: read its help (`nova-redis help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-johnny-ci` Rate nova-ci as released in nova-tools v1.2.0: read its help (`nova-ci help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never a
- `rerate-emma-tokens` Rate nova-tokens as released in nova-tools v1.2.0: read its help (`nova-tokens help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-zhi-tokens` Rate nova-tokens as released in nova-tools v1.2.0: read its help (`nova-tokens help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-freddy-table` Rate nova-table as released in nova-tools v1.2.0: read its help (`nova-table help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-alex-self-talk` Rate nova-self-talk as released in nova-tools v1.2.0: read its help (`nova-self-talk help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live 
- `rerate-zhi-ci` Rate nova-ci as released in nova-tools v1.2.0: read its help (`nova-ci help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never a
- `rerate-zhi-redis` Rate nova-redis as released in nova-tools v1.2.0: read its help (`nova-redis help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-freddy-sandbox` Rate nova-sandbox as released in nova-tools v1.2.0: read its help (`nova-sandbox help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-emma-cairn` Rate nova-cairn as released in nova-tools v1.2.0: read its help (`nova-cairn help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-bus` Rate nova-bus as released in nova-tools v1.2.0: read its help (`nova-bus help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never
- `rerate-freddy-decide` Rate nova-decide as released in nova-tools v1.2.0: read its help (`nova-decide help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-stella-config` Rate nova-config as released in nova-tools v1.2.0: read its help (`nova-config help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-freddy-ci` Rate nova-ci as released in nova-tools v1.2.0: read its help (`nova-ci help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never a
- `rerate-freddy-memory` Rate nova-memory as released in nova-tools v1.2.0: read its help (`nova-memory help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-alex-update` Rate nova-update as released in nova-tools v1.2.0: read its help (`nova-update help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-stella-redis` Rate nova-redis as released in nova-tools v1.2.0: read its help (`nova-redis help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-emma-redis` Rate nova-redis as released in nova-tools v1.2.0: read its help (`nova-redis help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-zhi-update` Rate nova-update as released in nova-tools v1.2.0: read its help (`nova-update help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-stella-secrets` Rate nova-secrets as released in nova-tools v1.2.0: read its help (`nova-secrets help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-zhi-bus` Rate nova-bus as released in nova-tools v1.2.0: read its help (`nova-bus help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, never
- `rerate-alex-redis` Rate nova-redis as released in nova-tools v1.2.0: read its help (`nova-redis help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-zhi-fuse` Rate nova-fuse as released in nova-tools v1.2.0: read its help (`nova-fuse help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, nev
- `rerate-johnny-decide` Rate nova-decide as released in nova-tools v1.2.0: read its help (`nova-decide help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-stella-tokens` Rate nova-tokens as released in nova-tools v1.2.0: read its help (`nova-tokens help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-johnny-self-talk` Rate nova-self-talk as released in nova-tools v1.2.0: read its help (`nova-self-talk help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live 
- `rerate-stella-fuse` Rate nova-fuse as released in nova-tools v1.2.0: read its help (`nova-fuse help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, nev
- `rerate-freddy-config` Rate nova-config as released in nova-tools v1.2.0: read its help (`nova-config help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,
- `rerate-zhi-self-talk` Rate nova-self-talk as released in nova-tools v1.2.0: read its help (`nova-self-talk help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live 
- `rerate-emma-version` Rate nova-version as released in nova-tools v1.2.0: read its help (`nova-version help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-johnny-fuse` Rate nova-fuse as released in nova-tools v1.2.0: read its help (`nova-fuse help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, nev
- `rerate-stella-cairn` Rate nova-cairn as released in nova-tools v1.2.0: read its help (`nova-cairn help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, n
- `rerate-freddy-fuse` Rate nova-fuse as released in nova-tools v1.2.0: read its help (`nova-fuse help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store, nev
- `rerate-alex-version` Rate nova-version as released in nova-tools v1.2.0: read its help (`nova-version help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live stor
- `rerate-alex-config` Rate nova-config as released in nova-tools v1.2.0: read its help (`nova-config help` and every verb's -h) and its spec cold, then use it for real on a throwaway store or directory (never a live store,

### shrink

- `shrink-pkg-ci-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-memory-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order
- `shrink-pkg-ci-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-redis-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `shrink-pkg-bus-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-bus-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-12-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-tokens-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-secrets-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `shrink-pkg-redisconn-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order
- `shrink-pkg-bus-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-tools-ghrelease-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-13-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-tool-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-ci-10-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-typedrec-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-cairn-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-testredis-t` A tree of 7 work steps (STEP 2 to STEP 8), walked in order
- `shrink-bus-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-14-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-fleet-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-8-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-7-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-9-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-fuse-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `shrink-pkg-check-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `shrink-pkg-update-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-5-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-tokens-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-redisfn-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order
- `shrink-testify-asserts-t` 1 script step (STEP 2 to STEP 2) in order; each commits its PATHS with its COMMIT: line, and its verdict line is `step <n>: <ok\|broken> <the full 40-hex commit sha pasted from git rev-parse HEAD, or 
- `shrink-bus-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-converge-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-release-t` A tree of 6 work steps (STEP 2 to STEP 7), walked in order
- `shrink-pkg-selftalk-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-tools-ci-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `shrink-tools-functionalrun-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-secrets-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `shrink-pkg-bus-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-update-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-config-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `shrink-pkg-update-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-tokens-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-config-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order
- `shrink-pkg-ci-11-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-check-t` A tree of 6 work steps (STEP 2 to STEP 7), walked in order
- `shrink-pkg-fuse-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-dogfood-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-record-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-bus-5-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-6-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-tokens-4-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-update-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-bus-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-ci-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-docs-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order
- `shrink-pkg-hygiene-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-pkg-tokens-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-bus-6-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-memindex-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-ci-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `shrink-pkg-bus-5-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-pkg-bus-6-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `shrink-self-talk-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `shrink-bus-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order

### use

- `use-bus-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-bus-4-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `use-tokens-4-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `use-dev-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-tokens-6-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-bus-5-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-secrets-5-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `use-cairn-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order
- `use-version-t` A tree of 6 work steps (STEP 2 to STEP 7), walked in order
- `use-self-talk-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `use-check-2-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `use-bus-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-secrets-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-tokens-5-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `use-update-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-release-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-check-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-dev-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-work-2-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `use-ci-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-config-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-bus-6-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-check-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-secrets-3-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `use-config-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-work-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-update-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-fuse-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `use-memory-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-config-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-update-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-bus-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-update-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-memory-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-ci-3-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `use-ci-4-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `use-redis-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `use-bus-7-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `use-tokens-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-tokens-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-config-4-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `use-dev-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-check-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-work-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-ci-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-memory-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-memory-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-secrets-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-secrets-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `use-tokens-3-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order

### debt

- `fix-dead-nsprint-storebb` The dead code ledger internal/ci/testdata/dead_code_allowlist.txt (held by TestDeadCode in internal/ci/dead_code_class_test.go, functional tier; "Shrink-only": a row is `<package> <count>` of function
- `fix-namedpaths-ci-examplesb` The named-paths ledger internal/ci/testdata/namedpaths_allowlist.txt (internal/ci/namedpaths_class_test.go, TestEveryNamedRepoPathExists: a row is a name in the tree that looks like a path into this r
- `fix-waits-nova-swarm-slowb` The fixed-waits ledger internal/ci/testdata/fixed-waits-allowlist.txt (internal/ci/ci_waits.go, class test TestNoFixedWaitsOnTheCIPath, docs/SPEC-CI.md "waits": a row is file:line kind date reason; ki
- `fix-namedpaths-tokens-messageb` The named-paths ledger internal/ci/testdata/namedpaths_allowlist.txt (internal/ci/namedpaths_class_test.go, TestEveryNamedRepoPathExists: a row is a name in the tree that looks like a path into this r
- `fix-transcript-nova-secretsb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-general-internal-swarm-lintheader-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-wall-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-serial-nova-fuseb` The serial-tests ledger internal/ci/testdata/serial-tests_allowlist.txt (internal/ci/parallel_class_test.go, TestEveryTestOpensWithTParallel: Go tests always run in parallel; a row is a test that does
- `fix-waits-update-escaped_pipe_returns_within_budget_unix_functionalb` The fixed-waits ledger internal/ci/testdata/fixed-waits-allowlist.txt (internal/ci/ci_waits.go, class test TestNoFixedWaitsOnTheCIPath, docs/SPEC-CI.md "waits": a row is file:line kind date reason; ki
- `fix-general-internal-swarm-slotweight-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-providererr-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-lintdepends-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-signature-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-transcript-nova-self-talkb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-general-internal-swarm-stage-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-lintbase-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-worker-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-pathcase-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-docs-spec-ci-mdb` The generality ledger shard internal/ci/testdata/generality-text/docs.txt (internal/ci/generality_text_class_test.go, TestGeneralityText; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tailnet,
- `fix-serial-nova-tokensb` The serial-tests ledger internal/ci/testdata/serial-tests_allowlist.txt (internal/ci/parallel_class_test.go, TestEveryTestOpensWithTParallel: Go tests always run in parallel; a row is a test that does
- `fix-general-internal-swarm-inputlimit-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-serial-swarmb` The serial-tests ledger internal/ci/testdata/serial-tests_allowlist.txt (internal/ci/parallel_class_test.go, TestEveryTestOpensWithTParallel: Go tests always run in parallel; a row is a test that does
- `fix-slowtests-updateb` The unit-tier slow-test allowlist internal/ci/slow-tests_allowlist.txt (make test runs `nova-ci slowtests --package-budget 2 --test-budget 1 --allowlist` with it: a top-level test over 1 s or a packag
- `fix-transcript-nova-tokensb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-general-internal-swarm-providerread-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-lintcontract-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-docs-tests-mdb` The generality ledger shard internal/ci/testdata/generality-text/docs.txt (internal/ci/generality_text_class_test.go, TestGeneralityText; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tailnet,
- `fix-transcript-nova-versionb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-general-internal-swarm-providerproxy-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-transcript-nova-updateb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-serial-testbinb` The serial-tests ledger internal/ci/testdata/serial-tests_allowlist.txt (internal/ci/parallel_class_test.go, TestEveryTestOpensWithTParallel: Go tests always run in parallel; a row is a test that does
- `fix-namedpaths-pulseb` The named-paths ledger internal/ci/testdata/namedpaths_allowlist.txt (internal/ci/namedpaths_class_test.go, TestEveryNamedRepoPathExists: a row is a name in the tree that looks like a path into this r
- `fix-transcript-nova-swarmb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-transcript-nova-fuseb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-serial-secretsb` The serial-tests ledger internal/ci/testdata/serial-tests_allowlist.txt (internal/ci/parallel_class_test.go, TestEveryTestOpensWithTParallel: Go tests always run in parallel; a row is a test that does
- `fix-namedpaths-decide-entryb` The named-paths ledger internal/ci/testdata/namedpaths_allowlist.txt (internal/ci/namedpaths_class_test.go, TestEveryNamedRepoPathExists: a row is a name in the tree that looks like a path into this r
- `fix-serial-nsprint-storeb` The serial-tests ledger internal/ci/testdata/serial-tests_allowlist.txt (internal/ci/parallel_class_test.go, TestEveryTestOpensWithTParallel: Go tests always run in parallel; a row is a test that does
- `fix-general-internal-swarm-lease-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-general-internal-swarm-slots-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-transcript-nova-sandboxb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-general-internal-swarm-staging-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-transcript-nova-memoryb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-general-internal-tokens-provider-gob` The generality ledger shard internal/ci/testdata/generality/internal/tokens.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, ta
- `fix-transcript-nova-redisb` The transcripts ledger internal/ci/testdata/transcripts_allowlist.txt (internal/ci/transcripts_class_test.go, TestEveryTranscriptIsExecutedLineForLine, docs/SPEC-TOOLWORK.md rule 3: a row is a `## <to
- `fix-general-internal-swarm-reap-gob` The generality ledger shard internal/ci/testdata/generality/internal/swarm.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, tai
- `fix-prmerge-secrets-sealb` The forge-merge-spelling ledger internal/ci/testdata/prmerge_allowlist.txt (internal/ci/prmerge_class_test.go, TestNoGhPrMergeSpellingInTheToolsGo; Glenn 2026-09-18: nothing reaches the dev merge queu
- `fix-serial-nova-sandboxb` The serial-tests ledger internal/ci/testdata/serial-tests_allowlist.txt (internal/ci/parallel_class_test.go, TestEveryTestOpensWithTParallel: Go tests always run in parallel; a row is a test that does

### prose

- `prose-redis-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-work-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-version-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-update-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-ci-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-release-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-cairn-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-release-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-config-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-redis-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-converge-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-dev-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-secrets-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-tokens-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-tokens-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-selftalk-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-memory-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-update-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-work-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-check-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-ci-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-fuse-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-secrets-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-check-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-self-talk-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-config-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-bus-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-self-talk-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-fuse-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-bus-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `prose-memory-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `prose-cairn-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order

### harness

- `harness-pkg-config-secrets-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-memindex-pkg-hygiene-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-release-check-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-tokens-tools-functionalrun-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-secrets-tools-ci-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-selftalk-pkg-tool-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-update-pkg-testredis-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-fleet-pkg-typedrec-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-fuse-pkg-converge-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-ci-tools-ghrelease-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-cairn-pkg-record-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-bus-tokens-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-redis-fuse-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-check-pkg-redisconn-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-config-pkg-redisfn-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-self-talk-pkg-dogfood-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-ci-bus-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `harness-pkg-docs-memory-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order

### reference

- `reference-agents-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `reference-cli-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-7-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-5-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-spec-ci-spec-check-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-readme-usage-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-readme-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-6-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-spec-work-v1-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `reference-cli-spec-update-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-spec-bus-spec-config-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-4-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-readme-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-9-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-spec-tokens-spec-secrets-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `reference-cli-8-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order

### fleetnames

- `fleetnames-bus-pkg-typedrec-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-tokens-pkg-fleet-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-tokens-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `fleetnames-pkg-selftalk-pkg-ci-timing-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-redisconn-pkg-oneline-audit-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-oneline-pkg-fuse-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-spec-bus-reply-changelog-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-check-pkg-memindex-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-update-pkg-textbody-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-check-tools-ghrelease-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-testing-spec-release-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-worklang-pkg-secrets-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-spec-swarm-models-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-hygiene-pkg-dogfood-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-release-pkg-bus-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-ci-pkg-cardhdr-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `fleetnames-pkg-config-pkg-ci-allowlist-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order

### read

- `read-cairn-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order
- `read-check-t` A tree of 5 work steps (STEP 2 to STEP 6), walked in order
- `read-bus-4-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-check-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-bus-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `read-bus-3-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `read-update-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `read-self-talk-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-ci-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-redis-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-tokens-t` A tree of 6 work steps (STEP 2 to STEP 7), walked in order
- `read-update-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-bus-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `read-config-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-tokens-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `read-memory-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `read-fuse-t` A tree of 4 work steps (STEP 2 to STEP 5), walked in order

### friends-general-v1-2-0

- `fg-session-broken-on-provider-refusal` A broken session is noticed, said once, and not fed
- `fg-friend-peers-verb` A verb any tool reads to learn who is up: `nova-friend peers`, over the presence records of docs/SPEC-FRIEND.md "Presence" (landed by fg-presence-off-the-sprint-server; read that section first and use
- `fg-friend-check-verb` The friend health check as a verb
- `fg-presence-off-the-sprint-server` fg-presence-off-the-sprint-server
- `fg-friend-renew-verbb` A broken session gets a fresh one in the same harness, as a verb
- `fg-friend-screen-verb` The coordinator sees a friend's screen without a person
- `fg-bus-store-without-the-sprint` nova-bus and nova-friend still lean on the sprint for their store
- `fg-tmux-host-and-adapter` One uniform push into the OPEN chat of any terminal harness (OpenCode, Grok, Aider, any TUI) when it is hosted in tmux: the friend's session is the TUI in the pane, the daemon types into it exactly as
- `friend-tla-delivery-states` Extend tla/Friend.tla (your liveness model) with the new delivery states that PR 5317 (rowan/friend-e2e, head 0e0212ad8, carried by this card's BASE rowan/integration-2026-10-04) built in internal/fri
- `fg-claude-open-chatb` The Claude Code route into the open session, measured, and the hand watch script retired
- `fg-friend-watch-verb` The coordinator's wake as a nova-friend verb
- `fg-adopt-friend-daemons` Adopt what the friends-general cards built, so the fleet's friend comms run on nova-tools alone, and retire the hand work of 2026-10-04
- `fg-friend-reach-verbb` The escalation ladder that gets a silent friend's attention, as a verb
- `fg-cold-setup-acceptanceb` fg-cold-setup-acceptanceb
- `fg-delivery-pending-as-one-turn` A message must not wait behind every older one

### tenv

- `tenv-pkg-scaffold-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-tokens-2-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-pkg-update-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `tenv-pkg-update-3-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `tenv-pkg-testbin-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-pkg-secrets-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-update-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-pkg-release-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-pkg-nsprint-testutil-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-pkg-update-2-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `tenv-pkg-nsprint-store-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-pkg-check-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-version-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `tenv-pkg-ci-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order

### tools-v1-2-0-setup

- `dep-tailnet` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-ssh` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-providers` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-go-sdk` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `setup-nova-doctor` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-harnesses` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-redis-stores` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-jev` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `setup-nova-up-fleet` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-secrets` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-launchd-units` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)
- `dep-postgres` nova-tools v1.2.0, lens setup (Glenn 2026-10-04: a stranger sets the whole thing up from the docs and the tools, with nothing hidden; nothing the coordinator needs is a Rowan-only tool)

### classes

- `classes-spec-ci-10-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-9-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-12-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-8-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-4-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-5-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-11-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-unit-sockets-t-m1` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-6-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-3-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `classes-spec-ci-7-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order

### security2

- `fp-sec74-f8` ReadBox decodes with json.Unmarshal into a *Box (internal/fuse/fuse.go:203-215, type Box :99-102): unknown keys are ignored, key matching is case-insensitive and a duplicated key is last-wins, and a w
- `fp-sec74-f9` The unreadable-box refusal of cmdQuarantine prints lockdown --box %s "<reason>" inside a backtick span using oneline.Escape(box) (cmd/nova-fuse/main.go:649), which stops control characters but not she
- `fp-sec74-f3` cmdQuarantine (cmd/nova-fuse/main.go:617-680), cmdLockdown (:542-615) and liftQuarantine (:369) are unlocked read-modify-write cycles (ReadBox then WriteBox), and internal/fuse/fuse.go note 3 admits t
- `fp-sec77-f5` extractLinkTargets (internal/check/links.go:256-274) calls matchBrackets (links.go:280-294) at every '[' on a line, and matchBrackets scans forward to the end of the line when the brackets never close
- `fp-sec67-f1` On darwin a symlink spelling of a granted path is unreadable inside the wall (security#67 finding 1)
- `fp-sec73-f4b` internal/cairn implements a state machine (an entry is absent, then published, then duplicate or conflict on re-read; two store shapes: nested entries/<session>/<id>.json published through atomicfile,
- `fp-sec78-f5` The member-prefix and epoch-key reservations miss the view family (security#78 finding 5)
- `sec-gate-names-the-check-not-the-rule` Model: tla/SecretsSeat.tla on sprint/md-secrets-h.w1.g1.e15; cite it from the code change
- `fp-sec76-f3` Nothing bounds the corpus read
- `sec-gate-marks-verb-made-files` Model: tla/SecretsSeat.tla on sprint/md-secrets-h.w1.g1.e15; cite it from the code change

### busdogfood2

- `fp-bus2-address-row-in-config` Dogfood finding: --redis has no default on a host where the plist knows the address but the shell does not, so NOVA_BUS_REDIS must be exported on every call; and the address should live in nova-config
- `fp-bus2-f3-send-ok-bytes-digestb` Stella's F3: the SEND OK line carries no payload byte count or digest, so verifying a --file send needs the receiver
- `fp-bus2-recv-says-oldest-of-n` Dogfood finding: 'nova-bus2 recv' without --exec delivers the oldest unacked message, not the newest (expected), but nothing says more are behind it
- `fp-bus2-f1-help-address-orderf` Stella's F1: the top-level help of nova-bus2 omits NOVA_SPRINT_REDIS from the Redis address precedence that 'nova-bus2 help send' includes, so the two disagree
- `fp-bus2-ack-says-why` Dogfood finding: 'nova-bus2 ack <id>' after only a 'peek' prints acked=false with no reason; a message must have been recv'd by you first
- `fp-bus2-send-default-as` Dogfood finding: 'nova-bus2 send' without --as refuses with 'refusing to guess' on a connection with no login user, so every call from a friend's own shell needs the flag
- `fp-bus2-f4-empty-recv-is-none-m1` Stella's F4: an empty 'recv --block 30s --json' prints status=failed at exit 1 with the word NONE; exit 1 is 'ran and said no', so the status must be none (a result, not a failure), as the human line'
- `fp-bus2-redis-address-guard` Glenn's rule: the tailnet is the boundary, no ACLs; the bus Redis binds 127.0.0.1 and the Studio's 100.x tailnet address and a member reaches it there

### contract

- `contract-version-work-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `contract-bus-cairn-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `contract-config-fuse-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `contract-secrets-self-talk-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `contract-check-ci-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `contract-memory-redis-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `contract-tokens-update-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `contract-dev-release-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order

### dead

- `dead-pkg-swarm-swarm-t2b` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `dead-pkg-fleet-pkg-nogh-t2b` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `dead-pkg-nsprint-fn-pkg-nsprint-store-t2b` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `dead-pkg-pkgselect-pkg-ghevent-t2b` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `dead-pkg-swarm-t2b` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `dead-pkg-workfile-pkg-workgh-t2b` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `dead-pkg-swarm-2-t2b` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `dead-pkg-bounded-pkg-buildinfo-t2b` A tree of 2 work steps (STEP 2 to STEP 3), walked in order

### ttime

- `ttime-pkg-bus-4-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `ttime-pkg-bus-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `ttime-pkg-update-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `ttime-pkg-bus-3-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `ttime-pkg-bus-2-tb` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `ttime-tokens-tb` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `ttime-bus-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `ttime-pkg-secrets-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order

### tla

- `tla-secrets` The TLA+ model of `internal/secrets`, beside the module that owns the state (the owner, 2026-09-27: TLA+ for every state machine, in every project; 2026-10-02: models for bus, update, cairn, secrets, 
- `tla-cairn` The TLA+ model of `internal/cairn`, beside the module that owns the state (the owner, 2026-09-27: TLA+ for every state machine, in every project; 2026-10-02: models for bus, update, cairn, secrets, co
- `tla-decide-record` The TLA+ model of `internal/decide`, beside the module that owns the state (the owner, 2026-09-27: TLA+ for every state machine, in every project; 2026-10-02: models for bus, update, cairn, secrets, c
- `tla-config` The TLA+ model of `internal/config`, beside the module that owns the state (the owner, 2026-09-27: TLA+ for every state machine, in every project; 2026-10-02: models for bus, update, cairn, secrets, c
- `tla-bus` The TLA+ model of `internal/bus`, beside the module that owns the state (the owner, 2026-09-27: TLA+ for every state machine, in every project; 2026-10-02: models for bus, update, cairn, secrets, conf
- `tla-update` The TLA+ model of `internal/update`, beside the module that owns the state (the owner, 2026-09-27: TLA+ for every state machine, in every project; 2026-10-02: models for bus, update, cairn, secrets, c
- `tla-cardtree-walk` The TLA+ model of `internal/cardtree`, beside the module that owns the state (the owner, 2026-09-27: TLA+ for every state machine, in every project; 2026-10-02: models for bus, update, cairn, secrets,

### tools-v1-2-0-docs

- `tdocs-docs-ci` nova-tools v1.2.0, lens docs (Glenn 2026-10-04: the whole documentation treatment; a stranger, human or AI, can use every tool from its docs alone)
- `tdocs-cold-read-rerates` nova-tools v1.2.0, lens docs (Glenn 2026-10-04: the whole documentation treatment; a stranger, human or AI, can use every tool from its docs alone)
- `tdocs-glossary-terminology-lint` nova-tools v1.2.0, lens docs (Glenn 2026-10-04: the whole documentation treatment; a stranger, human or AI, can use every tool from its docs alone)
- `tdocs-friend-onboarding` nova-tools v1.2.0, lens docs (Glenn 2026-10-04: the whole documentation treatment; a stranger, human or AI, can use every tool from its docs alone)
- `tdocs-concepts-architecture` nova-tools v1.2.0, lens docs (Glenn 2026-10-04: the whole documentation treatment; a stranger, human or AI, can use every tool from its docs alone)
- `tdocs-tool-readmes` nova-tools v1.2.0, lens docs (Glenn 2026-10-04: the whole documentation treatment; a stranger, human or AI, can use every tool from its docs alone)
- `tdocs-cli-generated` nova-tools v1.2.0, lens docs (Glenn 2026-10-04: the whole documentation treatment; a stranger, human or AI, can use every tool from its docs alone)

### machinery2

- `fp-mach-03b-card-sweep-cut-by-site` A sweep over one tool (a ledger row or a finding that names a whole tool or package) was cut into one card, which no child finishes inside its deadline (the night of 2026-10-03: sweep cards ran out of
- `fp-mach-03c-card-usage-banner-duplicatec` nova-card generate -h prints the same example line twice: the usage block of 'nova-card help' lists '  nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/re
- `fp-mach-03d-card-sleeps-skips-paths-narrow` A sleeps-skips ledger card is a mechanical edit of one test file (it removes a sleep or a skip from the tests the row names), but nova-card gives it the row's whole package directory as PATHS, so two 
- `fp-mach-03a-card-ledger-plan-one-wave` nova-card plans cards of one ordinary ledger in alternating waves (odd rows wave 1, even rows wave 2 depending on their neighbours) because adjacent deletions conflicted at land
- `fp-mach-08-generality-nova-bus-rows-stale` The generality ledger internal/ci/testdata/generality/cmd/nova-bus.txt lists exceptions (rows naming a host, a path or a person in cmd/nova-bus sources) that went stale when commit eeb04f95 changed th

### split

- `split-bus-2-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `split-pkg-update-2-t` A tree of 3 work steps (STEP 2 to STEP 4), walked in order
- `split-fuse-t` 1 script step (STEP 2 to STEP 2) in order; each commits its PATHS with its COMMIT: line, and its verdict line is `step <n>: <ok\|broken> <the full 40-hex commit sha pasted from git rev-parse HEAD, or 
- `split-bus-t` 1 script step (STEP 2 to STEP 2) in order; each commits its PATHS with its COMMIT: line, and its verdict line is `step <n>: <ok\|broken> <the full 40-hex commit sha pasted from git rev-parse HEAD, or 
- `split-fuse-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order

### lint

- `lint-tokens-2-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `lint-tools-benchstandard-2-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `lint-tools-functionalrun-2-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order
- `lint-tools-ci-2-tb` A tree of 1 work step (STEP 2 to STEP 2), walked in order

### sandbox-v1-2-0

- `sbx-fixture-guard` Layer 3 of the container-sandboxed functional tier, mas-bandwidth/ideas#826 (https://github.com/mas-bandwidth/ideas/issues/826)
- `sbx-ci-legs` Layer 4 of the container-sandboxed functional tier, mas-bandwidth/ideas#826 (https://github.com/mas-bandwidth/ideas/issues/826)
- `sbx-runner-verb` Design card, heavy, layer 2 of the container-sandboxed functional tier (mas-bandwidth/ideas#826, https://github.com/mas-bandwidth/ideas/issues/826)

### toolkit

- `toolkit-pkg-tool-7-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `toolkit-pkg-testkit-2-t` A tree of 2 work steps (STEP 2 to STEP 3), walked in order
- `toolkit-pkg-testkit-t2b` A tree of 1 work step (STEP 2), the test clock

### frictions2

- `fp-fric-alex1-gocache-line-staleb` Alex measured that the GOCACHE sentence in every generated card is stale: cards say a cache path or tell the child to set its own while JOB.md says GOCACHE is already the machine's shared build cache 
- `fp-fric-alex4-deadline-judgment-is-coordinators` Every card says 'Deadline: finish within N minutes.' and a child that is running out reads it as a command to push whatever it has

### friends-v1-2-0-reliability

- `session-recovery` nova-tools v1.2.0, stream friends-v1-2-0-reliability (general, never sprint-specific)
- `delivery-conformance` nova-tools v1.2.0, stream friends-v1-2-0-reliability (general, never sprint-specific)

### heavy-emma

- `sk-tokens-h` Move nova-tokens onto the shared skeleton `internal/tool` and delete its private copy, same behaviour, every existing test green (moved where the lines moved, never deleted)
- `fix-general-tools-ghrelease-ldflags-go-h` The generality ledger shard internal/ci/testdata/generality/tools/ghrelease.txt (internal/ci/generality_class_test.go, TestGeneralityGuardrail; docs/SPEC-CI.md#generality, Rule 1: no host, machine, ta

### heavy-johnny

- `sk-update-h` Move nova-update onto the shared skeleton `internal/tool` and delete its private copy, same behaviour, every existing test green (moved where the lines moved, never deleted)
- `gen-docs-h` Fleet specifics left in the two documents the tools print and in one fixture, outside what the wave-1 docs cards cover (they strip diary from docs/CLI.md lines 58-211 and docs/TESTS.md lines 24-201 at

### tools-v1-2-0-stranger

- `stranger-friend-fake-harness` nova-tools v1.2.0, lens stranger: a real cold run by a bud from the README and help alone
- `stranger-three-card-sprint` nova-tools v1.2.0 and nova-sprint v1.0.0, lens stranger: a real cold run by a bud from the README and help alone

### heavy-alex

- `st-release-h` One status grammar for nova-update release (internal/release): the standard's three words after the verb (`OK`, `REFUSED`, `FAILED`; docs/STANDARD.md section 2, AGENTS.md:71), one cap flag (`--max` wi

### modeltests

- `modeltests-pkg-bus-t` A tree of 1 work step (STEP 2 to STEP 2), walked in order

