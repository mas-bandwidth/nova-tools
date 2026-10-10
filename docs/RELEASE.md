# Releases

A release is a commit, a version, a set of stamped binaries, and a release object other people can
download. This page is the chain that makes one real, and the one way to recover when the workflow
itself is broken. The gates are specified in [SPEC-RELEASE.md](SPEC-RELEASE.md) and the verbs in
[SPEC-UPDATE.md](SPEC-UPDATE.md) and [CLI.md](CLI.md).

## The chain

1. **A pull request runs the functional shards.** [ci.yml](../.github/workflows/ci.yml)'s `functional`
   job runs on `pull_request` and `merge_group` alike, and both events deal the same
   `test-packages` functional list. A red functional test is red on the pull request that made it,
   never first in the merge queue and never at the cut. A shard that cannot finish under the
   per-package cap is split by the selection, never skipped; `ci-ok` requires the job's result.

2. **Certification vouches for the commit.** [certification.yml](../.github/workflows/certification.yml)
   is the slower tier that cannot fit the per-change cap: the whole-tree race run, the per-package
   Windows tests, the three-OS smoke of the shipped binary, and the release dry run. Its aggregate
   job is `certification-ok`. Nothing certifies a commit on its own, so certifying one is a
   dispatch:

   ```sh
   gh workflow run certification.yml --ref <tag-or-sha>
   ```

3. **The cut refuses a commit certification.yml has not vouched for.** `nova-update release cut`
   reads certification.yml's runs on the commit and refuses until the latest-updated evidence is a
   completed success. The refusal names the dispatch above. `--dispatch-certification` starts that
   run and waits for it. The `--no-dogfood-gate`, `--no-journey-gate` and `--no-spend-gate` waivers
   are evidence about other things; none of them covers certification.

4. **The tag is annotated and triggers release.yml.** A successful cut writes the CHANGELOG section
   and creates the annotated tag carrying `sums=<sha256 of SHA256SUMS>`; the tag push is what starts
   [release.yml](../.github/workflows/release.yml).

5. **release.yml publishes the release object.** Its `certified` job asks the same certification
   question again on the tagged commit. `build` compiles every shipped platform as
   `<tool>_<version>_<goos>_<goarch>`. `release` downloads the set into one directory, asserts every
   binary reports the tag, computes `SHA256SUMS` over the whole shipped set on the one machine that
   holds every artifact, and publishes the release object with the assets and that checksum file.

A cut's receipt says which path this release took:

```
RELEASE CUT version=<v> sha=<sha> ... publish=release.yml dry-run=no
```

## The one recovery path

Creating the release by hand is recovery when the workflow itself is broken, never the ordinary path:

```sh
gh release create <tag> <assets>
```

When it is used, say so in the release notes: the workflow is the path a release takes.

## Releasing

```sh
nova-update release cut --repo <owner/name> --from <branch> --version <v> --changelog ./CHANGELOG.md \
  --sums ./release/<v>/<goos>-<goarch>/SHA256SUMS --dispatch-certification
nova-update release build --version <v> --out ./release --source .
```

`cut --dispatch-certification` certifies the commit and then tags it; without it, the refusal names
the dispatch to run. `build` writes one `SHA256SUMS` per platform under `<out>/<version>/<goos>-<goarch>/`,
and `--sums` takes one of them: the tag and the CHANGELOG section carry that platform's digest, and
`adopt --repo` verifies that platform only.
