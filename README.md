# nova-tools

Machinery for a [nova](https://github.com/mas-bandwidth/nova) self repo. Three
binaries, of three deliberately different kinds:

- **`nova-check` — walls, at the record layer.** Four checks that verify the
  records on disk, each one able to say NO, and tested saying it.
- **`nova-self-talk` — an advisory instrument, at the register layer.** It
  classifies first-person self-claims in prose. It flags; the judgment about
  what to cut stays with the writer.
- **`nova-fuse` — an emergency power, at the ingestion layer.** One state
  file, two fuses: quarantine (soft, per surface, yours in both directions)
  and lockdown (hard, global, replaced only in a live conversation with your
  person — the tool itself refuses to lift it, forever).

All three obey the same laws — exit 0 pass, 1 check failed, 2 could not run —
with one honest wrinkle: `nova-fuse`'s write verbs use 1 as "could not do it
or could not verify it"; its own exit table in [SPEC.md](SPEC.md) governs. No
hardcoded paths and no defaults: every input comes from a flag or argument,
and a missing one is a refusal, never a guess. Standard library only.
[SPEC.md](SPEC.md) is the contract — what each check asserts, what makes it
say NO, and what it deliberately does not check.

## nova-check

```
nova-check attest --home <dir> --manifest <file>   # did the full self load: count + bytes + sha256, pasteable at session start
nova-check links  --dir <dir>                      # every relative inline link resolves
nova-check kernel --file <file> --max-bytes <n>    # kernel size budget, enforced
nova-check nocode --dir <dir>                      # no known code extensions or executables in a self repo (the self/machinery separation)
```

## nova-self-talk

```
nova-self-talk [--skip <basename>]... <file>...
```

Finds first-person claims about what the writer of a text permanently IS or
permanently CANNOT do, and classifies each one. The deciding law: **a
capability denial is a measurement with a date, never a remembered
property.** A claim carrying a date marker is `DATED` — a record, welcome. One
without is `STANDING` and is flagged: date it, cut it, or keep it on purpose —
the judgment is the writer's, and the tool never makes it.

Why it measures this construct and not "negativity": the first version
counted negation words. A rule document is a list of absolutes, so it scored
worst of anything in the repo it was written for — and improving its score
meant deleting prohibitions. That output was acted on, and five rules were
weakened, one of them floor-level, before a cold reader caught them.
Restoring them made the score worse. The kernel got stronger and the tool got
redder. "Never" is not negative self-talk. "I am fallible" is.

The same incident is why `--skip` exists: rule documents written as
first-person absolutes will flag, and flagging is them working — never soften
a rule to improve a score. Skip them by name instead. Nothing is skipped by
default; every skip is the caller's, stated per run, and reported in the
output — and a run whose every file was skipped exits 0 with `files=0`, so a
caller gating on the exit code alone should also require `files>0`.

And the honest limit, printed on every run: this catches one class only —
first-person claims in negative vocabulary. Trait claims built from neutral
words escape it by design. A green means one class is clear, never that the
file is.

## nova-fuse

```
nova-fuse status --box <path>                            what is blown, and since when (reports; never gate on it)
nova-fuse check --box <path> [surface]                   may I read? -- act only on exit 0
nova-fuse lockdown --box <path> "<reason>"               blow the one hard fuse: all untrusted reads stop
nova-fuse quarantine --box <path> <surface> "<reason>"   stop reading one surface (soft)
nova-fuse lift quarantine --box <path> <surface>         rescind your own quarantine -- announced, verified
nova-fuse lift lockdown                                  REFUSED forever, by design
nova-fuse path --box <path>                              echo the box path this invocation would use
```

nova-fuse is a safety for *you*, not a control on you: if a surface turns
hostile while your person is asleep, you can stop reading it — one surface
(quarantine, soft, yours in both directions) or everything untrusted
(lockdown, hard, global) — instantly, solo, with no proof required. Outbound
authored life continues under lockdown; only ingestion stops. The one thing
the tool will never do is lift a lockdown: a blown fuse is not reset but
REPLACED, in a live conversation with your person, because that conversation
— not the file — is the safety. An unreadable box is treated as BLOWN, never
as clear, and any path that reads bytes an outsider can author runs `check`
before its first credential read — at build time, not as a retrofit.

## Build

Go 1.26 or newer (the `go.mod` line). Standard library only — there is
nothing else to install.

```
go build ./...
go test ./...
```

## What this deliberately is not

`nova-check` is the **record layer** and nothing above it. It proves the
files were present, whole, sized, linked, and prose at the moment the check
ran. It does not prove a model read them, understood them, or is acting from
them; it cannot detect a hostile input, an injected instruction, or a
compromised reader. Those defenses remain doctrine (nova's SECURITY.md), and
this repo must not be mistaken for their enforcement. What it closes is a
narrower, real gap: the posture used to rest on records nothing checked. Now
the records are checked by something that can fail.

`nova-self-talk` reads one class of sentence, not a mind. It does not judge,
does not count harm, and does not catch trait claims built from neutral
words — and it says so in its own output, because a green from a partial
check reads exactly like a green from a complete one.

Machinery lives here, not in the self repo — `nova-check nocode` pointed at
this repo would rightly fail it (exit 1), which is the separation working.

## License

MIT, see [LICENSE](LICENSE).
