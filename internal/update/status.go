package update

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// statusVerb reads the manifest and lists each entry and its status against the latest,
// showing every entry including current ones.
func statusVerb(name string, args []string, out, errs io.Writer, env Environment) int {
	if env.Now == nil {
		env.Now = time.Now
	}
	o := options{max: 20, timeout: 5 * time.Second, budget: 60 * time.Second}
	asJSON := false
	f := flag.NewFlagSet("status", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.file, "file", "", "the manifest (required): "+manifestShape)
	timeoutWants := "one read's deadline, such as 5s"
	f.DurationVar(&o.timeout, "timeout", o.timeout, timeoutWants)
	f.BoolVar(&asJSON, "json", false, "print the result as one JSON object instead of lines")
	f.DurationVar(&o.budget, "budget", o.budget, "the whole run's deadline, such as 60s")
	f.IntVar(&o.max, "max", 20, "lines listed per kind before one MORE line stands for the rest; 0 lists all")
	f.Var(&o.kinds, "kind", "read only entries of this kind (harness, engine, model, tool or pin); repeat for several")
	help := name + " status -h"
	if err := verbflag.Parse(f, interspersed(f, args)); err != nil {
		return emit(refused("status", help, flagProblem(f, err).Error()), verbflag.BoolAsked(args, "json"), 0, out, errs)
	}
	if f.NArg() != 0 {
		return emit(refused("status", help, fmt.Sprintf("status takes no positional arguments, got %q", f.Arg(0))), asJSON, 0, out, errs)
	}
	var problems []string
	if o.file == "" {
		problems = append(problems, "missing --file; refusing to guess")
	}
	if o.max < 0 {
		problems = append(problems, fmt.Sprintf("--max wants 0 or more (0 shows all), got %d", o.max))
	}
	if o.timeout <= 0 || o.budget <= 0 {
		problems = append(problems, "--timeout and --budget want positive durations")
	}
	if len(problems) > 0 {
		return emit(refused("status", help, strings.Join(problems, "; ")), asJSON, 0, out, errs)
	}
	return emit(checked(name, "status", o, f.Args(), env), asJSON, o.max, out, errs)
}
