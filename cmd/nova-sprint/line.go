package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
)

func runLine() error {
	fs := flag.NewFlagSet("line", flag.ExitOnError)
	var (
		card     = fs.String("card", "", "card id")
		repo     = fs.String("repo", "", "repo")
		n        = fs.String("n", "", "pr number")
		head     = fs.String("head", "", "head sha")
		who      = fs.String("who", "", "who")
		kind     = fs.String("kind", "", "kind (SCORE, HOLD, REPAIR, SPEC, SPEC-WRITTEN, CLOSE)")
		score    = fs.String("score", "", "score")
		gates    = fs.String("gates", "", "gates")
		bodyFile = fs.String("body-file", "", "body file")
	)
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	cmd := fs.Arg(0)
	switch cmd {
	case "post":
		if *card == "" || *repo == "" || *n == "" || *head == "" || *who == "" || *kind == "" {
			return fmt.Errorf("missing required flags")
		}
		var body string
		if *bodyFile != "" {
			b, err := os.ReadFile(*bodyFile)
			if err != nil {
				return err
			}
			body = string(b)
		} else {
			b, _ := io.ReadAll(os.Stdin)
			body = string(b)
		}
		r := line.Record{
			Card:  *card,
			Repo:  *repo,
			N:     *n,
			Head:  *head,
			Who:   *who,
			Kind:  *kind,
			Score: *score,
			Gates: *gates,
			Body:  body,
		}
		return line.Post("", r)
	case "list":
		// nova-sprint line list --repo <r> --n <n> --head <sha>
		records, err := line.ListByHead("", *repo, *n, *head)
		if err != nil {
			return err
		}
		for _, r := range records {
			fmt.Printf("%s\t%s\n", r.Kind, r.Who)
		}
	}
	return nil
}
