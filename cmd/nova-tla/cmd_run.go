package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// findTLC resolves the jar and java of a verb and echoes what it found; a
// refusal is returned when either is missing.
func findTLC(e env, verb, jarFlag, javaFlag string) (tlc.Jar, string, *tool.Out) {
	jar, err := tlc.FindJar(jarFlag, e.getenv)
	if err != nil {
		return tlc.Jar{}, "", refuse(err.Error()+"; nothing was run", name+" "+verb+" --jar /path/to/tla2tools.jar ...")
	}
	java, err := tlc.FindHelper("java", javaFlag, e.lookPath)
	if err != nil {
		return tlc.Jar{}, "", refuse(err.Error()+"; nothing was run", name+" "+verb+" --java /path/to/java ...")
	}
	event(e.stdout, "HELPER", "OK", "name", "jar", "path", jar.Path, "source", jar.Source, "sha256", jar.SHA256)
	event(e.stdout, "HELPER", "OK", "name", "java", "path", java)
	return jar, java, nil
}

// checkRunner refuses a binary whose runner is not the tree's (runner.go): what
// it calls stale, and the fingerprints it writes, would be wrong with no sign of
// it.
func checkRunner(e env, verb, root string) *tool.Out {
	if err := checkTreeRunner(root, e.built); err != nil {
		return refuse(err.Error(), name+" help runner")
	}
	return nil
}

func loadCases(verb, root string) ([]tlc.Case, *tool.Out) {
	cases, err := tlc.LoadCases(root)
	if err != nil {
		return nil, refuse("the case plan is refused: "+err.Error(), name+" "+verb+" --root <a checkout with tla/CASES.tsv> ...")
	}
	return cases, nil
}

// cmdGroups prints the groups as one line of JSON: the payload, so --json
// carries the same line as its payload.
func cmdGroups(e env, c *tool.Call) *tool.Out {
	root := c.Str("root")
	cases, o := loadCases("groups", root)
	if o != nil {
		return o
	}
	groups := tlc.RequiredGroups(cases)
	if c.Bool("stale") {
		if o := checkRunner(e, "groups", root); o != nil {
			return o
		}
		src, err := tlc.SourceAt(root)
		if err != nil {
			return refuse(err.Error(), name+" groups -h")
		}
		records, err := readRuns(root)
		if err != nil {
			return refuse("cannot read the records: "+err.Error()+layoutHint(err), name+" groups -h")
		}
		if groups, err = tlc.StaleGroups(src, cases, records); err != nil {
			return refuse(err.Error(), name+" groups -h")
		}
	}
	raw, err := json.Marshal(groups)
	if err != nil {
		return refuse(err.Error(), name+" groups -h")
	}
	return tool.Payload(string(raw))
}

func cmdRun(e env, c *tool.Call) *tool.Out {
	root, dir, group := c.Str("root"), c.Str("dir"), c.Str("group")
	timeout, workers, manual := c.Dur("timeout"), c.Int("workers"), c.Bool("manual")
	cases, o := loadCases("run", root)
	if o != nil {
		return o
	}
	if o := checkRunner(e, "run", root); o != nil {
		return o
	}
	if manual && (e.getenv("GITHUB_ACTIONS") == "true" || e.getenv("NOVA_CI") == "1") {
		return refuse("--manual is forbidden in CI; every workflow job stays under two minutes", name+" run --group <group> ... without --manual")
	}
	if err := tlc.CheckLimits(timeout, workers, manual); err != nil {
		return refuse(err.Error(), name+" run -h")
	}
	if bench := c.Str("bench"); bench != "" {
		jar := c.Str("jar")
		if jar == "" {
			jar = defaultBenchJar
		}
		return runOnBench(c.Ctx, e, benchOpts{
			root: root, dir: dir, machine: bench, jar: jar, java: c.Str("java"), group: group,
			timeout: timeout, workers: workers, manual: manual,
			loadBelow: c.Get("load-below").(float64), troughWait: c.Dur("trough-wait"), troughPoll: c.Dur("trough-poll"), cases: cases,
		})
	}
	if e.goos != "linux" {
		return refuse("TLC runs on a Linux bench, and this is "+e.goos+"; nothing was run", name+" run --bench any ... (a record machine runs it), or the Linux CI job")
	}
	shards, shard := c.Int("shards"), c.Int("shard")
	chosen, err := tlc.Select(cases, group, shards, shard)
	if err != nil {
		return refuse(err.Error(), name+" groups --root "+root)
	}
	width := c.Int("parallel")
	if width == 0 {
		width = max(1, e.cpus()/4)
	}
	if err := tlc.CheckParallel(width, e.cpus()); err != nil {
		return refuse(err.Error()+"; nothing was run", name+" run --parallel 1 ...")
	}
	jar, java, o := findTLC(e, "run", c.Str("jar"), c.Str("java"))
	if o != nil {
		return o
	}
	platform, err := tlc.Platform(e.goos, e.goarch)
	if err != nil {
		return refuse(err.Error()+"; nothing was run", "ssh <bench> "+name+" run ...")
	}
	javaVer, err := e.javaVersion(java)
	if err != nil {
		return refuse("the java version cannot be read: "+err.Error()+"; nothing was run", name+" run --java /path/to/java ...")
	}
	event(e.stdout, "HELPER", "OK", "name", "java-version", "version", javaVer)
	res, err := tlc.RunSuite(tlc.Options{
		Root: root, Cases: chosen, Jar: jar, Java: java, JavaVer: javaVer, Out: dir,
		Selection: tlc.Selection{Group: group, Shards: shards, Shard: shard},
		Budget:    timeout, Workers: workers, Parallel: width, Manual: manual, Platform: platform, CPUs: e.cpus(), Exec: e.exec,
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
		return refuse("the run could not be completed: "+err.Error(), name+" run -h")
	}
	records := filepath.Join(dir, tlc.RunsFile)
	switch {
	case res.Refused != "":
		eventWhy(e.stderr, "RUN", "FAIL", res.Refused+"; no records were written; run the same command again", "cases", fmt.Sprint(len(res.Records)))
		return tool.Exit(1)
	case res.Failed && len(res.Records) != len(chosen):
		eventWhy(e.stderr, "RUN", "FAIL", "the budget ended before the last case", "cases", fmt.Sprint(len(res.Records)), "of", fmt.Sprint(len(chosen)), "records", records)
		return tool.Exit(1)
	case res.Failed:
		eventWhy(e.stderr, "RUN", "FAIL", "a case did not reach the result CASES.tsv declares", "cases", fmt.Sprint(len(res.Records)), "records", records)
		return tool.Exit(1)
	}
	event(e.stdout, "RUN", "OK", "cases", fmt.Sprint(len(res.Records)), "records", records)
	return tool.Exit(0)
}

// doMerge joins the records files into out, keeping keep's current records of
// the cases no file measured again; run --bench ends in it too.
func doMerge(e env, root, out, keep string, files []string) *tool.Out {
	cases, o := loadCases("merge", root)
	if o != nil {
		return o
	}
	if o := checkRunner(e, "merge", root); o != nil {
		return o
	}
	src, err := tlc.SourceAt(root)
	if err != nil {
		return refuse(err.Error(), name+" merge --root <a checkout with tla/CASES.tsv> ...")
	}
	var runs [][]tlc.Record
	for _, p := range files {
		recs, err := tlc.ReadRecordsFile(p)
		if err != nil {
			return refuse("cannot read records: "+err.Error(), name+" merge -h")
		}
		runs = append(runs, recs)
	}
	measured := len(runs)
	var dropped []tlc.Dropped
	if keep != "" {
		base, err := tlc.ReadRecordsFile(keep)
		if err != nil {
			return refuse("cannot read the records to keep: "+err.Error()+layoutHint(err), name+" merge -h")
		}
		var carried []tlc.Record
		carried, dropped, err = tlc.Carry(src, cases, base, runs...)
		if err != nil {
			return refuse(err.Error(), name+" merge -h")
		}
		runs = append(runs, carried)
		for _, d := range dropped {
			if d.Why == tlc.DroppedGone {
				event(e.stdout, "DROP", "OK", "config", d.Config, "why", d.Why, "keep", keep)
			}
		}
	}
	merged, err := tlc.Merge(src, cases, runs...)
	if err != nil {
		var missing *tlc.MissingError
		if errors.As(err, &missing) {
			err = errors.New(explainMissing(missing, dropped, root, keep, out))
		}
		eventWhy(e.stderr, "MERGE", "FAIL", err.Error(), "runs", fmt.Sprint(measured))
		return tool.Exit(1)
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return refuse(err.Error(), name+" merge -h")
	}
	var b bytes.Buffer
	if err := tlc.WriteRecords(&b, merged); err != nil {
		return refuse(err.Error(), name+" merge -h")
	}
	if err := atomicfile.WriteFile(abs, b.Bytes(), 0o644); err != nil {
		return refuse("cannot write the records: "+err.Error(), name+" merge --out <a file in an existing directory> ...")
	}
	event(e.stdout, "MERGE", "OK", "runs", fmt.Sprint(measured), "records", fmt.Sprint(len(merged)), "out", abs)
	return tool.Exit(0)
}

// readRuns reads root/tla/RUNS.tsv. A tree with no records file has measured
// nothing yet, so it reads as no records and every required group is stale (a
// repository that has just adopted nova-tla, nova-sprint's form of it); a file
// that cannot be read is an error.
func readRuns(root string) ([]tlc.Record, error) {
	path := filepath.Join(root, "tla", tlc.RunsFile)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return tlc.ReadRecordsFile(path)
}

// layoutHint is what to do about records in another layout than this tool's.
func layoutHint(err error) string {
	if !strings.Contains(err.Error(), "another layout") {
		return ""
	}
	return "; take the tla/RUNS.tsv of the base branch, which is in the layout this tool writes, or run every group and merge without --keep"
}

// explainMissing says why a merge has no record for some declared cases: a kept
// record that is stale is named as stale, with its group and the commands that
// measure it again, and the rest are named as missing.
func explainMissing(missing *tlc.MissingError, dropped []tlc.Dropped, root, keep, out string) string {
	staleGroup := map[string]string{}
	for _, d := range dropped {
		if d.Why == tlc.DroppedStale {
			staleGroup[d.Config] = d.Group
		}
	}
	var stale, absent, groups []string
	seen := map[string]bool{}
	for _, c := range missing.Cases {
		if g, ok := staleGroup[c]; ok {
			stale = append(stale, c+" (group "+g+")")
			if !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
			continue
		}
		absent = append(absent, c)
	}
	sort.Strings(groups)
	var parts []string
	if len(stale) > 0 {
		var cmds []string
		for _, g := range groups {
			cmds = append(cmds, name+" run --root "+root+" --jar <jar> --dir <a clean directory>/"+g+" --group "+g)
		}
		parts = append(parts, fmt.Sprintf("%d kept records are stale, measured on inputs their cases no longer read: %s; run their groups again, each into a clean directory (%s), then merge again with --keep %s and those runs",
			len(stale), strings.Join(stale, ", "), strings.Join(cmds, "; "), keep))
	}
	if len(absent) > 0 {
		parts = append(parts, (&tlc.MissingError{Cases: absent}).Error())
	}
	return strings.Join(parts, "; and ")
}

func cmdInputs(e env, root, given string) *tool.Out {
	cases, o := loadCases("inputs", root)
	if o != nil {
		return o
	}
	config := given
	if !strings.HasSuffix(config, ".cfg") {
		config += ".cfg"
	}
	known := false
	for _, c := range cases {
		known = known || c.Config == config
	}
	if !known {
		return refuse("no case "+oneline.Quote(given)+" in tla/CASES.tsv (the config column names them)", name+" groups --root "+root)
	}
	if o := checkRunner(e, "inputs", root); o != nil {
		return o
	}
	src, err := tlc.SourceAt(root)
	if err != nil {
		return refuse(err.Error(), name+" inputs -h")
	}
	inputs, err := src.Inputs(config)
	if err != nil {
		return refuse("the inputs of the case cannot be read: "+err.Error(), name+" inputs -h")
	}
	for _, in := range inputs {
		event(e.stdout, "INPUT", "OK", "path", in.Path, "sha256", in.SHA256)
	}
	event(e.stdout, "INPUTS", "OK", "case", config, "files", fmt.Sprint(len(inputs)), "fingerprint", tlc.Digest(inputs))
	return tool.Exit(0)
}
