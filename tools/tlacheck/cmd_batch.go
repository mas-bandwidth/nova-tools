package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/batchmodel"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

func cmdBatchReplay(e env, args []string) int {
	fs := flags("batch-replay")
	root := fs.String("root", ".", "")
	source := fs.String("source", "", "")
	models := fs.String("models", "", "")
	jarFlag := fs.String("jar", "", "")
	javaFlag := fs.String("java", "", "")
	redis := fs.String("redis-server", "", "")
	dir := fs.String("dir", "", "")
	timeout := fs.Duration("timeout", tlc.BoundedCap, "")
	if done, code := parse(e, "batch-replay", fs, args, helpBatchReplay); done {
		return code
	}
	if code, stop := missing(e, "batch-replay", "source", *source, "dir", *dir); stop {
		return code
	}
	if fs.NArg() != 0 {
		return refuse(e, "batch-replay", fmt.Sprintf("want no positionals, got %d", fs.NArg()), tool+" batch-replay -h")
	}
	if e.goos != "linux" {
		return refuse(e, "batch-replay", "batch TLC checks run on a Linux bench, and this is "+e.goos+"; nothing was run", "ssh <bench> "+tool+" batch-replay ...")
	}
	if *timeout <= 0 || *timeout > tlc.BoundedCap {
		return refuse(e, "batch-replay", "the timeout must be positive and at most "+tlc.BoundedCap.String(), tool+" batch-replay -h")
	}
	if *models == "" {
		*models = filepath.Join(*root, "tla")
	}
	outDir, err := filepath.Abs(*dir)
	if err != nil {
		return refuse(e, "batch-replay", "cannot resolve output directory: "+err.Error(), tool+" batch-replay -h")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	start := time.Now()
	jar, java, code, stop := findTLC(e, "batch-replay", *jarFlag, *javaFlag)
	if stop {
		return code
	}
	redisServer, code, stop := findRedis(e, "batch-replay", *redis)
	if stop {
		return code
	}
	capture := e.capture
	if capture == nil {
		capture = batchmodel.CaptureSuite
	}
	cases, err := capture(ctx, batchmodel.CaptureSuiteOptions{
		Source: *source, RedisServer: redisServer, ModelsDir: *models, OutputDir: outDir, TmpDir: e.tmpDir,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			eventWhy(e.stderr, "BATCH", "FAIL", "the total budget expired during capture", "cases", "0", "seconds", seconds(time.Since(start)))
			return 1
		}
		var cannot *tablemodel.CannotRun
		if errors.As(err, &cannot) {
			return refuse(e, "batch-replay", cannot.Error()+"; no histories were checked", tool+" batch-replay -h")
		}
		eventWhy(e.stderr, "BATCH", "FAIL", "capture did not produce a checkable suite: "+err.Error(), "cases", "0", "seconds", seconds(time.Since(start)))
		return 1
	}
	if len(cases) == 0 {
		return refuse(e, "batch-replay", "capture produced no histories", tool+" batch-replay -h")
	}
	for i, tc := range cases {
		if err := ctx.Err(); err != nil {
			eventWhy(e.stderr, "BATCH", "FAIL", "the total budget expired before the next case", "case", tc.Name, "seconds", seconds(time.Since(start)))
			return 1
		}
		config, err := os.ReadFile(tc.Bundle.Config)
		if err != nil {
			return refuse(e, "batch-replay", "cannot read generated config: "+err.Error(), tool+" batch-replay -h")
		}
		log := filepath.Join(outDir, tc.Name+".log")
		javaTmp := filepath.Join(outDir, "java-tmp")
		if err := os.MkdirAll(javaTmp, 0o700); err != nil {
			return refuse(e, "batch-replay", "cannot create Java temporary directory: "+err.Error(), tool+" batch-replay -h")
		}
		exec := e.exec
		if exec == nil {
			exec = tlc.Execute
		}
		r := tlc.Run{
			Java: java, Jar: jar.Path, Dir: tc.Bundle.Work,
			JVM:    []string{"-Xmx512m", "-XX:ActiveProcessorCount=2", "-XX:+UseParallelGC"},
			TmpDir: javaTmp, Workers: 1,
			LnCheckFinal: true, Config: filepath.Base(tc.Bundle.Config), Module: filepath.Base(tc.Bundle.Module),
		}
		code := exec(ctx, r, log)
		raw, readErr := os.ReadFile(log)
		if readErr != nil {
			return refuse(e, "batch-replay", "cannot read TLC log: "+readErr.Error(), tool+" batch-replay -h")
		}
		caseExpected := tc.Expected
		if caseExpected == "invariant-reject" {
			caseExpected = "invariant"
		}
		accepted := tlc.Accepts(tlc.Case{Expected: caseExpected, Property: tc.Property}, code, string(raw), string(config))
		accepted = accepted && ctx.Err() == nil
		if tc.Name == "corrupt-observation" && (tc.Expected != "invariant-reject" || tc.Property != "MatchesExecution") {
			accepted = false
		}
		outcome := tlc.Parse(string(raw))
		status, writer := "OK", e.stdout
		if !accepted {
			status, writer = "FAIL", e.stderr
		}
		verdict := caseExpected
		if len(outcome.Violations) > 0 {
			verdict = outcome.Violations[0].Kind + ":" + outcome.Violations[0].Name
		} else if outcome.Completed {
			verdict = "pass"
		} else if code == tlc.ExitTimeout {
			verdict = "timeout"
		}
		event(writer, "BATCH", status, "case", tc.Name, "verdict", verdict, "exit", fmt.Sprint(code),
			"generated", outcome.Generated, "distinct", outcome.Distinct,
			"lua_sha256", tc.SourceSHA256, "trace_sha256", tc.Bundle.SourceSHA256, "config_sha256", tc.Bundle.ConfigSHA256,
			"evidence_sha256", tc.EvidenceSHA256, "log", log, "evidence", tc.Bundle.Evidence,
			"suite", filepath.Join(outDir, "suite.json"))
		if !accepted {
			if code == tlc.ExitNoStart {
				return refuse(e, "batch-replay", "TLC could not start; inspect the case log", tool+" batch-replay --java /path/to/java --jar /path/to/tla2tools.jar ...")
			}
			eventWhy(e.stderr, "BATCH", "FAIL", "a case did not reach its expected TLC result", "cases", fmt.Sprint(i+1), "seconds", seconds(time.Since(start)))
			return 1
		}
	}
	event(e.stdout, "BATCH", "OK", "cases", fmt.Sprint(len(cases)), "seconds", seconds(time.Since(start)))
	return 0
}
