package read

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/spec"
	"github.com/redis/go-redis/v9"
)

// postSpec is Post for a SPEC line (nova-tools#3370): the line and its facts
// (who, rev, score, stream) go to Redis in one ns_spec_mark call, which moves
// the spec working -> done on the second distinct 10 at the current rev and
// releases the builds waiting on spec:<repo>#<n>. A spec issue has no head,
// so none is required. The comment mirror follows the Redis write and is
// skipped when the fact was already recorded (SAME).
func postSpec(ctx context.Context, c *redis.Client, repo, n, line string, poster *Poster, stdout, stderr io.Writer) int {
	f, err := spec.ParseLine(line)
	if err != nil {
		fmt.Fprintf(stderr, "READ POST REFUSED repo=%s n=%s why=%v\n", repo, n, err)
		return 1
	}
	r, err := spec.Do(ctx, c, spec.Mark{Repo: repo, N: n, Who: f.Who, Rev: f.Rev, Score: f.Score, Stream: f.Stream, Line: line, Actor: f.Who})
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint read post: %v\n", err)
		return 2
	}
	if r.ExitCode() != 0 {
		fmt.Fprintf(stderr, "READ POST REFUSED repo=%s n=%s kind=SPEC spec=%s why=%s; nothing written\n", repo, n, r.Answer, r.Why)
		return 1
	}
	facts := fmt.Sprintf("kind=SPEC spec=%s state=%s tens=%d released=%d stream=%s lines=%d", r.Answer, r.State, r.Tens, r.Released, r.Stream, r.Lines)
	if poster == nil || r.Answer == "SAME" {
		fmt.Fprintf(stdout, "READ POST repo=%s n=%s %s github_calls=0\n", repo, n, facts)
		return 0
	}
	id, err := poster.Comment(ctx, repo, n, line)
	if err != nil {
		fmt.Fprintf(stderr, "READ POST REFUSED repo=%s n=%s %s redis=ok github=%v; the line is in Redis, re-run with --no-github or fix the token\n", repo, n, facts, err)
		return 1
	}
	fmt.Fprintf(stdout, "READ POST repo=%s n=%s %s github_calls=1 comment=%d\n", repo, n, facts, id)
	return 0
}
