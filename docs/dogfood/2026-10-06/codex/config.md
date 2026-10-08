# nova-config dogfood, 2026-10-06 (codex)

I read `nova-config -h`, `nova-config help`, the help for every verb and kind, and
`docs/nova-config/README.md`, then used the branch-built Linux binary against a
local JSON scratch store. I exercised migrate/print, status, machine, friend,
sprint, fleet, loop, route, tier, kinds, inventory, and apply; writes and removals
used only the scratch file, and refusal paths included duplicate/invalid rows,
missing/bad inventory fixtures, disabled routes, and unsupported singleton
operations. No live service or configuration was touched.

## 1. Setting a field to its current value records a change with no field delta — NEXT

**Command:**

    nova-config machine set m1 --width 4 --as codex --file config.json

after `m1` already had width 4.

**Printed:**

    CONFIG SET kind=machine name=m1 rev=16 changed=width

The following `machine history m1 --file config.json` entry has `op=set` but
no field transitions.

**Expected:** A same-value set should be a no-op (no new revision), or say that
the supplied value already matches. As printed, `changed=width` and the new
revision imply a change that the history cannot show.

**Grade:** NEXT

## 2. The fleet guide omits the supported `loops_dir` setting — NEXT

**Command:**

    nova-config fleet set --loops_dir ./loops --as codex --file config.json
    nova-config fleet show --file config.json

**Printed:**

    CONFIG SET kind=fleet name=fleet rev=8 changed=loops_dir
    FLEET name=fleet store=m1 coordinator=m1 redis_port=6379 pg_dsn=postgres://scratch@127.0.0.1:5432/scratch bus=127.0.0.1:6381 loops_dir=./loops created=2023-11-14T22:13:20Z updated=2026-10-07T16:25:12Z

**Expected:** The fleet section should explain `--loops_dir` and its effect.
The binary help and `nova-config kinds` expose it, but the README's fleet
description and examples omit it, so a reader of the tool page cannot discover
how to configure the loops directory.

**Grade:** NEXT

## 3. The friend guide omits supported `config_dir` and `token_cap` settings — NEXT

**Command:**

    nova-config friend set f1 --config_dir /tmp/nova-config-dogfood --token_cap 1000 --as codex --file config.json
    nova-config friend show f1 --file config.json

**Printed:**

    CONFIG SET kind=friend name=f1 rev=9 changed=config_dir,token_cap
    FRIEND name=f1 slots=4 tiers=flash,pro roles=builder width=4 mode=one-shot config_dir=/tmp/nova-config-dogfood token_cap=1000 created=2026-10-07T16:25:12Z updated=2026-10-07T16:25:12Z

**Expected:** The friend section should describe these two supported fields and
their flags. Both appear in the verb help and kind listing and are persisted by
the scratch store, but neither appears in the README's friend explanation.

**Grade:** NEXT

## 4. The inventory guide does not show how to create a local example fixture — NEXT

**Command:**

    nova-config inventory --example > example.yml

**Printed:**

    machines:
      m1: {user: nova, seat: m1, slots: 1}
    fleet: {store: m1, coordinator: m1, redis_port: 6380, pg_dsn: postgres://nova@localhost:5432/nova, loops_dir: "~/nova-bench/loops"}

`nova-config inventory --fixture example.yml --list` then read this generated
fixture successfully. The verb help documents `--example`, while the README only
shows a checked-in fixture path and the `--fixture` reader.

**Expected:** Include the `--example` redirect as the local starting point for
trying inventory without a live Redis view.

**Grade:** NEXT

READ 7/10 — the help covers every verb and its flags, but the page does not explain several supported fields or the local inventory example flow.

USE 8/10 — scratch-file operations and refusals were actionable; the same-value set receipt is misleading and useful options are missing from the page.

urgent=0 next=4
