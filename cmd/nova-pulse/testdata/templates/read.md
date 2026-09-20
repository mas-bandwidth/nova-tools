RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
TURNS: 8
REASONING: low
Do not run go build, go test or any toolchain; read and write only.
Do not grep around. Each STEP names the exact file and line range; the numbered step count is the turn budget.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Read the named files and write notes.txt in the repo directory.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.
