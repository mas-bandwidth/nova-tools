package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// findTLC resolves the jar and java of a verb and echoes what it found.
func findTLC(e env, verb, jarFlag, javaFlag string) (tlc.Jar, string, int, bool) {
	jar, err := tlc.FindJar(jarFlag, e.getenv)
	if err != nil {
		return tlc.Jar{}, "", refuse(e, verb, err.Error()+"; nothing was run", tool+" "+verb+" --jar /path/to/tla2tools.jar ..."), true
	}
	java, err := tlc.FindHelper("java", javaFlag, e.lookPath)
	if err != nil {
		return tlc.Jar{}, "", refuse(e, verb, err.Error()+"; nothing was run", tool+" "+verb+" --java /path/to/java ..."), true
	}
	event(e.stdout, "HELPER", "OK", "name", "jar", "path", jar.Path, "source", jar.Source, "sha256", jar.SHA256)
	event(e.stdout, "HELPER", "OK", "name", "java", "path", java)
	return jar, java, 0, false
}

func loadCases(e env, verb, root string) ([]tlc.Case, int, bool) {
	cases, err := tlc.LoadCases(root)
	if err != nil {
		return nil, refuse(e, verb, "the case plan is refused: "+err.Error(), tool+" "+verb+" --root <a nova-tools checkout> ..."), true
	}
	return cases, 0, false
}

func cmdGroups(e env, args []string) int {
	fs := flags("groups")
	root := fs.String("root", ".", "")
	stale := fs.Bool("stale", false, "")
	if done, code := parse(e, "groups", fs, args, helpGroups); done {
		return code
	}
	cases, code, stop := loadCases(e, "groups", *root)
	if stop {
		return code
	}
	groups := tlc.RequiredGroups(cases)
	if *stale {
		src, err := tlc.SourceAt(*root)
		if err != nil {
			return refuse(e, "groups", err.Error(), tool+" groups -h")
		}
		records, err := tlc.ReadRecordsFile(filepath.Join(*root, "tla", tlc.RunsFile))
		if err != nil {
			return refuse(e, "groups", "cannot read the records: "+err.Error(), tool+" groups -h")
		}
		if groups, err = tlc.StaleGroups(src, cases, records); err != nil {
			return refuse(e, "groups", err.Error(), tool+" groups -h")
		}
	}
	raw, err := json.Marshal(groups)
	if err != nil {
		return refuse(e, "groups", err.Error(), tool+" groups -h")
	}
	fmt.Fprintln(e.stdout, string(raw))
	return 0
}

func cmdRun(e env, args []string) int {
	fs := flags("run")
	root := fs.String("root", ".", "")
	jarFlag := fs.String("jar", "", "")
	javaFlag := fs.String("java", "", "")
	dir := fs.String("dir", "", "")
	group := fs.String("group", "", "")
	shards := fs.Int("shards", 1, "")
	shard := fs.Int("shard", 0, "")
	timeout := fs.Duration("timeout", tlc.BoundedCap, "")
	workers := fs.Int("workers", 2, "")
	manual := fs.Bool("manual", false, "")
	if done, code := parse(e, "run", fs, args, helpRun); done {
		return code
	}
	if code, stop := missing(e, "run", "dir", *dir); stop {
		return code
	}
	cases, code, stop := loadCases(e, "run", *root)
	if stop {
		return code
	}
	if *manual && (e.getenv("GITHUB_ACTIONS") == "true" || e.getenv("NOVA_CI") == "1") {
		return refuse(e, "run", "--manual is forbidden in CI; every workflow job stays under two minutes", tool+" run --group <group> ... without --manual")
	}
	if e.goos != "linux" {
		return refuse(e, "run", "TLC runs on a Linux bench, and this is "+e.goos+"; nothing was run", "ssh <bench> "+tool+" run ... (or the Linux CI job)")
	}
	if err := tlc.CheckLimits(*timeout, *workers, *manual); err != nil {
		return refuse(e, "run", err.Error(), tool+" run -h")
	}
	chosen, err := tlc.Select(cases, *group, *shards, *shard)
	if err != nil {
		return refuse(e, "run", err.Error(), tool+" groups --root "+*root)
	}
	jar, java, code, stop := findTLC(e, "run", *jarFlag, *javaFlag)
	if stop {
		return code
	}
	host, err := e.hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	res, err := tlc.RunSuite(tlc.Options{
		Root: *root, Cases: chosen, Jar: jar, Java: java, Out: *dir,
		Selection: tlc.Selection{Group: *group, Shards: *shards, Shard: *shard},
		Budget:    *timeout, Workers: *workers, Manual: *manual, Host: host, Exec: e.exec,
		OnCase: func(r tlc.Record) {
			status, w := "OK", e.stdout
			if r.Result != "PASS" {
				status, w = "FAIL", e.stderr
			}
			event(w, "CASE", status, "config", r.Config, "result", r.Result, "seconds", r.Seconds,
				"exit", fmt.Sprint(r.Exit), "generated", r.Generated, "distinct", r.Distinct)
		},
	})
	if err != nil {
		return refuse(e, "run", "the run could not be completed: "+err.Error(), tool+" run -h")
	}
	records := filepath.Join(*dir, tlc.RunsFile)
	switch {
	case res.Refused != "":
		eventWhy(e.stderr, "RUN", "FAIL", res.Refused+"; no records were written; run the same command again", "cases", fmt.Sprint(len(res.Records)))
		return 1
	case res.Failed && len(res.Records) != len(chosen):
		eventWhy(e.stderr, "RUN", "FAIL", "the budget ended before the last case", "cases", fmt.Sprint(len(res.Records)), "of", fmt.Sprint(len(chosen)), "records", records)
		return 1
	case res.Failed:
		eventWhy(e.stderr, "RUN", "FAIL", "a case did not reach the result CASES.tsv declares", "cases", fmt.Sprint(len(res.Records)), "records", records)
		return 1
	}
	event(e.stdout, "RUN", "OK", "cases", fmt.Sprint(len(res.Records)), "records", records)
	return 0
}

func cmdMerge(e env, args []string) int {
	fs := flags("merge")
	root := fs.String("root", ".", "")
	out := fs.String("out", "", "")
	keep := fs.String("keep", "", "")
	if done, code := parse(e, "merge", fs, args, helpMerge); done {
		return code
	}
	if code, stop := missing(e, "merge", "out", *out); stop {
		return code
	}
	if fs.NArg() == 0 && *keep == "" {
		return refuse(e, "merge", "no records files named", tool+" merge --out "+*out+" <RUNS.tsv>...")
	}
	cases, code, stop := loadCases(e, "merge", *root)
	if stop {
		return code
	}
	src, err := tlc.SourceAt(*root)
	if err != nil {
		return refuse(e, "merge", err.Error(), tool+" merge --root <a nova-tools checkout> ...")
	}
	var runs [][]tlc.Record
	for _, p := range fs.Args() {
		recs, err := tlc.ReadRecordsFile(p)
		if err != nil {
			return refuse(e, "merge", "cannot read records: "+err.Error(), tool+" merge -h")
		}
		runs = append(runs, recs)
	}
	measured := len(runs)
	if *keep != "" {
		base, err := tlc.ReadRecordsFile(*keep)
		if err != nil {
			return refuse(e, "merge", "cannot read the records to keep: "+err.Error(), tool+" merge -h")
		}
		carried, err := tlc.Carry(src, cases, base, runs...)
		if err != nil {
			return refuse(e, "merge", err.Error(), tool+" merge -h")
		}
		runs = append(runs, carried)
	}
	merged, err := tlc.Merge(src, cases, runs...)
	if err != nil {
		eventWhy(e.stderr, "MERGE", "FAIL", err.Error(), "runs", fmt.Sprint(measured))
		return 1
	}
	abs, err := filepath.Abs(*out)
	if err != nil {
		return refuse(e, "merge", err.Error(), tool+" merge -h")
	}
	var b bytes.Buffer
	if err := tlc.WriteRecords(&b, merged); err != nil {
		return refuse(e, "merge", err.Error(), tool+" merge -h")
	}
	if err := atomicfile.WriteFile(abs, b.Bytes(), 0o644); err != nil {
		return refuse(e, "merge", "cannot write the records: "+err.Error(), tool+" merge --out <a file in an existing directory> ...")
	}
	event(e.stdout, "MERGE", "OK", "runs", fmt.Sprint(measured), "records", fmt.Sprint(len(merged)), "out", abs)
	return 0
}

func cmdInputs(e env, args []string) int {
	fs := flags("inputs")
	root := fs.String("root", ".", "")
	name := fs.String("case", "", "")
	if done, code := parse(e, "inputs", fs, args, helpInputs); done {
		return code
	}
	if code, stop := missing(e, "inputs", "case", *name); stop {
		return code
	}
	if fs.NArg() != 0 {
		return refuse(e, "inputs", "unexpected argument "+oneline.Quote(fs.Arg(0)), tool+" inputs -h")
	}
	cases, code, stop := loadCases(e, "inputs", *root)
	if stop {
		return code
	}
	config := *name
	if !strings.HasSuffix(config, ".cfg") {
		config += ".cfg"
	}
	known := false
	for _, c := range cases {
		known = known || c.Config == config
	}
	if !known {
		return refuse(e, "inputs", "no case "+oneline.Quote(*name)+" in tla/CASES.tsv (the config column names them)", tool+" inputs -h")
	}
	src, err := tlc.SourceAt(*root)
	if err != nil {
		return refuse(e, "inputs", err.Error(), tool+" inputs -h")
	}
	inputs, err := src.Inputs(config)
	if err != nil {
		return refuse(e, "inputs", "the inputs of the case cannot be read: "+err.Error(), tool+" inputs -h")
	}
	for _, in := range inputs {
		event(e.stdout, "INPUT", "OK", "path", in.Path, "sha256", in.SHA256)
	}
	event(e.stdout, "INPUTS", "OK", "case", config, "files", fmt.Sprint(len(inputs)), "fingerprint", tlc.Digest(inputs))
	return 0
}
