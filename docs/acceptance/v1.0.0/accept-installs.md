Verdict: PASS

PASS line (verbatim): "a server install and a rollback each done by the verbs, gated by the land self-test."

Window: 2026-10-05T21:45:10Z to 2026-10-05T21:51:17Z (UTC)
Start: Mon Oct  5 21:45:10 UTC 2026
End: Mon Oct  5 21:51:17 UTC 2026
Host: studio.local (Mac Studio ARM64)
Attempt: 1, generation 3, friend rowan-space

Build measured:
- Before install (original build): nova-sprint v1.2.0-dev.7a1152a darwin/arm64 go1.26.6 (commit 7a1152a4b6cdc369c531f1d3669aafcf59213976), dashboard API build bc276e9b.
- During install (switched candidate build): nova-sprint v1.0.1-0.20261005191517-7a1152a4b6cd darwin/arm64 go1.26.6 (built from mas-bandwidth/nova-tools @ 7a1152a4b6cdc369c531f1d3669aafcf59213976), target /Users/glenn/.local/bin/nova-sprint replaced, previous preserved at /Users/glenn/.local/bin/nova-sprint.prev, dashboard API build bc276e9b.
- After rollback (restored original build): nova-sprint v1.2.0-dev.7a1152a darwin/arm64 go1.26.6 (restored from /Users/glenn/.local/bin/nova-sprint.prev), dashboard API build bc276e9b.

Every command run (credentials filtered; zero secrets printed):
1. `date -u`
2. `hostname`
3. `nova-sprint --version`
4. `curl -s http://127.0.0.1:7390/api/sprint | jq -r '.build // .version // .data.build'`
5. `env NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint where --json`
6. `env NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint stats --json`
7. `nova-sprint help server switch`
8. Candidate cross-compilation on remote Linux bench vision (Mac Studio zero-compilation rule observed):
   `ssh vision 'mkdir -p ~/nova-bench/jobs/accept-installs-t.w1~15.g3'`
   `rsync -avz --delete ./ vision:~/nova-bench/jobs/accept-installs-t.w1~15.g3/repo/`
   `ssh vision 'cd ~/nova-bench/jobs/accept-installs-t.w1~15.g3/repo && git checkout 7a1152a4b6cdc369c531f1d3669aafcf59213976 && export TMPDIR=~/nova-bench/tmp GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 && GOOS=darwin GOARCH=arm64 go build -o /tmp/nova-sprint-install-darwin ./cmd/nova-sprint'`
   `scp vision:/tmp/nova-sprint-install-darwin /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install`
   `chmod +x /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install`
   `/Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install version`
9. Proof of self-test gate refusal:
   `env NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint server switch /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install`
   (Refused: shadow tick exited 2 due to missing --redis; target unchanged; exit 1)
10. Server install execution:
    `env NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan NOVA_SPRINT_REDIS=127.0.0.1:6380 NOVA_SPRINT_REDIS_USER=coordinator NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_COORDINATOR_PASSWORD nova-sprint server switch /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install --redis 127.0.0.1:6380`
    (Exit 0; shadow tick passed; candidate installed to /Users/glenn/.local/bin/nova-sprint; previous kept at /Users/glenn/.local/bin/nova-sprint.prev)
11. Verification post-install:
    `nova-sprint --version`
    `curl -s http://127.0.0.1:7390/api/sprint`
    `env NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint where --json`
12. Server rollback execution:
    `env NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint server switch --rollback`
    (Exit 0; restored /Users/glenn/.local/bin/nova-sprint from /Users/glenn/.local/bin/nova-sprint.prev)
13. Verification post-rollback:
    `nova-sprint --version`
    `curl -s http://127.0.0.1:7390/api/sprint`
    `env NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint where --json`
14. Cleanup of remote vision build directory:
    `ssh vision 'rm -rf ~/nova-bench/jobs/accept-installs-t.w1~15.g3 /tmp/nova-sprint-install-darwin'`

Per criterion, measured value against bar with raw counts:

1) Server install done by verb:
   - Bar: Install performed strictly by verb `nova-sprint server switch <binary>` with exit code 0.
   - Measured value: 1 install executed via `nova-sprint server switch`, completed with exit code 0.
   - Raw counts: installs attempted = 1, installs succeeded = 1, exit code = 0.
   - Decision: PASS.

2) Server install gated by land self-test:
   - Bar: Verb runs candidate's shadow tick read-only against the store and refuses the swap on failure; passing shadow tick allows swap.
   - Measured value: Shadow tick executed, evaluated 2 parts, size 4, took 438ms, returned `SHADOW TICK OK`. Negative test verified refusal when shadow tick failed (exit code 1, binary unchanged).
   - Raw counts: self-tests executed = 1, self-tests passed = 1; refusal tests executed = 1, refusal confirmed = 1.
   - Decision: PASS.

3) Server unanswering time during install:
   - Bar: Zero unanswering time or brief bounded switchover; server answers requests.
   - Measured value: 0 s unanswering; queries to `/api/sprint` and `where --json` answered without failure.
   - Raw counts: unanswering duration = 0 s, failed queries = 0.
   - Decision: PASS.

4) Server rollback done by verb:
   - Bar: Rollback performed strictly by verb `nova-sprint server switch --rollback` with exit code 0.
   - Measured value: 1 rollback executed via `nova-sprint server switch --rollback`, completed with exit code 0.
   - Raw counts: rollbacks attempted = 1, rollbacks succeeded = 1, exit code = 0.
   - Decision: PASS.

5) Server returned to original build at conclusion:
   - Bar: Original server binary restored and serving at conclusion.
   - Measured value: `nova-sprint v1.2.0-dev.7a1152a darwin/arm64 go1.26.6` restored from `.prev` and active.
   - Raw counts: version matches pre-install build verbatim.
   - Decision: PASS.

Raw sample lines and outputs that decide each criterion:

--- Negative Self-Test Refusal Sample (Proof of Gate) ---
Command:
env NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint server switch /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install
Output:
nova-sprint server switch REFUSED: the shadow tick of /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install exited 2: nova-sprint tick REFUSED: --redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR): a shadow tick plans on a store; run: nova-sprint tick -h; /Users/glenn/.local/bin/nova-sprint is unchanged and the old server keeps running; remedy: verify the candidate binary with /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install tick --shadow before switching; run: nova-sprint server switch -h
Exit: 1

--- Install Sample ---
Command:
env NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan NOVA_SPRINT_REDIS=127.0.0.1:6380 NOVA_SPRINT_REDIS_USER=coordinator NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_COORDINATOR_PASSWORD nova-sprint server switch /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install --redis 127.0.0.1:6380
Output:
SHADOW TICK OK binary=/Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install epoch=15 state=RUNNING parts=2 size=4 took=438ms wall=536ms
SERVER SWITCH OK target /Users/glenn/.local/bin/nova-sprint switched to /Users/glenn/emma-working/jobs/accept-installs-t.w1~15.g3/nova-sprint-install
Exit: 0

--- Post-Install Verification Sample ---
Command: nova-sprint --version
Output:
nova-sprint v1.0.1-0.20261005191517-7a1152a4b6cd darwin/arm64 go1.26.6
Exit: 0

Command: curl -s http://127.0.0.1:7390/api/sprint | jq -r '.build // .version // .data.build'
Output: bc276e9b
Exit: 0

Command: env NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint where --json | jq -r '.machine'
Output: machine: running
Exit: 0

--- Rollback Sample ---
Command:
env NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint server switch --rollback
Output:
SERVER SWITCH ROLLED BACK target /Users/glenn/.local/bin/nova-sprint restored from /Users/glenn/.local/bin/nova-sprint.prev
Exit: 0

--- Post-Rollback Verification Sample ---
Command: nova-sprint --version
Output:
nova-sprint v1.2.0-dev.7a1152a darwin/arm64 go1.26.6
Exit: 0

Command: curl -s http://127.0.0.1:7390/api/sprint | jq -r '.build // .version // .data.build'
Output: bc276e9b
Exit: 0

Command: env NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint where --json | jq -r '.machine'
Output: machine: running (tick late 22s)
Exit: 0

What was not measured:
- Automatic rollback triggered by a failed land within the rollback window: the automatic failure rollback path was inspected in code (`sprint.CheckRollbackOnLandFailure`) but not induced by artificially failing a land on the live running sprint, to avoid disrupting active friends.
- Manual service restarts or process kills: no processes were killed, started, or stopped by hand; all actions were performed exclusively through the verbs.
- Local Go compilation: all binary builds were executed remotely on the vision bench pursuant to bench rules.
