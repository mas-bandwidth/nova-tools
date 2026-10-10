// nova-local makes a local model engine usable (docs/SPEC-LOCAL.md): what is here
// (status), one model served at a context the caller chose (serve), and a worker
// description nova-swarm accepts (worker). It runs no inference, fetches no weights and
// judges no model. The dispatch, the banner, the help, the refusals and the output
// envelope are pkg/tool's; the engines (engine.go, ollama.go), the box (box*.go),
// the shared store (store.go), the serving-host rule (host.go) and the worker description
// (description.go) are this package's own files.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

var version string

// world is what the tool reaches outside itself: the HTTP transport every engine request
// goes through, the box's facts, the clock the warm-up is timed on, the name lookup the
// serving-host rule reads, the environment and home the AI root comes from, the stat the
// worker's files are checked with, and the engines. main passes the real one; a test
// passes its own, so no test opens a socket or reads the real clock.
type world struct {
	do       Do
	box      Box
	now      func() time.Time
	lookup   func(string) ([]string, error)
	getenv   func(string) string
	home     string
	stat     func(string) (os.FileInfo, error)
	adapters []Adapter
}

func realWorld() world {
	home, _ := os.UserHomeDir() // ignored: no home is no AI root, which status prints as shared=unknown
	return world{do: http.DefaultClient.Do, box: LocalBox(), now: time.Now, lookup: net.LookupHost,
		getenv: os.Getenv, home: home, stat: os.Stat, adapters: Adapters()}
}

func main() { os.Exit(localTool(realWorld()).Main()) }

func localTool(w world) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-local",
		What:  "run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts",
		Stamp: version,
		How: `an engine (ollama) serves models from the shared store, <ai-root>/shared/models.
status reads each engine and the box; serve makes <name>-<ctx>k, the context baked in, and loads it;
worker writes the one JSON file nova-swarm reads. --base is loopback or a tailnet address only.
It never fetches weights, never runs a prompt and never judges a model.`,
		ExitTable: "0 done, 1 it ran and said no (no engine answers, a model not pulled, a tag that differs, a threshold the caller gave), 2 could not run (a flag or an input).",
		Verbs: []tool.Verb{
			{
				Name:    "status",
				Usage:   "status [--engine <name>] [--base <url>] [--list] [--max <n>] [--timeout <d>]",
				Example: "status",
				Effect:  tool.Inspection + "; it asks each engine over HTTP and reads the box",
				Detail: `One ENGINE line per engine: state=up is a 2xx on its health path inside --timeout;
store= is where its weights are read from, symlinks resolved, and shared=yes when that is under
the AI root's shared/models (NOVA_AI_ROOT, else ~/ai). --list adds one MODEL line per model.

example:
  nova-local status`,
				Flags: func(f *tool.Flags) {
					w.engineFlags(f, false)
					f.Bool("list", false, "list every model as one MODEL line, not only the counts")
					f.Duration("timeout", 2*time.Second, "how long each engine has to answer")
					f.Max()
				},
				Run: w.status,
			},
			{
				Name: "serve",
				Usage: `serve --engine <name> --model <ref> --num-ctx <n> [--base <url>] [--keep-alive <d>] [--seed <n>] [--expect-digest <sha256:...>] [--max-load <f>] [--min-free <size>] [--require-shared-store] [--dry-run]
serve --stop --engine <name> --model <tag> [--base <url>]`,
				Example: "serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7",
				Effect:  tool.Delivery + "; it asks the engine to create the derived tag and load it (--stop unloads it); nothing is written on this machine",
				Detail: `serve makes <name>-<ctx>k (the reference's :tag and registry stripped) with num_ctx, temperature 0
and the seed baked in, reads it back, sends one warm-up and prints serve_as=, the name a harness calls.
An existing tag is used when its parent, num_ctx, temperature and seed match; one differing is exit 1.
The box's load1 and mem_free are read first and printed; --max-load and --min-free refuse past them.

example:
  nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7`,
				DryRun: true,
				Flags: func(f *tool.Flags) {
					w.engineFlags(f, true)
					f.Required("model", "the model: the reference to serve (gemma4:12b), or with --stop the served tag")
					f.Int("num-ctx", 0, "the context in tokens, a multiple of 1024 (required to serve; ollama's own default silently truncates)")
					f.String("keep-alive", "30m", "how long the engine keeps the model loaded after a request")
					f.String("seed", "", "the seed baked into the tag, a whole number; empty bakes none")
					f.String("expect-digest", "", "the digest the model must have (sha256:...); differing is exit 1")
					f.String("max-load", "", "refuse when the one-minute load average is above this number")
					f.String("min-free", "", "refuse when free memory is below this size (16G, 512M)")
					f.Bool("require-shared-store", false, "refuse when the engine's store is not under the AI root's shared/models")
					f.Bool("stop", false, "unload the served tag --model names instead of serving")
					f.Check(func(c *tool.Call) {
						if c.Bool("stop") {
							return
						}
						if p := ContextProblem(c.Int("num-ctx")); p != "" {
							c.Problem(p)
						}
						if s := c.Str("seed"); s != "" {
							if _, err := strconv.Atoi(s); err != nil {
								c.Problem(fmt.Sprintf("--seed wants a whole number (got %q)", s))
							}
						}
						if _, err := parseLoad(c.Str("max-load")); err != nil {
							c.Problem(err.Error())
						}
						if _, err := parseSize(c.Str("min-free")); err != nil {
							c.Problem(err.Error())
						}
					})
				},
				Run: w.serve,
			},
			{
				Name:    "worker",
				Usage:   "worker --engine <name> --model <tag> --out <file> --name <text> --harness <cmd> --harness-args <a,b,{model},...> --worker-dir <abs dir> --key-file <file> --env-var <NAME> --usage <opencode|none> --deadline <d> [--base <url>] [--board <owner/repo#n>] [--dry-run]",
				Example: "worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m",
				Effect:  tool.LocalWrite + "; it writes the --out file and nothing else, after asking the engine that it serves --model",
				DryRun:  true,
				Detail: `The description is nova-swarm's worker schema, every field from a flag or the engine: provider is
the engine's, base_url its --base, model the served tag. The key file is stat'ed, never opened:
the local engine wants no key, and nova-swarm a non-empty file, so one line of any text is the whole of it.

example:
  nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m`,
				Flags: func(f *tool.Flags) {
					w.engineFlags(f, true)
					f.Required("model", "the served tag a harness calls (serve's serve_as=)")
					f.Required("out", "the file the description is written to; its directory must exist")
					f.Required("name", "the name nova-swarm calls this worker by")
					f.Required("harness", "the harness command, found on PATH (opencode)")
					f.Required("harness-args", "the harness's arguments, comma-separated, {model} and {prompt} placed")
					f.Required("worker-dir", "the absolute home directory copied into each slot; it must exist")
					f.Required("key-file", "a non-empty file nova-swarm reads as the key; stat'ed, never opened")
					f.Required("env-var", "the NAME of the variable the provider reads (OLLAMA_API_KEY), never a value")
					f.Required("usage", "opencode (OpenCode's own accounting) or none")
					f.Required("deadline", "the default deadline per task, a Go duration (20m)")
					f.String("board", "", "the board issue the worker reports to, owner/repo#n; empty for none")
				},
				Run: w.worker,
			},
		},
	}
}

// engineFlags declares --engine (required for serve and worker) and --base, and holds
// --base to the serving-host rule (rule 3).
func (w world) engineFlags(f *tool.Flags, required bool) {
	names := w.names()
	if required {
		f.Required("engine", "the engine: one of "+strings.Join(names, ", "))
	} else {
		f.String("engine", "", "only this engine: one of "+strings.Join(names, ", ")+"; empty reports every one")
	}
	f.String("base", "", "the engine's /v1 URL, loopback or a tailnet address; empty is the engine's default")
	f.Check(func(c *tool.Call) {
		e := c.Str("engine")
		if e != "" && w.adapter(e) == nil {
			c.Problem(fmt.Sprintf("--engine wants one of %s (got %q)", strings.Join(names, ", "), e))
		}
		if b := c.Str("base"); b != "" {
			if e == "" {
				c.Problem("--base names one engine's endpoint, so it wants --engine")
			} else if p := BaseProblem(b, w.lookup); p != "" {
				c.Problem(p)
			}
		}
	})
}

func (w world) names() []string {
	var out []string
	for _, a := range w.adapters {
		out = append(out, a.Name())
	}
	return out
}

func (w world) adapter(name string) Adapter {
	for _, a := range w.adapters {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

// client is the engine's endpoint: --base, else its default.
func (w world) client(a Adapter, base string, timeout time.Duration) Client {
	if base == "" {
		base = a.DefaultBase()
	}
	return Client{Base: base, Do: w.do, Timeout: timeout}
}

// store is the engine's store for model, resolved, and whether it is shared: the
// store's words for every line that prints it (rule 15).
func (w world) store(ctx context.Context, a Adapter, c Client, model string) (dir, shared, why string) {
	raw, why := a.Store(ctx, c, model)
	if raw == "" {
		return "unknown", "unknown", why
	}
	dir, err := Resolve(raw)
	if err != nil {
		return "unknown", "unknown", err.Error()
	}
	root := AIRoot(w.getenv, w.home)
	if root == "" {
		return dir, "unknown", "no AI root: set " + AIRootEnv
	}
	resolved, err := Resolve(SharedRoot(root))
	if err != nil {
		return dir, "unknown", err.Error()
	}
	shared, why = Shared(dir, resolved)
	return dir, shared, why
}

// boxFacts are the box's facts, read now, as fields of a first line.
func (w world) boxFacts(o *tool.Out) (load1 float64, free uint64, ok bool) {
	free, total, memOK := uint64(0), uint64(0), false
	if w.box.Mem != nil {
		free, total, memOK = w.box.Mem()
	}
	if memOK {
		o.Fact("mem_used", total-free).Fact("mem_free", free).Fact("mem_total", total)
	} else {
		o.Fact("mem_free", "unknown").Fact("mem_total", "unknown")
	}
	if mib, set := uint64(0), false; w.box.WiredCap != nil {
		if mib, set = w.box.WiredCap(); set {
			o.Fact("wired_cap", mib<<20)
		} else {
			o.Fact("wired_cap", "unset")
		}
	}
	load1, loadOK := 0.0, false
	if w.box.Load1 != nil {
		load1, loadOK = w.box.Load1()
	}
	if loadOK {
		o.Fact("load1", strconv.FormatFloat(load1, 'f', 2, 64))
	} else {
		o.Fact("load1", "unknown")
	}
	return load1, free, loadOK && memOK
}

// status reads every engine (or the one --engine names) and the box (rules 5, 10, 12, 13, 15).
func (w world) status(c *tool.Call) *tool.Out {
	ctx := context.Background()
	o := tool.Done()
	engines, answering, loaded, models := 0, 0, 0, 0
	var items []func()
	for _, a := range w.adapters {
		if e := c.Str("engine"); e != "" && e != a.Name() {
			continue
		}
		engines++
		cl := w.client(a, c.Str("base"), c.Dur("timeout"))
		code, err := a.Health(ctx, cl)
		fields := []any{"name", a.Name()}
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			fields = append(fields, "state", "timeout")
		case err != nil || code/100 != 2:
			fields = append(fields, "state", "down")
			if code != 0 {
				fields = append(fields, "status", code)
			}
		default:
			fields = append(fields, "state", "up")
		}
		fields = append(fields, "base", cl.Base)
		if fields[3] != "up" {
			items = append(items, func() { o.Item("engine", fields...) })
			continue
		}
		answering++
		ms, err := a.Models(ctx, cl)
		if err != nil {
			items = append(items, func() { o.ItemText("engine", err.Error(), fields...) })
			continue
		}
		on, ctxs, first := 0, 0, ""
		for _, m := range ms {
			if first == "" {
				first = m.Ref
			}
			if m.Loaded {
				on++
				ctxs = max(ctxs, m.NumCtx)
			}
		}
		loaded, models = loaded+on, models+len(ms)
		fields = append(fields, "loaded", on, "advertised", len(ms))
		if ctxs > 0 {
			fields = append(fields, "num_ctx", ctxs)
		}
		dir, shared, why := w.store(ctx, a, cl, first)
		fields = append(fields, "store", dir, "shared", shared)
		items = append(items, func() {
			if why != "" {
				o.ItemText("engine", why, fields...)
			} else {
				o.Item("engine", fields...)
			}
		})
		if c.Bool("list") {
			for _, m := range ms {
				mf := []any{"engine", a.Name(), "model", m.Ref, "digest", orNone(m.Digest), "weights", m.Weights, "loaded", yes(m.Loaded)}
				if m.Loaded && m.NumCtx > 0 {
					mf = append(mf, "num_ctx", m.NumCtx)
				}
				items = append(items, func() { o.Item("model", mf...) })
			}
		}
	}
	if answering == 0 {
		f := tool.Fail(fmt.Sprintf("no engine answers (%d asked); start one, such as the ollama daemon", engines))
		f.Remedy = "ollama serve"
		return f
	}
	o.Fact("engines", engines).Fact("answering", answering).Fact("loaded", loaded).Fact("models", models)
	w.boxFacts(o)
	for _, it := range items {
		it()
	}
	return o
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// orNone is a digest as printed: none when the engine reports none (rule 4).
func orNone(d string) string {
	if d == "" {
		return "none"
	}
	return d
}

// serve serves one model at a context (rules 4 to 8 and 15), or unloads one (--stop).
func (w world) serve(c *tool.Call) *tool.Out {
	ctx := context.Background()
	a := w.adapter(c.Str("engine"))
	cl := w.client(a, c.Str("base"), 0)
	model := c.Str("model")
	if c.Bool("stop") {
		dry := c.DryRun()
		if dry {
			return tool.Done().Fact("stopped", "planned").Fact("engine", a.Name()).Fact("model", model)
		}
		already, err := a.Stop(ctx, cl, model)
		if err != nil {
			return tool.Fail("the engine did not unload " + model + ": " + err.Error())
		}
		o := tool.Done().Fact("stopped", "yes").Fact("engine", a.Name()).Fact("model", model)
		if already {
			o.Note("already stopped: " + model + " was not loaded")
		}
		return o
	}
	// the box first, before anything is started (rule 8)
	o := tool.Done()
	measured := tool.Done()
	load1, free, _ := w.boxFacts(measured)
	answering := 0
	for _, x := range w.adapters {
		base := ""
		if x == a {
			base = c.Str("base")
		}
		if code, err := x.Health(ctx, w.client(x, base, 2*time.Second)); err == nil && code/100 == 2 {
			answering++
		}
	}
	maxLoad, _ := parseLoad(c.Str("max-load")) // ignored: the verb's Check refused a bad value before it ran
	minFree, _ := parseSize(c.Str("min-free")) // ignored: the verb's Check refused a bad value before it ran
	var past []string
	if maxLoad != nil && load1 > *maxLoad {
		past = append(past, fmt.Sprintf("load1 is %.2f and --max-load asks at most %s", load1, c.Str("max-load")))
	}
	if minFree != nil && free < *minFree {
		past = append(past, fmt.Sprintf("mem_free is %d bytes and --min-free asks at least %s", free, c.Str("min-free")))
	}
	if len(past) > 0 {
		f := tool.Fail(past...)
		f.Facts = measured.Facts
		f.Remedy = "nova-local status --list"
		return f
	}
	if answering == 0 {
		f := tool.Fail("no engine answers; start one, such as the ollama daemon")
		f.Remedy = "ollama serve"
		return f
	}
	if want := c.Str("expect-digest"); want != "" {
		ms, err := a.Models(ctx, cl)
		if err != nil {
			return tool.Fail("the engine's models could not be read: " + err.Error())
		}
		for _, m := range ms {
			if m.Ref == model && m.Digest != want {
				f := tool.Fail(fmt.Sprintf("%s has digest %s and --expect-digest asks %s", model, orNone(m.Digest), want))
				f.Remedy = "nova-local status --engine " + a.Name() + " --list"
				return f
			}
		}
	}
	dir, shared, why := w.store(ctx, a, cl, model)
	if c.Bool("require-shared-store") && shared == "no" {
		f := tool.Fail(fmt.Sprintf("the store %s is not shared (%s); --require-shared-store asks for the AI root's shared/models", dir, why))
		f.Remedy = "mkdir -p " + SharedRoot(AIRoot(w.getenv, w.home)) + "/" + a.Name() + " and point the engine's store at it (docs/SPEC-LOCAL.md, rule 15)"
		return f
	}
	s, err := a.Serve(ctx, cl, ServeRequest{Model: model, NumCtx: c.Int("num-ctx"), Seed: c.Str("seed"), KeepAlive: c.Str("keep-alive"), DryRun: c.DryRun()}, w.now)
	var r *Refusal
	if errors.As(err, &r) {
		f := tool.Fail(r.Why)
		f.Remedy = r.Remedy
		return f
	}
	if err != nil {
		return tool.Fail("the engine did not serve " + model + ": " + err.Error())
	}
	o.Fact("engine", a.Name()).Fact("model", model).Fact("serve_as", s.ServeAs).Fact("digest", orNone(s.Digest)).
		Fact("num_ctx", c.Int("num-ctx")).Fact("keep_alive", c.Str("keep-alive")).Fact("temperature", orUnset(s.Temperature)).
		Fact("seed", orUnset(s.Seed)).Fact("created", yes(s.Created)).Fact("load", s.Load.String())
	o.Facts = append(o.Facts, measured.Facts...)
	o.Fact("engines", answering).Fact("store", dir).Fact("shared", shared)
	if why != "" {
		o.Note("shared=" + shared + ": " + why)
	}
	return o
}

// worker writes the description nova-swarm reads (rules 9, 11, 14).
func (w world) worker(c *tool.Call) *tool.Out {
	a := w.adapter(c.Str("engine"))
	cl := w.client(a, c.Str("base"), 0)
	d := Description{Name: c.Str("name"), Provider: a.Provider(), Model: c.Str("model"), BaseURL: cl.Base,
		EnvVar: c.Str("env-var"), KeyFile: c.Str("key-file"), Usage: c.Str("usage"), Harness: c.Str("harness"),
		HarnessArgs: SplitArgs(c.Str("harness-args")), WorkerDir: c.Str("worker-dir"), Deadline: c.Str("deadline"), Board: c.Str("board")}
	if ps := d.Problems(w.stat); len(ps) > 0 {
		return tool.Refuse(ps...)
	}
	ms, err := a.Models(context.Background(), cl)
	if err != nil {
		f := tool.Fail("the engine's models could not be read: " + err.Error())
		f.Remedy = "nova-local status --engine " + a.Name()
		return f
	}
	if !slices.ContainsFunc(ms, func(m Model) bool { return m.Ref == d.Model || m.Ref == d.Model+":latest" }) {
		f := tool.Fail(fmt.Sprintf("the engine %s does not serve %s; a description for a tag nobody served launches a harness at nothing", a.Name(), d.Model))
		f.Remedy = "nova-local serve --engine " + a.Name() + " --model <parent> --num-ctx <n>"
		return f
	}
	if !c.DryRun() {
		if err := d.Write(c.Str("out")); err != nil {
			return tool.Fail("the description could not be written: " + err.Error())
		}
	}
	return tool.Done().Fact("engine", a.Name()).Fact("model", d.Model).Fact("out", c.Str("out")).Fact("workers", 1).
		Fact("provider", d.Provider).Fact("harness", d.Harness).Fact("deadline", d.Deadline).Fact("base", d.BaseURL).
		Note("run it one worker at a time (--workers 1): an engine is a queue, and two workers interleave it")
}

// parseLoad is --max-load: nil when not given.
func parseLoad(s string) (*float64, error) {
	if s == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return nil, fmt.Errorf("--max-load wants a load average, a number such as 8.0 (got %q)", s)
	}
	return &f, nil
}

// parseSize is --min-free in bytes, a number with an optional K, M, G or T (powers of 1024): nil when not given.
func parseSize(s string) (*uint64, error) {
	if s == "" {
		return nil, nil
	}
	mult, num := uint64(1), strings.ToUpper(s)
	if i := strings.IndexAny(num, "KMGT"); i == len(num)-1 && i > 0 {
		mult = 1 << (10 * (strings.IndexByte("KMGT", num[i]) + 1))
		num = num[:i]
	}
	n, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("--min-free wants a size such as 16G or 512M (got %q)", s)
	}
	n *= mult
	return &n, nil
}
