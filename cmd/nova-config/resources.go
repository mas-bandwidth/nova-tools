package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

type machineResources struct {
	Tags      []string       `json:"tags"`
	Resources map[string]int `json:"resources"`
}

type resourcesView struct {
	Machines  map[string]machineResources `json:"machines"`
	Tags      map[string][]string         `json:"tags"`
	Resources map[string][]string         `json:"resources"`
}

const resourcesHint = "resources flags: [--json], --pg <dsn>"

func runResources(ctx context.Context, args []string, d deps, stdout io.Writer) error {
	fs, words, err := flags("resources", resourcesHint, args, func(fs *flag.FlagSet) {
		fs.Bool("json", false, "print JSON object")
	})
	if err != nil {
		return err
	}
	if len(words) > 0 {
		return usagef("resources", resourcesHint, "unexpected words %q", words)
	}
	st, err := open(ctx, fs, d)
	if err != nil {
		return err
	}
	defer st.Close()
	rows, err := st.List(ctx, kinds["machine"])
	if err != nil {
		return err
	}
	view := resourcesView{
		Machines:  map[string]machineResources{},
		Tags:      map[string][]string{},
		Resources: map[string][]string{},
	}
	for _, r := range rows {
		m := r.Name
		var tags []string
		if r.F["tags"] != "" {
			for _, t := range strings.Split(r.F["tags"], ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
		}
		os := r.F["os"]
		if os == "darwin" {
			os = "mac"
		}
		if os != "" {
			tags = append(tags, os)
		}
		if r.F["arch"] != "" {
			tags = append(tags, r.F["arch"])
		}
		slices.Sort(tags)
		tags = slices.Compact(tags)

		res := map[string]int{}
		if r.F["resources"] != "" && r.F["resources"] != "{}" {
			_ = json.Unmarshal([]byte(r.F["resources"]), &res) // ignored: canonical checked the field
		}
		if n, err := strconv.Atoi(r.F["cpu_units"]); err == nil && n >= 1 {
			res["cpu"] = n
		}
		if n, err := strconv.Atoi(r.F["memory_gb"]); err == nil && n >= 1 {
			res["memory"] = n
		}

		view.Machines[m] = machineResources{Tags: tags, Resources: res}
		for _, t := range tags {
			view.Tags[t] = append(view.Tags[t], m)
		}
		for k := range res {
			view.Resources[k] = append(view.Resources[k], m)
		}
	}

	if str(fs, "json") == "true" {
		b, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\n", b)
		return err
	}

	for _, r := range rows {
		mr := view.Machines[r.Name]
		tStr := "-"
		if len(mr.Tags) > 0 {
			tStr = strings.Join(mr.Tags, ",")
		}
		rStr := "-"
		if len(mr.Resources) > 0 {
			var pairs []string
			for _, k := range slices.Sorted(maps.Keys(mr.Resources)) {
				pairs = append(pairs, fmt.Sprintf("%s=%d", k, mr.Resources[k]))
			}
			rStr = strings.Join(pairs, ",")
		}
		fmt.Fprintf(stdout, "MACHINE name=%s tags=%s resources=%s\n", value(r.Name), value(tStr), value(rStr))
	}
	for _, t := range slices.Sorted(maps.Keys(view.Tags)) {
		fmt.Fprintf(stdout, "TAG name=%s machines=%s\n", value(t), value(strings.Join(view.Tags[t], ",")))
	}
	for _, r := range slices.Sorted(maps.Keys(view.Resources)) {
		fmt.Fprintf(stdout, "RESOURCE name=%s machines=%s\n", value(r), value(strings.Join(view.Resources[r], ",")))
	}
	fmt.Fprintf(stdout, "CONFIG RESOURCES machines=%d tags=%d resources=%d\n", len(rows), len(view.Tags), len(view.Resources))
	return nil
}
