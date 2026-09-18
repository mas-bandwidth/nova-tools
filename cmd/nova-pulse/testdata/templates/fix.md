RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
This card runs on whatever bench it was dealt to — a linux bench or a Mac. Every command below is spelt so both answer; keep it that way.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. command -v go >/dev/null && go version || echo "no go toolchain on this bench"
   check: one version line, or the one sentence saying there is none. Ask a toolchain for its version the way that toolchain spells it — the long-form flag is not what every one of them takes.
STEP 3. cores=$(if [ "$(uname -s)" = Darwin ]; then sysctl -n hw.ncpu; else nproc; fi); echo "cores=$cores"
   check: a number. Neither half of that line is on both platforms; the uname test is what chooses.
STEP 4. Make the fix; report the red line and then the green line, one row per item.
   Do not measure a step with GNU time(1): a stock Mac has none, and the harness already writes this run's own timing line.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.
