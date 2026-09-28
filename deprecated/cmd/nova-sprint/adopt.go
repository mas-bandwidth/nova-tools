// The adopt verb (nova-tools #3186, the matrix slice): adoption receipts per
// verb per POV live in Redis (adopt:<verb>, internal/nsprint/adopt), and the
// matrix and its x/y line are read from them, never from a hand-kept file.
// receipt is one FCALL (ns_adopt_receipt), matrix and status one FCALL_RO
// (ns_adopt_matrix). Every subverb first loads the function library when the
// store has none (fn.LoadMissing: a fresh Redis works; a newer library is
// never replaced). Exit 0 done, 1 refused with the remedy named, 2 usage.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/adopt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "adopt",
		Summary: "receipt|matrix|status: adoption receipts per verb per POV in Redis (adopt:<verb>), and the matrix read from them",
		Run:     runAdopt,
	})
}

func runAdopt(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "adopt", "want receipt, matrix or status")
	}
	sub := args[0]
	switch sub {
	case "receipt", "matrix", "status":
	default:
		return refuse(errOut, "adopt", fmt.Sprintf("unknown subverb %s; want receipt, matrix or status", sub))
	}
	name := "adopt " + sub
	fs, addr := lifeFlags(name)
	var r adopt.Receipt
	var verb string
	if sub == "receipt" {
		fs.StringVar(&verb, "verb", "", "the verb adopted, e.g. \"nova-sprint land stream\"")
		fs.StringVar(&r.Who, "as", "", "the seat writing the receipt; default NOVA_FRIEND")
		fs.StringVar(&r.POV, "pov", "", "coordinator, bench, reader or friend")
		fs.StringVar(&r.State, "state", "", "adopted, adopted-gaps, in-flight, unexercised, blocked or hack")
		fs.StringVar(&r.Gap, "gap", "", "the issue holding the gap: <repo>#<n>")
		fs.StringVar(&r.Hand, "hand", "", "the hand step the verb replaces")
		fs.StringVar(&r.Note, "note", "", "one line of evidence")
	}
	md := fs.Bool("md", false, "matrix as the hacks-to-verbs markdown table")
	tsv := fs.Bool("tsv", false, "matrix as tab-separated rows")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, name, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, name, "takes flags, not positional arguments")
	}
	if (*md || *tsv) && sub != "matrix" {
		return refuse(errOut, name, "--md and --tsv belong to matrix")
	}
	if *md && *tsv {
		return refuse(errOut, name, "--md and --tsv are exclusive")
	}
	if sub == "receipt" {
		r.Verb = adopt.CleanVerb(verb)
		if r.Who == "" {
			r.Who = os.Getenv(seatEnv)
		}
		if err := r.Check(); err != nil {
			return refuse(errOut, name, err.Error())
		}
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, name, err.Error())
	}
	defer st.Close()
	c := st.Client()
	if err := fn.LoadMissing(ctx, c); err != nil {
		return refuse(errOut, name, err.Error())
	}

	if sub == "receipt" {
		res, err := adopt.Write(ctx, c, r)
		if err != nil {
			return refuse(errOut, name, err.Error())
		}
		switch res.Outcome {
		case adopt.Refused:
			remedy := "fix the receipt and write it again"
			if res.Reason == "no-gap" {
				remedy = "file the gap as an issue and pass --gap <repo>#<n>"
			}
			fmt.Fprintf(errOut, "ADOPT REFUSED reason=%s verb=%s; remedy: %s\n", oneline.Field(res.Reason), oneline.Quote(r.Verb), remedy)
			return 1
		case adopt.Unchanged:
			fmt.Fprintf(out, "ADOPT UNCHANGED verb=%s who=%s pov=%s at=%d\n", oneline.Quote(r.Verb), oneline.Field(r.Who), r.POV, res.At)
			return 0
		}
		gap := r.Gap
		if gap == "" {
			gap = "-"
		}
		fmt.Fprintf(out, "ADOPT RECEIPT verb=%s who=%s pov=%s state=%s gap=%s at=%d receipts=%d\n",
			oneline.Quote(r.Verb), oneline.Field(r.Who), r.POV, r.State, oneline.Field(gap), res.At, res.Receipts)
		return 0
	}

	rows, err := adopt.Matrix(ctx, c)
	if err != nil {
		return refuse(errOut, name, err.Error())
	}
	switch {
	case sub == "status":
		fmt.Fprintf(out, "ADOPT STATUS %s\n", adopt.Status(rows))
	case *md:
		fmt.Fprint(out, adopt.Markdown(rows))
	case *tsv:
		fmt.Fprint(out, adopt.TSV(rows))
	default:
		for _, row := range rows {
			fmt.Fprintln(out, row.Line())
		}
		fmt.Fprintf(out, "ADOPT MATRIX verbs=%d %s\n", len(rows), adopt.Status(rows))
	}
	return 0
}
