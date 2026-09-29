# nova-functional: the functional-tier image

One container image, one job: run the functional tests of this repository (the
tests in files behind `//go:build functional`) in a container that owns every
process, port, System V shared memory segment and temporary file the run makes.
The test fixtures are unchanged. They start `redis-server` and `postgres` as
child processes of the test binary; inside the container those are children of
the container, and removing the container removes them, however the run ends.

The unit tier does not use this image. It calls out to no external service and
runs where it is.

## What it carries

| | |
|---|---|
| base | `docker.io/library/ubuntu:24.04`, pinned by its index digest in the Containerfile |
| packages | the distribution's, at a dated snapshot of the archive (`APT_SNAPSHOT`): `git`, `lsof`, `make`, `postgresql-16`, `procps`, `python3`, `sqlite3`, `ca-certificates` |
| Go | the exact version `go.mod` pins, from go.dev, sha256 verified per architecture, `GOTOOLCHAIN=local` |
| redis-server, redis-cli | Redis 8.0.5 built from the release source, sha256 verified |
| sops, age-keygen | the versions `docs/SPEC-SECRETS.md` pins as minimums, sha256 verified |
| user | `bench`, uid 10001, `HOME=/home/bench` |

Versions are chosen to satisfy every place the repository states or assumes
one. Postgres is 16: it is the first directory the fixtures search
(`/usr/lib/postgresql/16/bin`, so no `NOVA_PG_BIN` is needed) and the version
`.github/scripts/install-postgres.sh` installs. Redis is 8: the tests read Redis
8's error wording (`internal/nsprint/deal` matches its ACL refusals, and the
texts in `internal/redisfn`'s tests are Redis 8's), and 8.0.5 is the release
`.github/scripts/install-redis-server.sh` builds where the distribution has
none.

`binaries.txt` lists every program the functional tier runs by name and what
carries it, or why the tier does without it. The class test
`TestFunctionalImageCarriesEveryBinaryTheTierExecs` (`internal/ci`) holds that
list to the Go files in the tree in both directions and to this Containerfile,
so a new `exec.Command("tool", ...)` is red until the list, and so the image,
carries `tool`. The class tests beside it hold the base to a digest, the Go
version to `go.mod`, every download to a checksum, and the run user to non-root.

Environment baked in:

| variable | value | why |
|---|---|---|
| `NOVA_CI` | `1` | the fixtures fail a test whose dependency is missing and never skip it |
| `NOVA_FUNCTIONAL_RUN` | `container` | marks a run inside this image; `NOVA_CI` is also set on runs that are not in a container |
| `GOTOOLCHAIN` | `local` | never fetch a toolchain |
| `GOPROXY` | `off` | the module cache is filled in a separate step, so a missing module fails at once and names itself |
| `GOFLAGS` | `-buildvcs=false` | the mounted tree has no `.git` |
| `GOCACHE`, `GOMODCACHE` | `/gocache`, `/gomodcache` | mount points for the two caches |

## Build

The build context is this directory:

    podman build -t nova-functional -f infra/functional-image/Containerfile infra/functional-image

Every input is pinned (the base by digest, the archive by snapshot instant, each
download and source release by sha256), so two builds contain the same package
and tool versions. The build writes `/image-manifest.txt` inside the image: the
base, the snapshot instant, the Go, Redis, Postgres, git, make, python3, sqlite3,
sops and age versions, and every installed package with its version. Print it
with one command:

    podman run --rm nova-functional cat /image-manifest.txt

Compare two builds by diffing their manifests.

## Run

A run is one container for one package set. The source tree is mounted read
only; the two Go caches are named volumes. The module cache is filled once by a
separate step that has the network, and is mounted read only for every run. The
build cache is written by the code under test, so it is kept per trust domain
(see "The build cache" below); the commands name it `nova-gocache-<domain>`:

    podman run --rm --timeout 600 --security-opt no-new-privileges --cap-drop all \
      -v "$PWD":/src:ro -v nova-gomod:/gomodcache \
      -e GOPROXY=https://proxy.golang.org -w /src nova-functional go mod download

The run:

    podman run --rm --name nova-functional-run --init --timeout 600 \
      --network none --ipc private --pids-limit 512 --memory 4g --memory-swap 4g --cpus 4 \
      --security-opt no-new-privileges --cap-drop all \
      --read-only --tmpfs /tmp:rw,exec,size=2g --tmpfs /home/bench:rw,size=1g,mode=1777 \
      -v "$PWD":/src:ro -v nova-gocache-<domain>:/gocache -v nova-gomod:/gomodcache:ro \
      -w /src nova-functional \
      make test-functional PKGS=./internal/ntable/

`make test-functional` runs only the functional tests of the packages named,
which is the definition of the tier. The mounted tree is read by the `bench`
user as "other", so it is world-readable.

### The flags

| flag | why |
|---|---|
| `--rm` | the container, its writable layer and its anonymous volumes are removed when it exits |
| `--name` | one name per run, so an operator or a reaper finds the container by it |
| `--init` | a real PID 1 that reaps orphaned children and passes signals on, so a fixture's stray process is collected |
| `--timeout <seconds>` | the runtime's own hard bound: the container is ended when it expires, even if the client that started it is killed. The inner `go test -timeout` bounds each test binary but not a client that dies |
| `--network none` | loopback only. The fixtures bind `127.0.0.1`; nothing leaves the container and nothing reaches in. A missing module fails at once (`GOPROXY=off`) instead of waiting on a dial |
| `--ipc private` | System V shared memory and queues are the container's own, so Postgres's segment cannot outlive the run or collide with another |
| `--pids-limit` | a fork bomb or a leak stops at the limit |
| `--memory` | the run, tmpfs included, stops at the limit |
| `--memory-swap` | equal to `--memory`, so the run may use no swap: without it the runtime allows as much swap again as memory, and the run takes more from the machine than it was given |
| `--cpus` | the run takes no more cores than it was given |
| `--security-opt no-new-privileges` | no process in the run gains privilege through a setuid binary (the base image carries `su`, `passwd` and `mount`) |
| `--cap-drop all` | the run holds no capability; the fixtures need none as the `bench` user |
| `--read-only` | the image is not written; the writable places are listed under "Where a run can write" |
| `--tmpfs /tmp` | test directories and built test binaries live in memory and are gone at exit; `exec` because test binaries run from there |
| `--tmpfs /home/bench` | a writable home, in memory; `mode=1777` because podman does not take a `uid` option here |
| `-v …:/src:ro` | a test cannot change the tree it tests |
| `-v nova-gocache-<domain>:/gocache` | the build cache: warm runs compile nothing. One volume per trust domain and one writer at a time (see below) |
| `-v nova-gomod:/gomodcache:ro` | the complete module cache, read only |

### Where a run can write

With `--read-only` the test user (`bench`) can write in these places and no
other:

| place | what | size |
|---|---|---|
| `/tmp` | the `--tmpfs` mount: test directories and test binaries | 2 GB, counted in `--memory` |
| `/home/bench` | the `--tmpfs` mount: the home directory | 1 GB, counted in `--memory` |
| `/gocache` | the build cache volume, on disk, not counted in `--memory` (see below) | none |
| `/var/tmp` | a tmpfs that `--read-only` adds; podman gives it no size option | no size limit of its own; a run that fills it is stopped by `--memory` or not at all, and that has not been measured |
| `/dev/shm` | the container's own shared memory, which Postgres uses | 64 MB |

`/run` is also a tmpfs that `--read-only` adds, and `bench` cannot write to it.
`/src` and `/gomodcache` are read only mounts, and `/` is read only.

### The build cache

The test code in a run writes to `/gocache`, and Go links a cache entry into a
later build by its action id without re-checking its content (only
`GODEBUG=gocacheverify=1` re-checks, and it re-runs every action, so the cache
saves nothing). A cache that a broken or malicious branch has written can
therefore make another run go falsely green. The cache cannot reach the host
(no network, the tree is read only), so the rule is about which runs share one:

- One cache volume per trust domain. A run of code nobody has reviewed writes
  only a cache of its own: a volume made for that run and removed after it, or
  `--tmpfs /gocache:rw,size=2g,mode=1777` in place of the volume. Only runs of
  reviewed, merged code share `nova-gocache-merged`, and no volume that a run
  of unreviewed code has mounted is ever mounted by another run.
- One writer at a time on a volume, so parallel containers use a volume each.
- A size bound. The volume is not covered by `--memory` and grows with every
  new package and Go version. Measure it before a run and remove it above the
  bound; the next run rebuilds it warm-slow once:

      podman run --rm -v nova-gocache-merged:/gocache:ro nova-functional du -sm /gocache

  A volume over 2048 MB (`podman volume rm nova-gocache-merged`) is removed.

The runtime that runs this on a runner is installed and probed by
`fleet/container-runtime.yml` (rootless podman; the probe runs a container with
these flags and fails when a limit is not enforced).

## Layout

| file | |
|---|---|
| `Containerfile` | the image |
| `binaries.txt` | every program the tier runs by name, and what carries it |
| `README.md` | this file |
