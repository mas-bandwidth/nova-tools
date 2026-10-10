# Release Architecture and Operations

This document describes the automated release chain for `nova-tools` and the single operator recovery path.

## The Release Chain

A release is real: every commit tagged for release has been vouched for by the full certification test suite, and the release assets are built, verified, and published automatically by GitHub Actions workflow.

```
+-----------------------------------------------------------------------------------+
| 1. PR CI (.github/workflows/ci.yml)                                               |
|    - Unit tier (fast)                                                             |
|    - Functional tier (real processes, redis, binary checks across runner pool)    |
+-----------------------------------------------------------------------------------+
                                         |
                                         v
+-----------------------------------------------------------------------------------+
| 2. Certification Workflow (.github/workflows/certification.yml)                   |
|    - Whole-tree race tests                                                        |
|    - Multi-platform matrices (Linux, macOS Darwin, Windows)                       |
|    - Release dry-runs & tick gates                                                |
|    - Aggregated in certification-ok                                               |
+-----------------------------------------------------------------------------------+
                                         |
                                         v
+-----------------------------------------------------------------------------------+
| 3. Release Cut (`nova-update release cut`)                                        |
|    - Refuses uncertified commits (demands certification.yml run)                  |
|    - --dispatch-certification flag (or env var) dispatches & polls                |
|    - Enforces gates (sensitive paths, dogfood, journeys, spend)                   |
|    - Writes CHANGELOG section with SHA256SUMS digest                              |
|    - Creates annotated tag with digest                                            |
|    - Prints receipt line ending in publish=workflow                               |
+-----------------------------------------------------------------------------------+
                                         |
                                         v
+-----------------------------------------------------------------------------------+
| 4. Release Workflow (.github/workflows/release.yml)                               |
|    - certified job: verifies certification.yml vouched for the tagged commit      |
|    - build job: compiles binaries for all shipped platforms (release-targets)    |
|    - release job:                                                                 |
|        a. Downloads and stamps all binaries                                       |
|        b. Runs `ghrelease attach` to create global SHA256SUMS and draft release   |
|        c. Generates per-platform SHA256SUMS_<goos>_<goarch> files and uploads     |
|        d. Inserts CHANGELOG SHA256SUMS digest into release body                   |
|        e. Publishes release draft (`gh release edit "$TAG" --draft=false`)        |
+-----------------------------------------------------------------------------------+
```

### 1. Pull-Request CI Runs Functional Shards

Pull-request CI runs both unit tests and functional shards on every pull request onto sprint branches and `dev`. A functional test runs behind `//go:build functional` using real services (such as `redis-server`), real binaries, and real sub-processes. Running functional shards on pull requests ensures regressions are caught at the PR stage rather than later in merge queues or release cuts.

All functional shards execute under the two-minute timeout cap on the self-hosted Linux runner pool.

### 2. Certification Requirement

Before a release can be cut, `certification.yml` must vouch for the exact target commit. `nova-update release cut` queries the forge for the `certification`/`certification-ok` check runs on that commit and applies the same check `release.yml` does through `go run ./tools/ghrelease certified`: every certification run must be completed, and the run carrying the latest update must be green (an older green never covers a newer red or a still-running run). If the commit is not vouched for:
- The command refuses with exit code 2.
- The refusal names the exact dispatch command on the certified sha:
  ```bash
  gh workflow run certification.yml --ref <sha>
  ```
- An automated cutter passes `--dispatch-certification` (or sets `NOVA_RELEASE_DISPATCH_CERTIFICATION=1`) and the command dispatches the workflow on the certified sha and waits for completion.
- Waivers (`--no-dogfood-gate`, `--no-journey-gate`, `--no-spend-gate`) never waive certification.

### 3. Automated Publication by `release.yml`

When a valid annotated version tag (`v*`) is pushed:
1. `certified`: Asks GitHub API whether `certification.yml` vouches for the commit.
2. `build`: Compiles all binaries across the platforms in `tools/ghrelease/release-targets`:
   - `linux amd64`
   - `linux arm64`
   - `darwin arm64`
   - `darwin amd64`
   - `windows amd64`
3. `release`:
   - Downloads all artifacts into `dist/`.
   - Confirms version stamps on executables.
   - Runs `go run ./tools/ghrelease attach` to compute global `SHA256SUMS` and create the release draft.
   - Generates per-platform `SHA256SUMS_<goos>_<goarch>` files and uploads them to the release draft.
   - Extracts the `SHA256SUMS digest` from `CHANGELOG.md` and appends it to the release body.
   - Publishes the release object:
     ```bash
     gh release edit "$TAG" --draft=false
     ```

The cut receipt line records `publish=workflow` to indicate that release publication is owned by the workflow.

---

## Operator Recovery Path

Manual release creation by an operator is documented **strictly as a recovery path** for situations where GitHub Actions infrastructure or `release.yml` itself is broken. Under normal operations, all releases must be published by `release.yml`.

### Step 1: Confirm Certification
Never cut or publish an uncertified commit. Ensure `certification.yml` has run and passed:
```bash
gh workflow run certification.yml --ref <commit-sha-or-ref>
gh run list --workflow=certification.yml --commit <commit-sha>
```
Verify that the run conclusion is `success`.

### Step 2: Build Artifacts for All Shipped Platforms
From a clean checkout at the tagged commit, build each platform listed in `tools/ghrelease/release-targets`:
```bash
mkdir -p dist
for target in "linux amd64" "linux arm64" "darwin arm64" "darwin amd64" "windows amd64"; do
  set -- $target
  go run ./tools/ghrelease build --require-v-tag "$TAG" "$1" "$2" dist
done
```

### Step 3: Compute Checksums
Generate the global `SHA256SUMS` and per-platform checksum files:
```bash
go run ./tools/ghrelease sums "$TAG" dist

while IFS=' ' read -r goos goarch; do
  [[ -z "$goos" || "$goos" =~ ^# ]] && continue
  target="${goos}_${goarch}"
  grep "_${TAG}_${target}" dist/SHA256SUMS > "dist/SHA256SUMS_${target}"
done < tools/ghrelease/release-targets
```

### Step 4: Extract CHANGELOG Digest and Create Release
Extract the digest recorded by `nova-update release cut` in `CHANGELOG.md`:
```bash
DIGEST=$(awk -v tag="$TAG" '
  $0 ~ "^## " tag " " { in_tag=1; next }
  in_tag && /^## / { in_tag=0 }
  in_tag && /^SHA256SUMS digest: / { print $3; exit }
' CHANGELOG.md)
```

Create the published release with release notes and the digest:
```bash
BODY_FILE=$(mktemp)
cat "docs/RELEASE-NOTES-${TAG#v}.md" > "$BODY_FILE"
if [ -n "$DIGEST" ]; then
  printf "\n\nSHA256SUMS digest: %s\nAdopt this release with \`--expect-sums %s\`.\n" "$DIGEST" "$DIGEST" >> "$BODY_FILE"
fi

gh release create "$TAG" dist/* \
  --title "$TAG" \
  --notes-file "$BODY_FILE" \
  --verify-tag

rm -f "$BODY_FILE"
```

### Step 5: Post-Recovery Reporting
Document in the release retrospective that manual operator recovery was used, naming the infrastructure incident or workflow defect that required it.
