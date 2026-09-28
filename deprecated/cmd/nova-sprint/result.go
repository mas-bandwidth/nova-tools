package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

func init() {
	register(Verb{
		Name:    "result",
		Summary: "validate, inspect, and check RESULT v2 records and DISPOSITION v1 claims",
		Run:     runResult,
	})
}

func runResult(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "result", "wants check, show, disposition, or contract")
	}
	switch args[0] {
	case "contract":
		return runResultContract(args[1:], out, errOut)
	case "check":
		return runResultCheck(args[1:], out, errOut)
	case "show":
		return runResultShow(ctx, args[1:], out, errOut)
	case "disposition":
		return runResultDisposition(args[1:], out, errOut)
	default:
		return refuse(errOut, "result", fmt.Sprintf("unknown subcommand %q; wants check, show, disposition, or contract", args[0]))
	}
}

func runResultContract(args []string, out, errOut io.Writer) int {
	fs := verbflag.New("result contract")
	_ = fs.Bool("markdown", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "result contract", err.Error())
	}
	fmt.Fprint(out, typedrec.Contract.Markdown())
	return 0
}

func runResultCheck(args []string, out, errOut io.Writer) int {
	fs := verbflag.New("result check")
	kind := fs.String("kind", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "result check", err.Error())
	}
	if *kind == "" {
		return refuse(errOut, "result check", "--kind <fix|recut|port|docs-guard|report|read> is required")
	}
	targetArgs := fs.Args()
	if len(targetArgs) == 0 {
		return refuse(errOut, "result check", "wants a file path or - for stdin")
	}

	var raw []byte
	var err error
	if targetArgs[0] == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(targetArgs[0])
	}
	if err != nil {
		return refuse(errOut, "result check", err.Error())
	}

	res := typedrec.ParseResult(raw, typedrec.ParseOptions{ExpectedKind: *kind})
	if res.Valid {
		fmt.Fprintf(out, "RESULT VALID kind=%s schema=%s status=%s check=%s attempt=%d\n",
			res.Kind, res.Schema, res.Status, res.Check, res.Attempt)
		return 0
	}

	fmt.Fprintf(out, "RESULT REFUSED field=%s defect=%s line=%d\n", res.Field, res.Defect, res.Line)
	return 2
}

func runResultShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("result show")
	sprint := fs.String("sprint", "", "")
	redisAddr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "result show", err.Error())
	}
	if *sprint == "" {
		return refuse(errOut, "result show", "--sprint is required")
	}
	labels := fs.Args()
	if len(labels) == 0 {
		return refuse(errOut, "result show", "wants at least one card label")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintf(errOut, "result show: redis %v\n", err)
		return 6
	}
	defer st.Close()

	client := st.Client()

	// Pipeline: read card hashes first to get current attempts and facts
	pipe := client.Pipeline()
	cardCmds := make([]*redis.MapStringStringCmd, len(labels))
	for i, l := range labels {
		cardCmds[i] = pipe.HGetAll(ctx, "s:"+*sprint+":card:"+l)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		fmt.Fprintf(errOut, "result show: pipeline read %v\n", err)
		return 6
	}

	// Second pipeline: read result hashes for each label and its attempt
	pipe2 := client.Pipeline()
	resultCmds := make([]*redis.MapStringStringCmd, len(labels))
	cards := make([]map[string]string, len(labels))
	for i, l := range labels {
		c, err := cardCmds[i].Result()
		if err != nil || len(c) == 0 {
			c = make(map[string]string)
		}
		cards[i] = c
		att := c["attempt"]
		if att == "" {
			att = "1"
		}
		resultCmds[i] = pipe2.HGetAll(ctx, "s:"+*sprint+":card:"+l+":result:a"+att)
	}
	if _, err := pipe2.Exec(ctx); err != nil && err != redis.Nil {
		fmt.Fprintf(errOut, "result show: pipeline read results %v\n", err)
		return 6
	}

	for i := range labels {
		res, _ := resultCmds[i].Result()
		card := cards[i]

		valid := res["valid"]
		if valid == "1" {
			fmt.Fprintf(out, "valid=1 schema=%s c_check=%s v_branch=%s\n",
				res["schema"], res["c_check"], res["v_branch"])
			if pr := card["pr"]; pr != "" && pr != "0" {
				fmt.Fprintf(out, "HARVESTED pr=%s\n", pr)
			}
		} else if valid == "0" {
			fmt.Fprintf(out, "valid=0 field=%s defect=%s line=%s\n",
				res["field"], res["defect"], res["line"])
		} else {
			// No result hash found or incomplete
			field := res["field"]
			defect := res["defect"]
			if defect == "" {
				defect = "missing"
			}
			fmt.Fprintf(out, "valid=0 field=%s defect=%s line=%s\n",
				field, defect, res["line"])
		}
	}
	return 0
}

func runResultDisposition(args []string, out, errOut io.Writer) int {
	verbflag.HelpIfAsked(args, "result disposition")
	if len(args) == 0 {
		return refuse(errOut, "result disposition", "wants a file path or - for stdin")
	}
	var raw []byte
	var err error
	if args[0] == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(args[0])
	}
	if err != nil {
		return refuse(errOut, "result disposition", err.Error())
	}

	claims := typedrec.ParseDispositionClaims(raw)
	for _, c := range claims {
		wholeInt := 0
		if c.Whole {
			wholeInt = 1
		}
		fmt.Fprintf(out, "DISPOSITION CLAIM who=%s head=%s verdict=%s score=%s whole=%d authority=%s\n",
			c.Who, c.Head, c.Verdict, c.Score, wholeInt, c.Authority)
	}
	return 0
}
