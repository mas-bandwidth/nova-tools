## The beat

The coordinator window restarts thin at every beat (token item, cairn 8386). `nova-pulse
beat --queue <dir> --cairn <file> --title <text> [--resume <text>]` appends one section to
the cairn file — `## <UTC time> <title>`, then one line per queue fact: the pending,
running, done and failed counts from `status`, the `REDS` line count, whether `STOP`
stands, the `HUMAN` line count, and the last merged PR from `<queue>/MERGED` — and then the
`Resume rule:` line: `--resume`, or `read this section, run nova-pulse status, act on
REDS/STOP/HUMAN first` when the flag names none. It reads only the queue and makes no model
call. When the cairn file is in a git repo it commits it (`git add` and one commit, never a
push) and otherwise leaves it uncommitted, and either way it prints `BEAT OK cairn=<file>
lines=<n>`, where `<n>` is the cairn's line count, and then the line a fresh window restarts
from: `RESTART: exit this window; the next window boots from <cairn>`. Replays:
`beat-appends-the-queue-section`, `beat-appends-a-second-section`,
`beat-without-git-still-says-ok`.
