package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runLandFlaky(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "land flaky", "want list or observe")
	}
	switch args[0] {
	case "list":
		return runLandFlakyList(ctx, args[1:], out, errOut)
	case "observe":
		return runLandFlakyObserve(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "land flaky", "want list or observe")
	}
}

func runLandFlakyList(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("land flaky list")
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "land flaky list", err.Error())
	}
	if *addr == "" || len(fs.Args()) > 0 {
		return refuse(errOut, "land flaky list", "needs --redis <addr> and optional --repo <r>")
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land flaky list: %v\n", err)
		return 6
	}
	defer st.Close()
	rows, err := land.NewRedisStore(st.Client(), "list").List(ctx, *repo)
	if storeDown(errOut, "land flaky list", err) {
		return 6
	}
	if err != nil {
		return refuse(errOut, "land flaky list", err.Error())
	}
	for _, row := range rows {
		r := row.Record
		fmt.Fprintf(out, "FLAKY %s lanes_hit=%d issue=#%d first_seen=%s last_at=%s last_lane=%s\n", row.Key, r.LanesHit, r.Issue, r.FirstSeen, r.LastAt, r.LastLane)
	}
	return 0
}

func runLandFlakyObserve(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("land flaky observe")
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	pkg := fs.String("pkg", "", "the Go package the flaky test is in")
	test := fs.String("test", "", "the flaky test's name")
	lane := fs.String("lane", "", "the lane that observed it")
	api := fs.String("forge-api", gh.DefaultAPI, "the forge's REST base url")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "land flaky observe", err.Error())
	}
	if len(fs.Args()) > 0 || *addr == "" || *sprint == "" || *repo == "" || *pkg == "" || *test == "" || *lane == "" {
		return refuse(errOut, "land flaky observe", "needs --redis, --sprint, --repo, --pkg, --test and --lane")
	}
	for name, v := range map[string]string{"repo": *repo, "pkg": *pkg, "test": *test} {
		if strings.Contains(v, ":") {
			return refuse(errOut, "land flaky observe", "--"+name+" must not contain ':'")
		}
	}
	tok, err := envGitHubToken()
	if err != nil {
		return refuse(errOut, "land flaky observe", err.Error())
	}
	if tok == "" {
		return refuse(errOut, "land flaky observe", "needs a GitHub token: a seats.tsv row naming the seat's token env (seventh column), or GH_TOKEN or GITHUB_TOKEN")
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land flaky observe: %v\n", err)
		return 6
	}
	defer st.Close()
	key := land.FlakyKey(*repo, *pkg, *test)
	title := fmt.Sprintf("flaky: %s.%s fails a gate batch while every member passes alone", *pkg, *test)
	body := fmt.Sprintf("dedup=%s\n\nObserved by nova-sprint land flaky on lane %s.\n", key, *lane)
	rec, filed, err := land.NewRedisStore(st.Client(), *sprint).ObserveLive(ctx, land.Observation{Repo: *repo, Key: key, Lane: *lane, Title: title, Body: body}, land.RESTFiler{BaseURL: *api, Token: tok, Redis: st.Client()})
	if err != nil {
		var pending *land.PendingError
		if errors.As(err, &pending) {
			fmt.Fprintf(out, "FLAKY PENDING %s (%v)\n", key, pending.Err)
			return 1
		}
		if storeDown(errOut, "land flaky observe", err) {
			return 6
		}
		return refuse(errOut, "land flaky observe", err.Error())
	}
	switch rec.Status {
	case "FILED":
		suffix := ""
		if rec.Reconciled {
			suffix = " reconciled"
		}
		fmt.Fprintf(out, "FLAKY FILED %s issue=#%d%s\n", key, rec.Issue, suffix)
		_ = filed
		return 0
	case "SEEN":
		fmt.Fprintf(out, "FLAKY SEEN %s lanes_hit=%d issue=#%d\n", key, rec.LanesHit, rec.Issue)
		return 0
	case "FILING":
		fmt.Fprintf(out, "FLAKY FILING %s (lock held)\n", key)
		return 0
	case "DUPLICATE":
		fmt.Fprintf(out, "FLAKY DUPLICATE %s issue=#%d extra=#%d\n", key, rec.Issue, rec.Extra)
		return 3
	default:
		return refuse(errOut, "land flaky observe", "unexpected store outcome "+rec.Status)
	}
}
