# Native OpenCode adapter — source findings and launch proposal

Status: discussion draft supporting [execution binding](SWARM-EXECUTION-BINDING.md).
The useful target remains per-job prompt, tool and model selection for Go and
Zen, with shared accounting. This document does not certify a runnable adapter,
activate profile fields or replace that target with text-only workers.
[Issue 296](https://github.com/mas-bandwidth/nova-tools/issues/296) owns the
bounded native configuration/route compatibility work.

## Pinned source and compatibility decision

The inspected native source is OpenCode v1.18.29, commit
`16747470f976aca3d362ad730bcd3fe82ecc2c9a`. Source links below refer to that exact
revision. A version string alone is not executable compatibility evidence; the
protected artifact binds the actual harness bytes. Runtime libraries remain
the stated trusted platform base; additional dependencies need an explicit
pinned compatibility/package policy or refusal, not an implied artifact hash.

The [config loader](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/config/config.ts#L328)
merges global, custom, project, plugin, account/org and managed configuration.
An inline config does not remove unrelated inherited members. Discovery also
schedules dependency installation at lines 448–470.
[Disabling project config](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/config/paths.ts#L23)
leaves global and home discovery. The proposed private HOME and frozen PATH
therefore do not, by themselves, prove frozen effective inputs.

Likewise, [disabling model fetch](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/core/src/models-dev.ts#L175)
does not reject existing mutable catalog metadata or compiled fallback, and
[provider loading](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/provider/provider.ts#L1477)
merges model/SDK configuration with that catalog. Runtime SDK installation is
another input path. None of those are frozen merely by `allowed_models`.

Consequently this source read does **not** admit stock v1.18.29 as
`opencode-native/1`. Before implementation is enabled, issue 296 must establish
a supported isolated entry point or a reviewed, narrowly scoped native-harness
change, with a compatibility test against the actual build. Do not suppress
managed/admin policy through test-only overrides; unsupported combinations
refuse. No new agent framework or silent weakening of the frozen-input contract
is proposed.

## Prompt and argv boundary

For a compatible native implementation, construct these separate argv entries:

```text
<protected harness> run --model <native-provider-id>/<native-model-id> --format json
```

The adapter admits exactly the corresponding template array
`["run","--model","{model}","--format","json"]`; `{model}` expands to the
single resolved native provider/model argument. Provider ID must be nonempty
and contain no slash; model ID must be nonempty and may contain slashes. Unknown
templates refuse. This proposes an adapter-specific expansion rule: it must
**not** invoke the legacy `harnessArgs` helper, which appends a prompt filename
when `{prompt}` is absent. Legacy file-consuming harness behavior is unchanged.

Feed the exact verified `PROMPT.md` UTF-8 bytes to a non-TTY stdin pipe, then
close it. Do not place prompt text or its filename in argv. Reject invalid UTF-8
and whitespace-only prompts during admission; preserve every admitted byte,
including newlines. Feed/drain operations remain under the attempt deadline,
cancellation and existing byte limits; a failed or partial feed is an attempt
failure, not a successful execution receipt. Use the verified job directory as
the working directory. Do not pass attach, continuation, session, fork, command,
agent, file, auto-approval or permission-bypass arguments.

This follows [`run` input handling](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/cli/cmd/run.ts#L31)
and its non-TTY stdin read at lines 416–422. A positional filename is literal
message text in that CLI; `--file` attaches content rather than supplying this
prompt protocol. This is a proposed native launcher change, not a claim that
the existing nova-swarm supervisor already supplies stdin.

## Route and secret boundary

Keep Go and Zen as distinct resolved routes. Each supported adapter revision
needs a reviewed route table binding native provider ID, model ID, endpoint,
SDK transport and complete required capability metadata. Names in a profile are
not proof of a provider's transport; a missing mapping refuses. The proposed
[native model projection](SWARM-NATIVE-MODEL.md) supplies SDK, effective API ID,
capability and option fields per selected model, with explicit number encoding.
Its parent-table additions and companion vectors remain proposals. Incorporate
the strict reader and full parent catalog/body/launch fixtures together before
enabling admission; do not create a second unhashed route owner in a callback.

The [SDK routing contract](SWARM-NATIVE-MODEL.md#sdk-base-urls-and-terminal-request-paths)
uses the SDK base URL in `resolved.endpoint`, not the full documentation
endpoint. Its pinned fake-fetch witnesses catch duplicated terminal paths;
they do not clear native configuration isolation.

For a provider established by that reviewed route table, native configuration
accepts `env:["<selected-variable-name>"]`, and
[provider initialization](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/provider/provider.ts#L1578)
can consume the gated value from that one name. This is not evidence of a
built-in Go/Zen mapping; an unreviewed route still refuses. Only the name belongs in the
generated configuration. Do not insert a secret value or use `{env:...}` in
the config: [substitution](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/config/variable.ts#L33)
occurs on raw text before JSON parsing. Other auth/account providers must not
override this binding. This mechanism still requires the isolation gate above.

## Tool selection, permission and sandbox are distinct

`prompt.tools` needs an adapter-owned table mapping each supported capability
to actual model-visible native registry names and permission requests/patterns.
Do not use the native permission schema as a tool inventory. For example,
[config lowering](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/config/config.ts#L567)
maps `write/edit/patch` to permission `edit`, whereas
[visibility filtering](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/permission/index.ts#L204)
maps `write/edit/apply_patch`. Moreover, the normal
[session tool assembly](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/session/tools.ts#L81)
exposes the registry's built-ins before per-call permission checks; that
visibility helper is used for a separate MCP code-mode catalog. A permission
deny alone does not remove an ordinary built-in definition from the model.

Propose this closed capability table for the first native adapter revision.
The symbols belong to this adapter, not a universal vocabulary imposed on other
harnesses:

| Profile capability | Native registry IDs | Permission request key |
|---|---|---|
| `read` | `read` | `read` |
| `glob` | `glob` | `glob` |
| `grep` | `grep` | `grep` |
| `modify` | `edit,write` or `apply_patch`, selected below | `edit` |
| `shell` | `bash` | `bash` |

The pinned [registry predicate](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/tool/registry.ts#L291)
selects `apply_patch` when the **effective API model ID** contains `gpt-` and
contains neither `oss` nor `gpt-4`; otherwise it selects `edit,write`. This is a
case-sensitive source predicate, not an inferred model-family classification.
Freeze that API ID in the reviewed route projection and reject a mismatch.
The request/display model name alone cannot determine the group.

Admission requires distinct known capability symbols already in ascending raw
UTF-8 order. It rejects an unsorted list, preserving the parent rule that admitted
arrays retain their order; it does not silently sort a retained array.
Raw aliases (`bash`, `write`, `patch`, `apply_patch`)
are not additional accepted symbols. Registry filtering must expose exactly the
union selected by this table and must also reject execution of an unselected
name, including model-emitted names that were never advertised. Permission
requests remain an independent check. Out-of-instance paths can additionally
request `external_directory`; its reviewed pattern policy must correspond to the
admitted roots and must never widen the OS sandbox. Registry/permission mapping
tests must exercise those secondary requests, not just the primary column.

Delegation, skills, LSP, web/MCP and plugin tools require their own reviewed
mapping before a later adapter revision enables them. This table is a useful
first native slice, not completion of every profile capability across every
harness. A supported isolated registry filter remains implementation work in
issue 296, and the parent schema/fixtures must adopt these symbols explicitly.

A wildcard deny plus selected allows is only a secondary permission layer.
Merged inherited exact rules or plugin contributions may survive; final policy
and registry enforcement require the isolation contract. Noninteractive `run`
also denies question and plan-mode transitions. Shell permission is not an OS
filesystem or network boundary: the sandbox remains independently authoritative,
and no egress restriction is implied unless actually configured and enforced.
The table must cover useful worker capabilities before the adapter is called
complete; an empty-only tools implementation does not satisfy that requirement.

## Remaining bounded implementation evidence

1. A fake native process captures exact argv, cwd and stdin (including Unicode,
   quotes, newlines and filename-like text), fails on partial input and cannot
   receive a continuation or automatic-approval flag.
2. Real registry/session fixtures prove selected tools are available, unselected
   tools cannot execute, and actual permission requests match the reviewed table.
   Cover edit/write/apply-patch selection and an unselected plugin tool.
3. Native compatibility fixtures observe config/package/catalog reads and side
   effects under changed ambient inputs, proving fixed admitted behavior or
   refusal before provider use. Managed-policy conflicts refuse without bypass.
4. Distinct Go/Zen synthetic routes bind all transport metadata and one secret
   name; secret values never enter retained config, argv, hashes or logs. Unknown
   metadata, revision or SDK refuses. Retry preserves the prior protected bytes.
5. The adapter-owned event/usage reader follows the realized attempt data home
   and records model/token coverage, unknown paid cost, and local zero API cost
   according to the shared accounting contract. Stock `run --format json` exposes
   post-normalized events, so this route does not claim raw per-call usage
   coverage. A successful process exit alone proves neither accepted work nor
   correct accounting.

The exact generated config bytes, derived config-path environment additions and
static variable whitelist remain implementation-contract gates. Do not add
unhashed ambient variables to the existing realization vectors to make a native
binary start. These changes must be explicit in the parent tables, realization
preimage and independently recomputed fixtures. After review and implementation,
measure equal-quality operational workload tokens before/after adoption;
implementation spend and smaller serialized output are not the savings result.
