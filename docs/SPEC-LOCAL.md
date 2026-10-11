# nova-local — specification

**What this tool is for, in one sentence (the owner, 2026-09-12):** *"somebody should
be able to grab nova-local and run local models."*

Everything below serves that sentence or is cut. The owner cut it three more times the
same night — *"I want to not overengineer this. I think it should just run the
model."*, *"no deps."*, *"if we want to eval, it is another tool"* — and it is three
verbs. It came back for 1.2.0 (the owner, 2026-10-04: *"I think nova-local should come
back"*), with the fleet's side added the same morning ("Fleet", below).

`nova-local` is one binary. It does not run inference, fetch weights or judge models.
It makes a local engine usable: **what is here** (`status`), **one model served at a
context you chose** (`serve`), **a description the thing that will call it accepts**
(`worker`). `status` is a report and exits 0 whenever it ran, except when no engine
answers at all; `serve` and `worker` are walls.

This spec is normative; if the code and this document disagree, one of them has a bug
and the tests decide which. It is built on `pkg/tool` (docs/STANDARD.md): the
banner, the help, `--json`, the refusal line and the exit table are the skeleton's, and
are not restated. The command is `cmd/nova-local`, and the engines (`engine.go`,
`ollama.go`), the box (`box*.go`), the shared store (`store.go`), the serving-host rule
(`host.go`) and the worker description (`description.go`) are files of that one package.

## DATA

| | |
|---|---|
| language | Go — `cmd/nova-local`, the standard library and this module's own packages only |
| kind | engine runner. Not an inference client, not a fetcher, not a judge, not an outbound actor |
| engines | **ollama** (many models, one daemon), built. **ds4** (one resident model per process), `llama.cpp`'s server and `mlx-lm`: named, not built |
| pin | **loopback or the tailnet.** `--base` defaults to the adapter's constant; any other host is exit 2 |
| store | **the shared directory of the AI root**: `<ai-root>/shared/models/<engine>`, never one account's |
| secrets | **none.** Opens no key file, exports no key, prints no credential. It `stat`s the key file a description names |
| exit | `0` did it · `1` it ran and said **NO** · `2` could not run |
| verbs | `status` · `serve` (with `--stop`) · `worker` — three, no others |

## The rules, numbered

Every rule is normative and has a test (**Tests**, below).

1. **No dependencies** (the owner: *"no deps"*). One Go binary whose imports are the
   standard library and this module's own packages, and no shelling out to learn what
   the standard library cannot read: the box's memory and load are `sysctl` on darwin
   and `/proc` on linux, read in process. What the stranger installs (ollama, the
   harness) is not this binary's dependency.

2. **An engine is an adapter, and adding one changes nothing else.** An adapter
   (`Adapter`, `cmd/nova-local/engine.go`) declares its name, its default base URL, its health check,
   how it lists models, how it serves one at a context, how it stops one, where its
   weights live and the provider id a harness names it by. Adding one is one file and
   one entry in `Adapters`, and no change to a verb.

3. **Loopback or the tailnet, and the port is the operator's.** A `--base` whose host is
   not loopback and not an address in the tailnet's range (100.64.0.0/10) is exit 2
   naming the host; a name is looked up and **every** address it resolves to must be one
   of those, so a name cannot point a local tier at a remote endpoint. The default is
   printed on every `ENGINE` line.

4. **A model is named by `(engine, reference, digest)`, and the digest is printed, never
   ruled on.** `--expect-digest` is optional and, differing, is exit 1 naming both and
   serving nothing; an engine reporting none prints `digest=none`. No lockfile, no
   quarantine, no trust state: those belong with an evaluation tool.

5. **`--num-ctx` has no default.** ollama's own is small and silently truncates a long
   prompt, so a long prompt comes back as a confident answer about a truncated input.
   `serve` without it is exit 2 and `refusing to guess`; `status` prints as `num_ctx=`
   only the `context_length` a loaded model reports.

6. **`serve` makes the served thing and prints the name the harness must use.** ollama
   keeps per-model options only in a model file and a `/v1` caller sends no `num_ctx`,
   so `serve` **creates a derived tag** `<name>-<ctx>k` with `num_ctx`, `temperature 0`
   and the given `seed` baked in, and prints `serve_as=`. `<name>` is the reference with
   its `:<tag>` and any registry prefix stripped (`gemma4:12b` → `gemma4-32k`), so a
   shared name derives one tag and a collision is caught by the parent; `<ctx>k` is
   `--num-ctx / 1024`, and a remainder is exit 2 naming the two nearest multiples. An
   existing tag is compared on exactly four things — parent, `num_ctx`, `temperature`,
   `seed` — used as it stands when all four match (whatever else the engine reports, such
   as an inherited `top_k`), and any one different is exit 1 naming both values with
   `ollama rm <tag>`.

7. **`keep_alive` is on, and load time is measured here, once, by the thing that caused
   it.** `serve` sends one warm-up with `keep_alive` (default `30m`), timed on its own
   clock: that is `load=`. Nothing else reports a duration.

8. **READY is a measurement, printed — and a refusal only against a value the operator
   gave.** `serve` reads the one-minute load average, free memory and the engines
   answering **before** starting anything and prints them on its line. It has no
   threshold of its own; `--max-load` and `--min-free` are optional and, passed, are
   exit 1 naming measured and asked.

9. **One local worker at a time.** An engine is a queue: two workers interleave it.
   `worker` prints `workers=1` and says so in a `NOTE`. In a fleet the machine's lanes
   carry it (below).

10. **The memory is read at the moment of the call, and printed — not judged.** `status`
    prints each model's weights and, on its first line, `mem_used`, `mem_free`,
    `mem_total` and the live `wired_cap` (darwin's GPU wired limit, `unset` when none).
    No verdict, never cached.

11. **Triage and report; never decide-and-act.** What a local model returns is untrusted
    data. No verb merges, sends, deletes or rules on safety, and this tool writes exactly
    one file: `worker`'s `--out`. The prompt conditions are `nova-swarm`'s.

12. **Every model has a caller or it leaves.** Every `MODEL` line carries `weights=` and
    `loaded=`, and `serve --stop` is the act: one request with `keep_alive 0`; the tag
    stays on disk; a model not loaded is `already stopped` at exit 0.

13. **Every listing is bounded.** Counts by default, a list behind `--list`, capped by
    `--max` (default 20, `0` for all) with one `MORE` line. With nothing answering,
    `status` is one line at exit 1 naming the command that starts an engine.

14. **Content on stdin, never in argv; no key, ever.** No prompt or model output reaches
    a command line, a log or a printed line. This binary has no shell and **never opens
    the key file** a description names: it `stat`s it and refuses a missing or empty one
    with the command that writes it.

15. **The weights are one store per machine, under the AI root's shared directory, and
    this tool reads it, reports it and refuses a store outside it when asked.** The AI
    root is `$NOVA_AI_ROOT`, else `ai` under the caller's home; the shared store is
    `<ai-root>/shared/models`, one subdirectory per engine, never under one account's
    working directory (the owner, 2026-10-04: models live under the shared directory of
    the AI root, never per account). `nova-local` installs nothing and serves from
    whatever store the engine reports: for ollama, the parent of the `blobs` directory
    its model file's `FROM` line names, **resolved through symlinks** (a component that
    does not exist ends resolution, and the rest is appended as spelled). Every `ENGINE`
    line and the serving `SERVE OK` line carry `store=` and `shared=yes|no|unknown`:
    `yes` exactly when the resolved store is under the resolved shared store, matched a
    whole component at a time (`models-scratch` beside `models` is not under it); `no`
    with the reason; `unknown` when the store or the AI root cannot be read, which is no
    verdict. With `--require-shared-store`, `serve` is exit 1 on `shared=no` and never on
    `unknown`; `status` never refuses. Pointing the engine at the shared store
    (`OLLAMA_MODELS`, the store's owner and mode, the one-time move of the weights) is
    the box's side, done by its operator, not by this tool.

## The engines

| | **ollama** |
|---|---|
| what it is | a daemon serving many installed models |
| default base | `http://127.0.0.1:11434/v1` |
| health | `GET /api/tags` on the daemon's root |
| a model is | a tag, `<name>:<tag>` |
| arrives by | the operator's own `ollama pull <tag>`: **this tool fetches nothing** |
| digest | the manifest digest from `/api/tags` |
| `serve` | read the tag back from `/api/tags` and `/api/show`; create `<name>-<ctx>k` with `/api/create`; one warm-up `/api/generate` with `keep_alive` |
| `serve --stop` | one `/api/generate` with `keep_alive: 0`; the derived tag stays on disk |
| harness id | provider `ollama`, model the derived tag; OpenCode's `--model` is `<provider>/<model>`, so the harness argv carries `ollama/{model}` |

ds4 (one resident model per process, a GGUF on disk, no digest) is named and not built:
its harness id, its load-progress endpoint and its shutdown path are unknowns read off
its own source before its adapter is written.

## The verbs

```
nova-local status [--engine <name>] [--base <url>] [--list] [--max <n>] [--timeout <d>]

nova-local serve  --engine <name> --model <ref> --num-ctx <n> [--base <url>]
                  [--keep-alive <d>] [--seed <n>] [--expect-digest <sha256:...>]
                  [--max-load <f>] [--min-free <size>] [--require-shared-store] [--dry-run]
nova-local serve  --stop --engine <name> --model <tag> [--base <url>]

nova-local worker --engine <name> --model <tag> --out <file> --name <text>
                  --harness <cmd> --harness-args <a,b,{model},...> --worker-dir <abs dir>
                  --key-file <file> --env-var <NAME> --usage <opencode|none>
                  --deadline <duration> [--base <url>] [--board <owner/repo#n>] [--dry-run]
```

**No guessed anything.** No default engine, context, model or output path, and on
`worker` no default for any field the description carries; a missing one is exit 2 and
`refusing to guess`. The only defaults are `--base` (the adapter's constant),
`--timeout` (2 s), `--max` (20) and `--keep-alive` (`30m`), each printed on the line that
used it. `--harness-args` is split on `,` with no escape.

### `status`

Reads each engine's health inside `--timeout`, its models (`/api/tags`, `/api/ps`), the
store (`/api/show` of the first model) and the box. Writes nothing.

```
STATUS OK engines=<n> answering=<n> loaded=<n> models=<n> mem_used=<n> mem_free=<n> mem_total=<n> wired_cap=<n|unset> load1=<f>
STATUS ENGINE name=<e> state=<up|down|timeout> [status=<code>] base=<url> [loaded=<n> advertised=<n>] [num_ctx=<n>] [store=<dir|unknown> shared=<yes|no|unknown>][: <why>]
STATUS MODEL engine=<e> model=<ref> digest=<sha256:...|none> weights=<n> loaded=<yes|no> [num_ctx=<n>]
STATUS MORE kind=model shown=<n> total=<n> ...
STATUS FAILED: no engine answers (<n> asked); start one, such as the ollama daemon; run: ollama serve
```

`state=up` is a 2xx on the health path inside `--timeout` and nothing weaker; a slow
engine is `state=timeout` at exit 0.

### `serve` (with `--stop`)

Reads the box (rule 8) **before** starting anything, then the parent's tags, the derived
tag's four compared parameters, and the store; its own clock across the warm-up.

```
SERVE OK engine=<e> model=<ref> serve_as=<tag> digest=<...> num_ctx=<n> keep_alive=<d> temperature=<0|unset> seed=<n|unset> created=<yes|no> load=<t> mem_used=<n> mem_free=<n> mem_total=<n> wired_cap=<...> load1=<f> engines=<n> store=<dir|unknown> shared=<yes|no|unknown>
SERVE OK stopped=yes engine=<e> model=<tag>
SERVE FAILED: <reason>; run: <command>
```

Refuses at exit 1, each with its remedy: a parent the engine does not have (`ollama pull
<ref>`); an `--expect-digest` that differs, naming both (`nova-local status --list`); an
existing derived tag differing in parent, `num_ctx`, `temperature` or `seed`, naming
both values (`ollama rm <tag>`); a load or free memory past a `--max-load` or
`--min-free` the caller gave, naming measured and asked; a store outside the shared
store, only under `--require-shared-store`. A missing `--num-ctx`, one with a remainder,
and a `--base` outside rule 3 are exit 2. `--dry-run` reads all of it and creates and
loads nothing.

### `worker`

Reads the served tag from the engine, the adapter's provider id and base URL, and its
flags; `stat`s the key file and the worker directory; writes exactly one file.

```
WORKER OK engine=<e> model=<tag> out=<path> workers=1 provider=<p> harness=<cmd> deadline=<d> base=<url>
```

The description is `nova-swarm`'s worker schema (`pkg/swarm.Worker`, decoded with
unknown fields refused): `name`, `provider`, `model`, `base_url`, `env_var`,
`key_file`, `usage`, `harness`, `harness_args`, `worker_dir`, `deadline`, and `board`
when given. It carries no `temperature`, `seed` or `num_ctx` — `serve` baked them into
the tag — and no conditions. Refuses at exit 2, every problem in one run: harness
arguments without `{model}`; a `--worker-dir` relative or absent; a `--key-file` missing
or empty, with `printf 'local\n' > <path> && chmod 600 <path>`; a `--usage` other than
`opencode` or `none`; a `--deadline` that is no Go duration. One refusal is exit 1,
because the caller can retry it: a `--model` the engine does not serve, remedy
`nova-local serve`. `--dry-run` does all of it and writes nothing.

## Fleet

The owner's design, 2026-10-04: a local model is a provider like any other, and the
sprint deals to it as it deals to any route. Whether a lane's calls go to a local model or
to a remote one is route configuration, never a second count on the machine: a machine's
lanes are its `width`, and a route says what serves them.

**Status: designed, not built.** `cmd/nova-local` builds the store and the endpoint's
serving-host rule (rules 3 and 15). The route, the concurrency, the deal and the launch
below need nova-config's route fields `endpoint` and `concurrency`, the sprint's cap and
`nova-swarm native --local-base`, none of which exists yet; until they do, `serve` prints
no `nova-config route add` line and has no `--concurrency`.

- **The store.** Every machine keeps its weights under the shared directory of its AI
  root, `<ai-root>/shared/models/<engine>` (rule 15), never per account.
- **The endpoint.** An engine serves a model at an endpoint: a host, a port, the model's
  served tag, and the most requests it takes at once. Over the tailnet its ollama listens
  on its tailnet address, and every caller reaches it there or on loopback (rule 3). The
  endpoint of a model served on host `h` is `http://h:11434/v1`.
- **The route.** A route of `--provider local` names its endpoint and a concurrency
  (nova-config's route fields `endpoint` and `concurrency`; only a local route names an
  endpoint, and a local route wants a concurrency above 0), `--model` the served tag. Its
  prices are 0 (`--price_input 0 --price_output 0`): its cost is $0 and its tokens are
  recorded as any route's. `serve --base http://<host>:11434/v1 --concurrency <n>` prints
  the row's `nova-config route add` line; its name is `local-<model>-<host>`.
- **The concurrency.** The cap on how many lanes may use a route at once is the
  endpoint's, so it is the route's: nova-config's route field `concurrency` (migration
  0028), the cards using the route at once; 0, the default, is no cap, and any route may
  carry one (a metered provider's limit is the same field). A host running N instances of
  a model is N routes of the instances' concurrency each, or one route of concurrency N;
  both are only configuration. A machine row holds no second count: `width` is its lanes,
  and `--width 0` is a machine that runs no member, whatever its endpoints serve. A change
  reaches the deal at the next `nova-config apply` of the routes.
- **The deal** (`internal/sprint/route.go`, the cap). A local route is in its tier's
  route set and is drawn like any route. A work card ready or working, or a read card
  asked or reading, whose `route` names a capped route holds one of its slots; a draw
  skips a route whose slots are all taken, as it skips a resting route, and the card
  waits for a slot with no judgment, since the route serves the tier. The draw writes the
  route's endpoint on the work card (`serve`), the packet hands it to the member, and
  `nova-sprint routes` shows `endpoint=<url> concurrency=<busy>/<cap>`.
- **The launch.** The member passes `--local-base <endpoint>` to
  `nova-swarm native` for a card whose packet names an endpoint; native declares the
  provider `local` at that endpoint in the job's harness config (OpenCode's
  OpenAI-compatible provider, no key), the route's model under it, and stands its
  read-deadline proxy in front of it; on loopback the wall opens that one port.

## Deliberately not in this tool

- **`pull`** — the engine's own tool and the operator's own download.
- **`trust`, `untrust`, a lockfile, `fit`** — policy and verdicts, cut.
- **`eval` and `compare`** — the owner: *"if we want to eval, it is another tool."*
- **inference** — no `ask`, no `read`; `nova-swarm` runs the workers.
- **discovery and ranking** — a model name found on a page or in a model's own output is
  data, never a serve target.
- **the box's side** — the daemon, its listening address, the store's owner and the
  one-time move. A tool writing under `/Library` would be an outbound actor, and this one
  writes exactly one file.

## Tests

Each runs against a fake engine through the injected transport (`cmd/nova-local`,
`fake_test.go`): no socket, no network, no real time; the box is faked through
`Box`.

| rule | test |
|---|---|
| 1 | `go list -deps ./cmd/nova-local` names no package outside the standard library and this module (checked at the build; no in-process test) |
| all | `TestTheThreeVerbsOfTheSpec`: each verb against the fake engine, and the `--dry-run` of serve and worker creating, loading and writing nothing |
| 2 | `TestOnlyThreeVerbs` (the tool's shape); the adapter set is `Adapters` |
| 3 | `TestBaseIsLoopbackOrTheTailnet`, `TestTheServingHostRule` |
| 4 | `TestServeRefusesOnlyPastTheCallersValues`, `TestStatusPrintsTheEngineAndTheBox` |
| 5 | `TestServeRefusesToGuessTheContext`, `TestDerivedTag` |
| 6 | `TestServeMakesTheDerivedTagOnce` |
| 7, 12 | `TestServeTimesOneWarmUpAndStops` |
| 8 | `TestServeRefusesOnlyPastTheCallersValues` |
| 9, 11, 14 | `TestWorkerWritesADescriptionNovaSwarmAccepts` (decoded by `swarm.LoadWorker` with zero problems; one file written; the key file at mode 0000) |
| 10 | `TestStatusReadsTheMemoryEachCall` |
| 13 | `TestStatusListingIsBounded`, `TestStatusWithNothingAnsweringIsOneRemedyLine` |
| 14 | `TestWorkerRefusesEveryProblemAtOnce` |
| 15 | `TestTheSharedStore`, `TestSharedAndResolve` |
| Fleet | `TestALocalRouteNamesItsEndpointAndConcurrency` (config), `TestALocalRouteTakesItsConcurrencyOfCardsAndRecordsItsUsageAtNoCost` and `TestARouteAtItsConcurrencyDrawsTheNextRoute` (the sprint's twin), `TestRoutesAndWhereShowALocalRoute`, `TestAMemberLaunchesALocalRoutesCardAtItsEndpoint`, `TestNativeTakesALocalBaseOnlyWithALocalModel`, `TestAJobOnALocalRouteDeclaresTheProviderAtItsEndpoint`, `TestDeclareLocalProviderPointsTheHarnessAtTheEndpoint`, `TestServeOverTheTailnetNamesTheFleetRoute`: owed with the Fleet's build |

The first run (`docs/TESTS.md`) is executed by `TestTESTSFirstRunIsWhatTheToolPrints`,
and the banner's examples by `TestUsageBannerExamplesRun`. The functional tier's
`TestARealTinyModelIsServedFromTheSharedStore` serves the smallest model a real ollama on
loopback advertises from the shared store, and stops it; it runs only where such a model
is present, and is skipped elsewhere.
