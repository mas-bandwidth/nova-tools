// nova-sprint classify consumes non-DONE card ends from Redis. Classification
// itself has no forge client: dependency evidence is read atomically by the
// Redis function and follow-up tips use the injected git-only Tip seam.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{Name: "classify",
		Summary: "classify --redis <addr> --sprint <S> [--consumer <name>] [--once]: classify non-DONE card ends",
		Run:     runClassify})
}

func runClassify(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("classify")
	addr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	consumer := fs.String("consumer", "", "")
	once := fs.Bool("once", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "classify", err.Error())
	}
	if fs.NArg() != 0 || *addr == "" || *sprint == "" {
		return refuse(errOut, "classify", "needs --redis <addr> and --sprint <S>, with no positional arguments")
	}
	if *consumer == "" {
		*consumer = "classify-" + strconv.Itoa(os.Getpid())
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint classify: %v\n", err)
		return 6
	}
	defer st.Close()
	classifier := &consume.Classifier{Store: st, Sprint: *sprint, Consumer: *consumer,
		Actor: "nova-sprint classify", Tip: classifyTip}
	if !*once {
		if err := classifier.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(errOut, "nova-sprint classify: %v\n", err)
			return 2
		}
		return 0
	}
	classifier.Block = -1
	rows, err := classifier.PassResults(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint classify: %v\n", err)
		return 2
	}
	for _, row := range rows {
		result := row.Result
		if result == "" {
			result = "UNRESOLVED"
		}
		if i := strings.IndexByte(result, ' '); i >= 0 && !strings.HasPrefix(result, "SKIP ") {
			result = result[:i]
		}
		fmt.Fprintf(out, "%s %s/%s -> %s\n", row.Label, row.Outcome, row.Reason, result)
	}
	return 0
}
