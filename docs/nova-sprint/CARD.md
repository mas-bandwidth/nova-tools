# Nova Sprint Card Contract

A sprint card specifies work, check instructions (`CHECK`), and expected output patterns (`EXPECT`).

## Harness Contract
- `NOVA_CARD_REPO=<job>/repo`: The clone path where the harness executes.
- `NOVA_CARD_BRANCH`: The branch where the harness leaves its commit.
- The wrapper executes `CHECK` at head (on a detached worktree at `NOVA_CARD_BRANCH`) and at base (`base_sha` with test-only patch applied via `git diff <base-sha> <head> -- '*_test.go'`).
