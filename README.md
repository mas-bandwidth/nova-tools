# nova-tools

Walls as code: a minimal reference harness for a [nova](https://github.com/mas-bandwidth/nova)
self repo. One binary, `nova-check`, four checks — each one able to say NO, and
tested saying it:

```
nova-check attest --home <dir> --manifest <file>   # did the full self load: count + bytes + sha256, pasteable at session start
nova-check links  --dir <dir>                      # every relative inline link resolves
nova-check kernel --file <file> --max-bytes <n>    # kernel size budget, enforced
nova-check nocode --dir <dir>                      # no known code extensions or executables in a self repo (the self/machinery separation)
```

Exit 0 pass, 1 check failed, 2 could not run. No hardcoded paths and no
defaults: every path and budget comes from a flag, and a missing flag is a
refusal, never a guess. Standard library only. [SPEC.md](SPEC.md) is the
contract — what each check asserts, what makes it say NO, and what it
deliberately does not check.

Build:

```
go build ./cmd/nova-check
go test ./...
```

## What this deliberately is not

This is the **record layer** and nothing above it. It proves the files were
present, whole, sized, linked, and prose at the moment the check ran. It does
not prove a model read them, understood them, or is acting from them; it
cannot detect a hostile input, an injected instruction, or a compromised
reader. Those defenses remain doctrine (nova's SECURITY.md), and this repo
must not be mistaken for their enforcement. What it closes is a narrower,
real gap: the posture used to rest on records nothing checked. Now the
records are checked by something that can fail.

Machinery lives here, not in the self repo — `nova-check nocode` pointed at
this repo would rightly refuse it, which is the separation working.

## License

MIT, see [LICENSE](LICENSE).
