# nova-reserve: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

Unit tests use fake locks and fake time; functional tests start
processes and take real locks. The models in `reserve/tla` cover the highest
number of holders, exclusive access, the life of a lock and the shared
capacity. `CASES.tsv` has 16 cases. The models do not cover what a platform does
to a child process when its parent is killed.

Run the models with TLC, with Java on PATH and the jar pinned in
[tests/tla/tla2tools.sha256](../tests/tla/tla2tools.sha256), at an absolute path:

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar reserve/tla
```

Each line starts with `ok` when the case came out as `reserve/tla/CASES.tsv` says.
