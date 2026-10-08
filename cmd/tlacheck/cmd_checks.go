package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

const defaultChecksTimeout = 2 * time.Minute

// findRedis resolves the redis-server of a verb and echoes what it found.
func findRedis(e env, verb, flag string) (string, int, bool) {
	path, err := tlc.FindHelper("redis-server", flag, e.lookPath)
	if err != nil {
		return "", refuse(e, verb, err.Error()+"; nothing was run", tool+" "+verb+" --redis-server /path/to/redis-server ..."), true
	}
	event(e.stdout, "HELPER", "OK", "name", "redis-server", "path", path)
	return path, 0, false
}

// suiteVerb is the body of the table and member verbs: plan the steps, run them
// under one budget, print one event per step and a closing line.
func suiteVerb(e env, token, verb string, plan func() ([]tablemodel.Step, error), fields []string, root, jarFlag, javaFlag, dir string, timeout time.Duration) int {
	if code, stop := missing(e, verb, "dir", dir); stop {
		return code
	}
	if timeout <= 0 {
		return refuse(e, verb, "--timeout must be positive", tool+" "+verb+" -h")
	}
	steps, err := plan()
	if err != nil {
		return refuse(e, verb, err.Error(), tool+" "+verb+" -h")
	}
	jar, java, code, stop := findTLC(e, verb, jarFlag, javaFlag)
	if stop {
		return code
	}
	start := time.Now()
	results, err := tablemodel.RunSteps(steps, tablemodel.StepOptions{
		Jar: jar.Path, Java: java, Models: filepath.Join(root, "tla"), Dir: dir, Budget: timeout, Exec: e.exec,
		OnStep: func(r tablemodel.StepResult) {
			status, w := "OK", e.stdout
			if !r.OK {
				status, w = "FAIL", e.stderr
			}
			event(w, token, status, "case", r.Step.Name, "verdict", r.Verdict, "seconds", seconds(r.Seconds),
				"generated", r.Outcome.Generated, "distinct", r.Outcome.Distinct, "log", r.Log)
		},
	})
	if err != nil {
		return refuse(e, verb, "the run could not be completed: "+err.Error(), tool+" "+verb+" -h")
	}
	closing := append([]string{}, fields...)
	closing = append(closing, "cases", fmt.Sprint(len(results)), "seconds", seconds(time.Since(start)))
	if len(results) < len(steps) || (len(results) > 0 && !results[len(results)-1].OK) {
		event(e.stderr, token, "FAIL", closing...)
		return 1
	}
	event(e.stdout, token, "OK", closing...)
	return 0
}

func cmdTable(e env, args []string) int {
	fs := flags("table")
	root := fs.String("root", ".", "")
	jar := fs.String("jar", "", "")
	java := fs.String("java", "", "")
	dir := fs.String("dir", "", "")
	mode := fs.String("mode", "strict", "")
	timeout := fs.Duration("timeout", defaultChecksTimeout, "")
	if done, code := parse(e, "table", fs, args, helpTable); done {
		return code
	}
	return suiteVerb(e, "TABLE", "table", func() ([]tablemodel.Step, error) { return tablemodel.TableSteps(*mode) },
		[]string{"mode", *mode}, *root, *jar, *java, *dir, *timeout)
}

func cmdMember(e env, args []string) int {
	fs := flags("member")
	root := fs.String("root", ".", "")
	jar := fs.String("jar", "", "")
	java := fs.String("java", "", "")
	dir := fs.String("dir", "", "")
	suite := fs.String("suite", "all", "")
	workers := fs.Int("workers", 4, "")
	timeout := fs.Duration("timeout", defaultChecksTimeout, "")
	if done, code := parse(e, "member", fs, args, helpMember); done {
		return code
	}
	return suiteVerb(e, "MEMBER", "member", func() ([]tablemodel.Step, error) { return tablemodel.MemberSteps(*suite, *workers) },
		[]string{"suite", *suite}, *root, *jar, *java, *dir, *timeout)
}

func cmdReplay(e env, args []string) int {
	fs := flags("replay")
	root := fs.String("root", ".", "")
	source := fs.String("source", "", "")
	models := fs.String("models", "", "")
	jarFlag := fs.String("jar", "", "")
	javaFlag := fs.String("java", "", "")
	redis := fs.String("redis-server", "", "")
	dir := fs.String("dir", "", "")
	timeout := fs.Duration("timeout", defaultChecksTimeout, "")
	if done, code := parse(e, "replay", fs, args, helpReplay); done {
		return code
	}
	if code, stop := missing(e, "replay", "source", *source, "dir", *dir); stop {
		return code
	}
	if *timeout <= 0 {
		return refuse(e, "replay", "--timeout must be positive", tool+" replay -h")
	}
	if *models == "" {
		*models = filepath.Join(*root, "tla")
	}
	jar, java, code, stop := findTLC(e, "replay", *jarFlag, *javaFlag)
	if stop {
		return code
	}
	redisServer, code, stop := findRedis(e, "replay", *redis)
	if stop {
		return code
	}
	start := time.Now()
	err := tablemodel.RunReplay(tablemodel.ReplayOptions{
		Source: *source, Jar: jar.Path, Java: java, Models: *models, Dir: *dir, Budget: *timeout,
		Server: tablemodel.ServerOptions{RedisServer: redisServer, TmpDir: e.tmpDir}, Exec: e.exec,
		OnStep: func(s tablemodel.ReplayEvent) {
			kv := []string{"step", s.Step, "verdict", s.Verdict}
			for _, f := range s.Fields {
				k, v := splitField(f)
				kv = append(kv, k, v)
			}
			event(e.stdout, "REPLAY", "OK", kv...)
		},
	})
	if err != nil {
		var cannot *tablemodel.CannotRun
		if errors.As(err, &cannot) {
			return refuse(e, "replay", cannot.Error()+"; nothing was checked", tool+" replay -h")
		}
		eventWhy(e.stderr, "REPLAY", "FAIL", err.Error(), "seconds", seconds(time.Since(start)))
		return 1
	}
	event(e.stdout, "REPLAY", "OK", "seconds", seconds(time.Since(start)))
	return 0
}

func splitField(f string) (string, string) {
	for i := 0; i < len(f); i++ {
		if f[i] == '=' {
			return f[:i], f[i+1:]
		}
	}
	return f, ""
}

func cmdWitnesses(e env, args []string) int {
	fs := flags("witnesses")
	redis := fs.String("redis-server", "", "")
	timeout := fs.Duration("timeout", defaultChecksTimeout, "")
	if done, code := parse(e, "witnesses", fs, args, helpWitnesses); done {
		return code
	}
	if fs.NArg() != 1 {
		return refuse(e, "witnesses", fmt.Sprintf("want exactly one table.lua, got %d", fs.NArg()), tool+" witnesses -h")
	}
	if *timeout <= 0 {
		return refuse(e, "witnesses", "--timeout must be positive", tool+" witnesses -h")
	}
	redisServer, code, stop := findRedis(e, "witnesses", *redis)
	if stop {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	findings := 0
	err := tablemodel.RunWitnesses(ctx, fs.Arg(0), tablemodel.ServerOptions{RedisServer: redisServer, TmpDir: e.tmpDir}, func(f tablemodel.Finding) {
		findings++
		fmt.Fprintf(e.stdout, "WITNESS OK result=%s name=%s: %s\n", f.Result, f.Name, oneLine(f.Text))
	})
	if err != nil {
		var cannot *tablemodel.CannotRun
		if errors.As(err, &cannot) {
			return refuse(e, "witnesses", cannot.Error()+"; nothing was checked", tool+" witnesses -h")
		}
		eventWhy(e.stderr, "WITNESS", "FAIL", err.Error(), "findings", fmt.Sprint(findings))
		return 1
	}
	event(e.stdout, "WITNESS", "OK", "findings", fmt.Sprint(findings))
	return 0
}
