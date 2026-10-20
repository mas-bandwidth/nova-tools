The stable tools point release after v1.6.10. It lets a card's dotnet run inside the job wall on Linux. nova-sprint stays at v1.6.6.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.11 --bin <dir>` with the nova-sprint v1.6.6 binaries staged beside them (see docs/SPEC-RELEASE.md).

## What changed in v1.6.11

- A card's `dotnet run`, `build` or `test` no longer fails inside the job wall on Linux. The .NET runtime keeps its named mutexes under the fixed path `/tmp/.dotnet/shm`, and `TMPDIR` does not move it. NuGet takes one on every restore with a fresh HOME, and the wall refused the open (`EACCES`). `nova-swarm native` now creates `/tmp/.dotnet` outside the wall (mode 0777, as dotnet creates it) and grants that one directory as a `--write`, never `/tmp` itself and never through a symlink. The gate runner uses the same wall, so it gets the fix too.
- The child environment sets `DOTNET_CLI_HOME` to the job's data home, and sets `DOTNET_SKIP_FIRST_TIME_EXPERIENCE`, `DOTNET_NOLOGO` and `DOTNET_CLI_TELEMETRY_OPTOUT` to 1. A value inherited from the bench is dropped.
