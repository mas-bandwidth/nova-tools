// nova-sprint digest (nova-tools#3158): what landed, which holds were routed
// and which reads were scored in [since, until), from Redis alone
// (internal/nsprint/digest): ws:log, land:<repo>:events and the pr:<name>:<n>
// records. No GitHub, no model.
//
//	nova-sprint digest --redis <host:port> --since <RFC3339 UTC> [--until <RFC3339 UTC>] [--repo <owner/name>]...
//
// --until defaults to now. --repo adds an event stream to read besides the
// ones a landing in the window names. Exit 0 printed, 1 a Redis read
// failed, 2 usage or no Redis (refused before any read).
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/digest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "digest",
		Summary: "landed, holds and reads in a window, from ws:log, land:<repo>:events and pr records (no GitHub)",
		Run:     runDigest,
	})
}

// repoList is --repo, repeatable.
type repoList []string

func (r *repoList) String() string { return strings.Join(*r, ",") }
func (r *repoList) Set(v string) error {
	if !landRepoOK(v) {
		return fmt.Errorf("--repo %q is not <owner>/<name>", v)
	}
	*r = append(*r, v)
	return nil
}

// digestNow is the default --until; a test pins it.
var digestNow = time.Now

func runDigest(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("digest")
	addr := fs.String("redis", redisDefault(), "")
	sinceS := fs.String("since", "", "")
	untilS := fs.String("until", "", "")
	var repos repoList
	fs.Var(&repos, "repo", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "digest", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "digest", "takes flags, not positional arguments")
	}
	if *addr == "" {
		return refuse(errOut, "digest", "wants --redis <host:port>")
	}
	since, err := time.Parse(time.RFC3339, *sinceS)
	if err != nil || !strings.HasSuffix(*sinceS, "Z") {
		return refuse(errOut, "digest", "wants --since <RFC3339 UTC>")
	}
	until := digestNow().UTC().Truncate(time.Millisecond)
	if *untilS != "" {
		if until, err = time.Parse(time.RFC3339, *untilS); err != nil || !strings.HasSuffix(*untilS, "Z") {
			return refuse(errOut, "digest", "wants --until <RFC3339 UTC>")
		}
	}
	if !since.Before(until) {
		return refuse(errOut, "digest", "since must be before until")
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(errOut, "digest", fmt.Sprintf("redis %s: %v", *addr, err))
	}
	defer st.Close()
	d, err := digest.Read(ctx, st.Client(), digest.Options{Since: since, Until: until, Repos: repos})
	if store.Unreachable(err) {
		// Open sends nothing (#3277): the first read is the probe, and an
		// unreachable store is the same refusal and exit as before.
		return refuse(errOut, "digest", fmt.Sprintf("redis %s: %v", *addr, err))
	}
	if err != nil {
		refuse(errOut, "digest", fmt.Sprintf("read: %v", err))
		return 1
	}
	if err := digest.Render(out, d); err != nil {
		return 1
	}
	return 0
}
