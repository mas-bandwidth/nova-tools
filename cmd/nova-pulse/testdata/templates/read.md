RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
This card runs on whatever bench it was dealt to — a linux bench or a Mac. Every command below is spelt so both answer; keep it that way.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Read the named files and write notes.txt in the repo directory.
   Find a file with git ls-files or find, never with a GNU-only listing flag; hash one with shasum -a 256, which both platforms have.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.
