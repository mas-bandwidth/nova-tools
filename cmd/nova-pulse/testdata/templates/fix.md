RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Make the fix; report the red line and then the green line, one row per item.
STEP 3. git add -A && git commit -q -m "<label>" && git bundle create repo.bundle <branch>
   check: git bundle verify repo.bundle names <branch>. This is how the commit leaves: the card may be running on a disposable volume that is deleted the moment it exits, and the bundle is what `nova-sandbox run --out <dir>` carries off it. A commit with no bundle is a commit that never happened.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.
