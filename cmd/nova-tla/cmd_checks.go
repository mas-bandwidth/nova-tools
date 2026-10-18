package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// findRedis resolves the redis-server of a verb and echoes what it found.
func findRedis(e env, verb, flag string) (string, *tool.Out) {
	path, err := tlc.FindHelper("redis-server", flag, e.lookPath)
	if err != nil {
		return "", refuse(err.Error()+"; nothing was run", name+" "+verb+" --redis-server /path/to/redis-server ...")
	}
	event(e.stdout, "HELPER", "OK", "name", "redis-server", "path", path)
	return path, nil
}

// suiteVerb is the body of the table and member verbs: plan the steps, run them
// under one budget, print one event per step and a closing line.
func suiteVerb(e env, c *tool.Call, token, verb string, plan func() ([]tablemodel.Step, error), fields []string) *tool.Out {
	root := c.Str("root")
	steps, err := plan()
	if err != nil {
		return refuse(err.Error(), name+" "+verb+" -h")
	}
	jar, java, o := findTLC(e, verb, c.Str("jar"), c.Str("java"))
	if o != nil {
		return o
	}
	start := e.now()
	results, err := tablemodel.RunSteps(steps, tablemodel.StepOptions{
		Jar: jar.Path, Java: java, Models: filepath.Join(root, "tla"), Dir: c.Str("dir"), Budget: c.Dur("timeout"), Exec: e.exec,
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
		return refuse("the run could not be completed: "+err.Error(), name+" "+verb+" -h")
	}
	closing := append([]string{}, fields...)
	closing = append(closing, "cases", fmt.Sprint(len(results)), "seconds", seconds(e.now().Sub(start)))
	if len(results) < len(steps) || (len(results) > 0 && !results[len(results)-1].OK) {
		event(e.stderr, token, "FAIL", closing...)
		return tool.Exit(1)
	}
	event(e.stdout, token, "OK", closing...)
	return tool.Exit(0)
}

func cmdTable(e env, c *tool.Call) *tool.Out {
	mode := c.Str("mode")
	return suiteVerb(e, c, "TABLE", "table", func() ([]tablemodel.Step, error) { return tablemodel.TableSteps(mode) }, []string{"mode", mode})
}

func cmdMember(e env, c *tool.Call) *tool.Out {
	suite, workers := c.Str("suite"), c.Int("workers")
	return suiteVerb(e, c, "MEMBER", "member", func() ([]tablemodel.Step, error) { return tablemodel.MemberSteps(suite, workers) }, []string{"suite", suite})
}

func cmdReplay(e env, c *tool.Call) *tool.Out {
	models := c.Str("models")
	if models == "" {
		models = filepath.Join(c.Str("root"), "tla")
	}
	jar, java, o := findTLC(e, "replay", c.Str("jar"), c.Str("java"))
	if o != nil {
		return o
	}
	redisServer, o := findRedis(e, "replay", c.Str("redis-server"))
	if o != nil {
		return o
	}
	start := e.now()
	err := tablemodel.RunReplay(tablemodel.ReplayOptions{
		Source: c.Str("source"), Jar: jar.Path, Java: java, Models: models, Dir: c.Str("dir"), Budget: c.Dur("timeout"),
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
			return refuse(cannot.Error()+"; nothing was checked", name+" replay -h")
		}
		eventWhy(e.stderr, "REPLAY", "FAIL", err.Error(), "seconds", seconds(e.now().Sub(start)))
		return tool.Exit(1)
	}
	event(e.stdout, "REPLAY", "OK", "seconds", seconds(e.now().Sub(start)))
	return tool.Exit(0)
}

func splitField(f string) (string, string) {
	for i := 0; i < len(f); i++ {
		if f[i] == '=' {
			return f[:i], f[i+1:]
		}
	}
	return f, ""
}

func cmdWitnesses(e env, c *tool.Call, source string) *tool.Out {
	redisServer, o := findRedis(e, "witnesses", c.Str("redis-server"))
	if o != nil {
		return o
	}
	ctx, cancel := context.WithTimeout(c.Ctx, c.Dur("timeout"))
	defer cancel()
	findings := 0
	err := tablemodel.RunWitnesses(ctx, source, tablemodel.ServerOptions{RedisServer: redisServer, TmpDir: e.tmpDir}, func(f tablemodel.Finding) {
		findings++
		writeLine(e.stdout, "WITNESS OK result="+f.Result+" name="+f.Name+": "+oneLine(f.Text))
	})
	if err != nil {
		var cannot *tablemodel.CannotRun
		if errors.As(err, &cannot) {
			return refuse(cannot.Error()+"; nothing was checked", name+" witnesses -h")
		}
		eventWhy(e.stderr, "WITNESS", "FAIL", err.Error(), "findings", fmt.Sprint(findings))
		return tool.Exit(1)
	}
	event(e.stdout, "WITNESS", "OK", "findings", fmt.Sprint(findings))
	return tool.Exit(0)
}
