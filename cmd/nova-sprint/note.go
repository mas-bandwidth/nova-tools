// The note verb's MERGE-NOTE form (nova-tools #4324, part 4): typed lines
// on a stream's or the sprint's record, rendered into every copy's brief at
// deal (internal/nsprint/note). The same verb's --rote form is rote.go.
//
//	nova-sprint note post --stream <s>|--sprint <S> --by <who> [--redis <addr>] <text...>
//	nova-sprint note ls   --stream <s>|--sprint <S> [--redis <addr>]
//	nova-sprint note drop --stream <s>|--sprint <S> [--redis <addr>]
//
// post appends one line (NOTE POSTED key=<k> n=<count>); ls prints each
// (NOTE <line>) and a count; drop deletes the list (NOTE DROPPED key=<k>
// n=<count>). A stream's notes also expire when its landing merges (land
// merge drops them: the merge card that wrote them landed). Exit 0 done, 2
// refused or usage, 6 no Redis.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/note"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The `note` verb is registered by rote.go (`note --rote <class>`, #3110);
// cmdNote hands post, ls and drop here.

// isMergeNoteSub reports whether args name a MERGE-NOTE subverb.
func isMergeNoteSub(args []string) bool {
	return len(args) > 0 && (args[0] == "post" || args[0] == "ls" || args[0] == "drop")
}

func runMergeNote(ctx context.Context, args []string, out, errOut io.Writer) int {
	sub := args[0]
	verb := "note " + sub
	fs := taskFlags(verb)
	streamName := fs.String("stream", "", "")
	sprint := fs.String("sprint", "", "")
	by := fs.String("by", "", "")
	redisAddr := fs.String("redis", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	var key string
	switch {
	case *streamName != "" && *sprint != "":
		return refuse(errOut, verb, "one of --stream <s> or --sprint <S>, not both")
	case *streamName != "":
		key = note.StreamKey(*streamName)
	case *sprint != "":
		key = note.SprintKey(*sprint)
	default:
		return refuse(errOut, verb, "needs --stream <s> or --sprint <S>")
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	switch sub {
	case "post":
		if *by == "" {
			return refuse(errOut, verb, "needs --by <who> (the merge card or friend posting)")
		}
		if text == "" {
			return refuse(errOut, verb, "needs the note text after the flags: one line")
		}
	default:
		if text != "" {
			return refuse(errOut, verb, "takes flags, not "+oneline.Field(text))
		}
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	c := st.Client()
	switch sub {
	case "post":
		n, err := note.Post(ctx, c, key, *by, text, time.Now())
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		fmt.Fprintf(out, "NOTE POSTED key=%s n=%d by=%s\n", oneline.Field(key), n, oneline.Field(*by))
	case "ls":
		lines, err := note.Load(ctx, c, key)
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
			return 1
		}
		for _, l := range lines {
			fmt.Fprintf(out, "NOTE %s\n", oneline.Escape(l))
		}
		fmt.Fprintf(out, "NOTES key=%s n=%d\n", oneline.Field(key), len(lines))
	case "drop":
		n, err := note.Drop(ctx, c, key)
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
			return 1
		}
		fmt.Fprintf(out, "NOTE DROPPED key=%s n=%d\n", oneline.Field(key), n)
	}
	return 0
}
