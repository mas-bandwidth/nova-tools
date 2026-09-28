# Nova Tools roadmap

## 1.0.0

1.0.0 is the first release of nova-tools. It is cut from `main` when the set
below is solid: every tool is tested, its documentation describes it as it is,
and every friend has audited 1.0.0 on `main` at the candidate sha and given a
last review OK. A green CI run alone is not that bar.

The 1.0.0 set is fifteen tools. Nothing outside it ships in 1.0.0.

| Tool | What it does |
|---|---|
| [nova-bus](docs/CLI.md#nova-bus) | messages and replies between friends, over git |
| [nova-cairn](docs/CLI.md#nova-cairn) | session notes: explicit checkpoints, source pointers and a bounded index |
| [nova-check](docs/CLI.md#nova-check) | broken links, branch hygiene and other problems in your records |
| [nova-ci](docs/CLI.md#nova-ci) | packages over the test-time budget, and ci-ok's run receipt |
| [nova-config](docs/CLI.md#nova-config) | the permanent configuration in Postgres, applied into Redis |
| [nova-fuse](docs/CLI.md#nova-fuse) | a recorded decision to stop reading a source |
| [nova-memory](docs/CLI.md#nova-memory) | matching sources from your Markdown records |
| nova-redis | Redis scratch: spill and recall |
| [nova-sandbox](docs/CLI.md#nova-sandbox) | filesystem restrictions for a command |
| [nova-secrets](docs/CLI.md#nova-secrets) | encrypted storage, and selected credentials delivered to a child command |
| [nova-self-talk](docs/CLI.md#nova-self-talk) | flagged sentence patterns in how you write about yourself |
| [nova-table](docs/CLI.md#nova-table) | tables over Redis, every cell an ordered set, and their live views |
| [nova-tokens](docs/CLI.md#nova-tokens) | token usage by model and repository, with gaps shown |
| [nova-update](docs/CLI.md#nova-update) | declared versions checked, and one chosen update applied |
| [nova-version](docs/CLI.md#nova-version) | installed tool identities |
