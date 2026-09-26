package reconcile_test

import (
	"context"
)

// refusingBench is a fake `ssh` to a bench whose card wrapper refuses every
// launch, the way sprint quack-0925 ran (2026-09-25 03:23-03:35Z: the bench
// ACL answered NOPERM on ws:* inside the first card move). The session
// reaches `card launch --stdin`, which prints the secrets banner on stderr,
// one REFUSED line per batch line on stdout (and on stderr), and exits 1.
// Every session appends one line to sessions.log. It lives in t.TempDir(), so
// testguard sees a fake.
const refusingBench = `#!/bin/bash
set -u
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    *) break ;;
  esac
done
echo "open $1" >> %q
echo "SECRETS EXEC OK as=ctl-cap keys=1 only=1 required=1 cmd=nova-sprint" >&2
n=0
while read -r s label attempt token; do
  n=$((n+1))
  line="REFUSED line=$n wrapper $s/$label/$attempt: REFUSED card launched NOPERM No permissions to access a key (ws:swarm: cards:working)"
  echo "$line"
  echo "$line" >&2
done
echo "LAUNCH started=0 refused=$n ms=3 over=false"
exit 1
`

type capFence string

func (f capFence) Token(context.Context) (string, error) { return string(f), nil }
