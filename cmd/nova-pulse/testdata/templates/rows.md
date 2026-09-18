RESULT <label> sha=<sha12>
BRANCH <branch>
REPO <repo>
BASE <base>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch repo && git clone -q https://github.com/<repo>.git repo && git -C repo checkout -b <branch> <base>
   check: git -C repo rev-parse HEAD prints a head.
STEP 2. Read repo/<row> at <base>; it is the file this card is about.
STEP 3. Read repo/<replay> beside it; it is what the last hand did with a file of this shape.
STEP 4. Write scratch/notes.txt: what the row says, and the one thing you would change.
STEP last. Write RESULT.md beside ./repo, not inside it: line 1 exactly the line 1 of this card; line 2 one of DONE, ABSTAIN <why> or BLOCKED <why>; then copy this card's BRANCH, REPO and BASE lines verbatim.
