# nova-card: the card-runner image

One container image, one job: run a card's work in a throwaway sandbox — build
and test the Go tools, run the Lisp kernel, drive the opencode harness — over a
workspace bind-mounted from the host.

## What it carries

| | |
|---|---|
| base | `docker.io/library/ubuntu:24.04` |
| apt | `git`, `ca-certificates`, `curl`, `build-essential` |
| Go | 1.26.5 at `/usr/local/go`, from go.dev, sha256 verified in the Containerfile, symlinked into `/usr/local/bin` |
| SBCL | 2.5.8 at `/usr/local/lib/sbcl`, official x86-64 Linux binary tarball, sha256 verified in the Containerfile |
| opencode | harness 1.18.20 at `/usr/local/bin/opencode`, copied in at build time from the bench, sha256 pinned and verified in the Containerfile right after the copy |
| user | `card`, uid/gid 10001, `HOME=/home/card` |
| workdir | `/work`, a volume, bind-mounted at run time |

What it does **not** carry: no API keys, no tokens, no SSH material, no git
identity, no `nova-secrets` store, no host config of any kind. Verified on a
built image — the only `.pem` files present are the public CA roots, and
`/etc/gitconfig` holds nothing but `safe.directory`.

## Building

The harness binary is not in git (a 176 MB vendor executable with no provenance
a reader of this repo could check). Stage it into the build context from the
bench first, then build from the repo root:

    cp ~/nova-bench/harness-v1.18.20/opencode infra/image/opencode
    podman build -t nova-card:<sha> -f infra/image/Containerfile .

Tag with the nova-tools commit the context was taken from. Rootless podman;
no daemon, no sudo.

The build verifies the staged binary against `OPENCODE_SHA256` in the
Containerfile and fails if it does not match, so staging the wrong harness is a
build error rather than a surprise inside a card run.

## The run contract

The image is only half of the sandbox. The flags below are the other half, and
they are **part of the contract, not suggestions** — a card run that drops one
of them is not the thing that was reviewed.

    podman run --rm \
      --userns=keep-id:uid=10001,gid=10001 \
      --read-only \
      --tmpfs /tmp:rw,exec,nosuid,nodev,size=1g \
      --mount type=tmpfs,destination=/home/card,tmpfs-size=2g,tmpfs-mode=0700,U=true,notmpcopyup \
      --security-opt no-new-privileges \
      --cap-drop=ALL \
      --memory 8g \
      --pids-limit 512 \
      -v <workdir>:/work \
      -e DEEPSEEK_API_KEY \
      nova-card:<sha> <cmd>

### The workspace

- `<workdir>` is a host directory the caller owns. It arrives at `/work`, and
  files the container writes there come out owned by the host user. **It is the
  only bind mount.** Nothing else of the host is mounted — not the secrets
  store, not the harness directory, not a config file. Verified from inside a
  run: the only writable mounts are `/work`, `/tmp` and `/home/card`, and the
  host's `~/.config/nova-secrets` cannot be reached at all.
- `--userns=keep-id:uid=10001,gid=10001` maps the container's `card` user onto
  the invoking host user. **It is not optional.** Without it the container's uid
  10001 lands somewhere in the host's subuid range and cannot so much as `cd`
  into a `0700` workspace: `cd: /work: Permission denied`.

### The wall

- `--read-only` makes the whole root filesystem read-only. Verified: `touch
  /usr/local/bin/evil` and `touch /etc/evil` both fail with `Read-only file
  system`. A card cannot modify its own toolchain, and nothing it does to the
  image survives the run.
- `--tmpfs /tmp` and a tmpfs over `/home/card` give back the two writable paths
  the toolchain actually needs, as memory that dies with the container.
  - `/tmp` carries `exec` deliberately: `go test` compiles the test binary into
    `$TMPDIR` and then runs it, so `noexec` there breaks every card that runs a
    test. `nosuid,nodev` stay.
  - `$HOME` uses the `--mount` form rather than `--tmpfs` because `--tmpfs
    /home/card` mounts root-owned `0750` and the `card` user then cannot write
    to its own home (`bash: /home/card/.bash_profile: Permission denied`), and
    with `--cap-drop=ALL` nothing in the container can chown it afterwards.
    `U=true` has podman chown the mount to the container user, giving a proper
    `0700 card:card` home.
  - **`GOCACHE` lives on the `$HOME` tmpfs** (`/home/card/.cache/go-build`, set
    in the image), so the Go build cache is disposable and never pollutes the
    caller's workspace. Measured on the smoke run: 143 MB of cache for a full
    `go build ./...` of nova-tools, so the 2 GB tmpfs is roomy. A card that
    wants a warm cache across runs should be given an explicit cache volume —
    that is a later step, not a reason to write into `/work`.
- `--security-opt no-new-privileges` — verified `NoNewPrivs: 1`. No setuid
  binary in the image can raise privileges mid-run.
- `--cap-drop=ALL` — verified `CapEff: 0000000000000000` **and** `CapBnd:
  0000000000000000`. The bounding set is empty, so there is no capability for a
  process to regain. Nothing a card does needs one: it clones, compiles, runs
  tests and talks HTTPS.
- `--memory 8g` and `--pids-limit 512` keep one bad card from taking the bench
  down — a runaway allocation or a fork bomb hits its own cgroup, not the
  machine and not the CI runners sharing it. The numbers are sized from the
  real smoke run, which peaked at **925 MB** against the 8 GB cap: 8 GB leaves
  roughly 8× headroom for a heavier build (and the tmpfs pages count against
  the same cap, so keep the two tmpfs sizes well inside it), and 512 pids is
  ample for a 64-way parallel Go build plus git and a shell, while a fork bomb
  hits the wall immediately.

### The network

The container keeps podman's **default rootless network** (slirp4netns, address
`10.0.2.100`). Not `--network=host`: that would hand a card the bench's whole
network namespace, including every service on the host's loopback. Not
`--network=none` either — a card genuinely needs egress, to clone from GitHub
and to call the model API. Verified from inside: GitHub reachable, and the
host's loopback services are *not* on the container's loopback.

This is the weakest part of the wall today: egress is open, so the boundary is
"can reach the internet", not "can reach the two endpoints it needs". An egress
allowlist is a later step and is not in this image.

### Credentials

Passed by name only — `-e DEEPSEEK_API_KEY` forwards the value from the
caller's environment, which on the host comes from `nova-secrets exec` wrapping
the `podman run`. Never a `--env-file`, never a mounted secrets directory,
never a value spelled out on the command line where it would land in shell
history and in `ps`.

## Assumptions

- **amd64 only.** Both the Go and SBCL URLs are `x86-64-linux`, and the harness
  binary is an x86-64 ELF. The image builds and runs on the Linux benches; it is
  not for the Studio's arm64.
- **`GOTOOLCHAIN=local`.** The image's Go is the Go a card gets. A `go.mod`
  asking for a newer toolchain fails loudly rather than silently downloading one
  mid-run.
- **`git config --system safe.directory '*'`.** The workspace is always owned by
  a uid other than the one git expects for a fresh checkout, so the dubious
  ownership check would otherwise stop every git command. A blanket `*` is only
  acceptable because of the run contract above: `/work` is the single workspace
  the caller handed in, `$HOME` is a tmpfs that dies with the container, and the
  root filesystem is read-only — so there is no second repository, and no
  attacker-placed `.git` directory, for the check to protect us from. Loosen the
  run contract and this line stops being safe.
- **`/usr/local/bin` symlinks for `go` and `gofmt`.** `bash -lc` sources
  `/etc/profile`, which rewrites `PATH` from scratch and would drop
  `/usr/local/go/bin`. The symlinks put the toolchain on the login-shell PATH;
  the `ENV PATH` in the Containerfile covers non-login shells.
- **The harness binary carries no credentials.** It is the same executable the
  bench runs; opencode reads its configuration and keys from `$HOME` and the
  environment at run time, and the image's `/home/card` is empty of both — and
  under the run contract it is a fresh tmpfs anyway.
- **The harness binary is an unsigned vendor blob.** 176 MB of executable with
  no signature we can check and no source we build it from; that is the largest
  piece of trust in this image. What we do about it: its sha256 is pinned in the
  Containerfile and verified immediately after the `COPY`, before anything runs
  it, so the image can only ever contain the exact binary that was reviewed —
  a swapped, truncated or stale stage fails the build. Pinning proves identity,
  not provenance; a signed upstream release would be the real answer, and
  updating the harness means updating `OPENCODE_VERSION` and `OPENCODE_SHA256`
  together, deliberately.
