// The ci verb (#2756 4.8 and section 10, nova-tools #2936) registers itself
// through the S0 registry, so it never edits main.go. cut, rerun and dispose
// are one guarded Redis Function call each; show and status only read.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "ci",
		Summary: "cut, show, rerun, dispose and status of ci cards and the ci:<repo>:<sha> verdict",
		Run:     runCI,
	})
}

const ciUsage = "want cut, show, rerun, dispose or status"

func runCI(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "ci", ciUsage)
	}
	switch args[0] {
	case "cut":
		return runCICut(ctx, args[1:], out, errOut)
	case "show":
		return runCIShow(ctx, args[1:], out, errOut)
	case "rerun":
		return runCIRerun(ctx, args[1:], out, errOut)
	case "dispose":
		return runCIDispose(ctx, args[1:], out, errOut)
	case "status":
		return runCIStatus(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "ci", "unknown subverb "+args[0]+"; "+ciUsage)
	}
}

// splitPositional lets a positional sha sit before or after the flags.
func splitPositional(args []string) (flags []string, pos []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return flags, pos
}

func runCICut(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci cut")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "", "")
	pr := fs.Int("pr", 0, "")
	sha := fs.String("sha", "", "")
	base := fs.String("base", "", "")
	leg := fs.String("leg", "go", "")
	paths := fs.String("paths", "", "")
	actor := fs.String("actor", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	if *sha == "" || *base == "" {
		return refuse(errOut, "ci cut", "needs --sha and --base: the head and base tip read by REST for the PR")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	defer st.Close()
	r, err := ci.Cut(ctx, st, ci.CutRequest{Sprint: *sprint, Repo: *repo, PR: *pr, Head: *sha,
		Base: *base, Leg: *leg, Paths: *paths, Actor: *actor})
	if err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	fmt.Fprintf(out, "%s %s\n", r, ci.Label(*pr, *sha))
	return r.ExitCode()
}

func runCIShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags, pos := splitPositional(args)
	fs := taskFlags("ci show")
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "nova-tools", "")
	_ = fs.String("sprint", "", "")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	if len(pos) != 1 {
		return refuse(errOut, "ci show", "needs one head sha")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	defer st.Close()
	rec, err := ci.Read(ctx, st, *repo, pos[0])
	if err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	return ci.WriteShow(out, rec)
}

func runCIRerun(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags, pos := splitPositional(args)
	fs := taskFlags("ci rerun")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "nova-tools", "")
	pr := fs.Int("pr", 0, "")
	reason := fs.String("reason", "", "")
	actor := fs.String("actor", "", "")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	if len(pos) != 1 || len(pos[0]) != 40 {
		return refuse(errOut, "ci rerun", "needs one full head sha")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	defer st.Close()
	label := ""
	if *pr > 0 {
		label = ci.Label(*pr, pos[0])
	} else {
		rec, err := ci.Read(ctx, st, *repo, pos[0])
		if err != nil {
			return refuse(errOut, "ci rerun", err.Error())
		}
		s, l, ok := strings.Cut(rec.Fields["card"], "/")
		if !ok || s != *sprint {
			return refuse(errOut, "ci rerun", "no ci card for that head in sprint "+*sprint+"; pass --pr")
		}
		label = l
	}
	r, err := ci.Rerun(ctx, st, *sprint, label, *actor, *reason)
	if err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	fmt.Fprintf(out, "%s %s\n", r, label)
	return r.ExitCode()
}

func runCIDispose(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags, pos := splitPositional(args)
	fs := taskFlags("ci dispose")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "nova-tools", "")
	disposition := fs.String("disposition", "", "")
	friend := fs.String("as", "", "")
	url := fs.String("url", "", "")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	if len(pos) != 1 {
		return refuse(errOut, "ci dispose", "needs one head sha")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	defer st.Close()
	r, err := ci.Dispose(ctx, st, *sprint, *repo, pos[0], *disposition, *friend, *url)
	if err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	fmt.Fprintln(out, r)
	return r.ExitCode()
}

func runCIStatus(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci status")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci status", err.Error())
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci status", err.Error())
	}
	defer st.Close()
	s, err := ci.ReadStatus(ctx, st, *sprint)
	if err != nil {
		return refuse(errOut, "ci status", err.Error())
	}
	fmt.Fprintln(out, s)
	return 0
}
