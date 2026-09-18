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
| opencode | the harness binary at `/usr/local/bin/opencode`, copied in at build time from the bench |
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

## The run contract

    podman run --rm \
      --userns=keep-id:uid=10001,gid=10001 \
      -v <workdir>:/work \
      -e DEEPSEEK_API_KEY \
      nova-card:<sha> <cmd>

- `<workdir>` is a host directory the caller owns. It arrives at `/work`, and
  files the container writes there come out owned by the host user.
- `--userns=keep-id:uid=10001,gid=10001` maps the container's `card` user onto
  the invoking host user. **It is not optional.** Without it the container's uid
  10001 lands somewhere in the host's subuid range and cannot so much as `cd`
  into a `0700` workspace: `cd: /work: Permission denied`.
- Credentials are passed by name only — `-e DEEPSEEK_API_KEY` forwards the value
  from the caller's environment, which on the host comes from `nova-secrets exec`
  wrapping the `podman run`. Never a `--env-file`, never a mounted secrets
  directory, never a value on the command line where it would land in shell
  history and in `ps`.
- Nothing else of the host is visible. The host's `~/.config/nova-secrets` is
  not mounted and cannot be reached; `/home` inside the container holds only the
  image's own users.

## Assumptions

- **amd64 only.** Both the Go and SBCL URLs are `x86-64-linux`, and the harness
  binary is an x86-64 ELF. The image builds and runs on the Linux benches; it is
  not for the Studio's arm64.
- **`GOTOOLCHAIN=local`.** The image's Go is the Go a card gets. A `go.mod`
  asking for a newer toolchain fails loudly rather than silently downloading one
  mid-run.
- **`git config --system safe.directory '*'`.** The workspace is always owned by
  a uid other than the one git expects for a fresh checkout, so the dubious
  ownership check would otherwise stop every git command. The container is a
  single-tenant throwaway holding one workspace we handed it, so the check buys
  nothing here.
- **`/usr/local/bin` symlinks for `go` and `gofmt`.** `bash -lc` sources
  `/etc/profile`, which rewrites `PATH` from scratch and would drop
  `/usr/local/go/bin`. The symlinks put the toolchain on the login-shell PATH;
  the `ENV PATH` in the Containerfile covers non-login shells.
- **The harness binary carries no credentials.** It is the same executable the
  bench runs; opencode reads its configuration and keys from `$HOME` and the
  environment at run time, and the image's `/home/card` is empty of both.
