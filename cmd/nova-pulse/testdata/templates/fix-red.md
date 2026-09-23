RESULT: <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
KIND: fix-red
PATHS: internal/swarm/batch.go, internal/swarm/batch_test.go
TEST: ./internal/swarm TestTheNamedDefect
LEGS: go
SOURCE: <source>#1728
MODE: explore
TURNS: 12
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head; that head is the pinned base.
STEP 2. Read the ground: the SOURCE issue and every PATHS file at the pinned head; write notes.txt.
STEP 3. Write the TEST first and run it alone: report the red line.
STEP 4. Make the smallest fix inside PATHS and run the TEST again: report the green line.
STEP 5. Negative control: revert the fix, run the TEST, see it red; restore the fix.
STEP 6. Gates: gofmt -l on the changed files, go vet and go test on the touched package only.
STEP 7. git add -A && git commit -q -m "<label>" && git bundle create repo.bundle <branch>
   check: git bundle verify repo.bundle names <branch>.
STEP 8. Write RESULT.md with line 1 equal to this card's line 1.

Below the steps, inside the contract hash (SPEC-TOOLWORK section 5 rule 1): the lane, the
accept gate, the bench certificate and the rules prose.
LANE: work
ACCEPT: fix-red gate: hygiene, shape, positive, mutate
CERT: standard
Rules: this card is unattended; never ask a question. Stay inside PATHS. One command per line.
