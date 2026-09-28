# SDK route probe

This fixture checks request-path construction by the exact public packages
`@ai-sdk/openai-compatible@2.0.41` and `@ai-sdk/openai@3.0.84`. It is evidence
for a proposed native-route contract, not OpenCode/native-adapter implementation
or production admission validation.

Install dependencies with an isolated, credential-free npm configuration. The
package lock is required; do not substitute a later SDK release.

From this directory, with Node.js 18+ and npm on PATH:

```sh
probe_work=$(mktemp -d)
cp package.json package-lock.json probe.mjs stream-probe.mjs "$probe_work/"
printf 'registry=https://registry.npmjs.org/\n' > "$probe_work/npmrc"
printf '' > "$probe_work/global-npmrc"
(cd "$probe_work" && env NPM_CONFIG_USERCONFIG="$probe_work/npmrc" \
  NPM_CONFIG_GLOBALCONFIG="$probe_work/global-npmrc" \
  NPM_CONFIG_CACHE="$probe_work/cache" \
  npm ci --ignore-scripts --no-audit --no-fund --registry=https://registry.npmjs.org/ \
  && node probe.mjs && node stream-probe.mjs)
```

Installation downloads public dependencies; the probe uses intercepted requests.
Run outside a shell with injected npm authentication settings. No dependency
installation is added to the normal per-change Go CI. The scratch directory
retains the dependency lock and run inputs for inspection.


`probe.mjs` replaces global `fetch` with a throwing guard, injects a recording
fake fetch into every SDK provider, and returns only synthetic JSON. It proves
that base URLs ending at `/v1` construct the documented chat/responses paths,
and shows the duplicated path when a terminal chat path is incorrectly supplied
as a base URL. It also records a fixture `x-opencode-session` and fixture
User-Agent per call. No request can use global fetch; the script does not call a
provider, load OpenCode, use a worker, or use credentials. `fixture-only-key` is
a literal test value and not a credential.

`probe.mjs` covers direct nonstreaming `doGenerate` construction.
`stream-probe.mjs` supplies synthetic SSE to actual `doStream` calls for both
chat routes and the Responses route. It checks declared function-tool request
schemas, split argument deltas, normalized tool-call identity/arguments,
inclusive input/output counts and their cache/reasoning subcounts, absent
chat usage, and malformed chat chunks. Positive streams must finish once with
`tool-calls` and no error events. These are SDK-level fixture results, not
proof that native OpenCode exposes, executes or authorizes those tools.

The inputs are deliberately synthetic and never enter operational token or
cost reports. They contain no provider money observation. This fixture does
not validate authentication, provider availability, retries, native catalog or
configuration isolation, arbitrary transport fragmentation, interrupted streams,
all malformed inputs, missing Responses usage or full native-adapter admission.
