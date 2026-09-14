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
cp package.json package-lock.json probe.mjs "$probe_work/"
printf 'registry=https://registry.npmjs.org/\n' > "$probe_work/npmrc"
printf '' > "$probe_work/global-npmrc"
(cd "$probe_work" && env NPM_CONFIG_USERCONFIG="$probe_work/npmrc" \
  NPM_CONFIG_GLOBALCONFIG="$probe_work/global-npmrc" \
  NPM_CONFIG_CACHE="$probe_work/cache" \
  npm ci --ignore-scripts --no-audit --no-fund --registry=https://registry.npmjs.org/ \
  && node probe.mjs)
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

The probe covers direct nonstreaming `doGenerate` construction only. It does not
validate streaming/SSE parsing, authentication, provider availability, retries,
model catalog/config isolation, tool conversion, or native-adapter admission.
