// kinds.go holds the kinds verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func runKinds(args []string, stdout, stderr io.Writer) int {
	const verb = "kinds"
	fs := verbflag.New(verb)
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "kinds takes no arguments")
	}
	if *asJSON {
		o := tool.Done().Fact("count", len(config.Kinds))
		o.Verb = verb
		for _, k := range config.Kinds {
			var req []string
			for _, f := range k.Fields {
				if f.Required {
					req = append(req, f.Name)
				}
			}
			rows := "many"
			if k.Singleton {
				rows = "one"
			}
			o.Item("kind", "name", k.Name, "table", "config."+k.Table, "fields", k.FieldNames(), "required", req, "rows", rows, "doc", k.Doc)
		}
		return emit(stdout, o)
	}
	for _, k := range config.Kinds {
		fmt.Fprintln(stdout, config.KindLine(k))
	}
	fmt.Fprintf(stdout, "CONFIG KINDS count=%d\n", len(config.Kinds))
	return 0
}
