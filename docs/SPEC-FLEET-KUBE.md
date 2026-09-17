# nova-fleet, declared by Terraform and run by Kubernetes (draft 1)

Glenn, 2026-09-17: *"If we want proper isolation, we should reuse docker, or kube or whatever is
already there. We should not re-do all that ourselves. Should we consider kube in future for
workers in fleet? Kube helps make more manual things into automated. Consider terraform as well.
Terraform is great. If something already exists and is suitable, we use that. We only invent when
it is radically new and will create for us a big advantage."*

This spec spends that ruling on the fleet. The fleet today is four Linux benches and one seat:
the **Studio** (macOS, the coordinator's seat, no pods), **hulk** and **vision** (64 cores, 98 GB
disks), **space** (16 cores, 440 GB), **mini** (4 cores). Each was provisioned by hand on
2026-09-16 — Go, SBCL, the harness, the sixteen nova bins, the runners, the secrets seats — and
asserted the next morning by `tools/bench-standard.sh`. Hygiene and git mirrors run on timers,
each bench keeps a fetch-only mirror and shared Go caches, cards live under
`working/tmp/<guid>`, and a bench's **capacity line** is
`min(cores*1.5 - load1, (free_gb-25)/2, memfree_gb/2)`. It runs the job system of
[SPEC-JOBS.md](SPEC-JOBS.md): a ready set, per-bench queues, work stealing, pull workers on
leases, affinity, lanes and batches. This draft does not move that system; it hands its *state*
to Terraform and its *execution* to Kubernetes, and keeps nova-sandbox for the wall the container
does not build. No code. If this document and the tools disagree, one of them has a bug.

## Part 1 — Terraform: the fleet as declared state

**One root module, one module per bench role.** `fleet/` holds `benches.auto.tfvars` naming each
bench, its role (`coordinator`, `heavy`, `medium`, `light`), its address and its `mac`, and a
`module "bench"` instantiated once per role: `modules/bench-linux` for hulk, vision, space and
mini, `modules/bench-studio` for the Studio. A role is a contract, not a copy: the heavy role's
slice of disk, runner count and kind labels differ from the light role's in the module's
variables, and every bench of a role is the same declaration with a different name.

**What the module declares.** The resources are ordinary Terraform, and nothing here is new:
OS packages; the per-line unix users and their homes; the runner services
(`nova-runner-<i>.service` with the `Environment=PATH`, `KillMode=control-group` and
`TimeoutStopSec=30s` stanzas the standard already names); the hygiene and mirror systemd timers;
the k3s install on each Linux bench (`curl -sfL https://get.k3s.io | sh`, pinned to the release
the fleet tests against) and the k3s agent join on the others; the shared-cache and mirror
volumes under `$HOME/nova-bench`; and the secrets **seat public keys** — public by definition, so
they may sit in state and in the module's inputs. On an already-provisioned host the *writes* run
once over the SSH connection a `null_resource` carries — but a `null_resource` is not the drift
detector. Its `triggers` hash the content the module *declares*, not the bytes on the host, so a
file edited by hand on the bench leaves `plan` empty until someone also changes the declaration.
**Every managed file is therefore paired with a data source that reads the host on every plan.**
`data "external" "file_state"` runs `ssh <bench> 'sha256sum <path>'` (or reads the file back over
ssh) and returns the observed hash; the resource that owns the file carries a `precondition` (or
a `check`) comparing that observed hash with the declared one, so the drift you did by hand is an
attribute that differs and the plan is not empty. The same shape covers facts no provider exposes
as a resource: `data "external" "bench_standard"` runs `tools/bench-standard.sh` on the host each
plan and parses its lines, and the precondition fails the plan when the script and the
declaration disagree. A `null_resource` may still carry the `remote-exec` that writes; it never
carries the *witness*.

**How `plan` shows drift.** For every object a provider can read back — the Kubernetes objects in
Part 2, the k3s release — `terraform plan` refreshes and prints the attribute that differs, and a
plan that is not empty before an apply *is* the drift report. For files and OS facts no provider
reads natively, the read-each-plan data source above is the witness: it reads the remote state on
every refresh, and the precondition on the owning resource fails the plan the moment the host and
the declaration disagree, a hand edit nobody declared included. The authoritative in-sandbox
witness on a live host stays `tools/bench-standard.sh`, folded by `nova-pulse fleet survey`; the
data source makes `plan` read the same script rather than a hash of what was once written. The
two are not allowed to disagree silently: a `DRIFT` line the standard prints while `plan` is clean
is itself a red test.

**A new bench is one apply.** Add one entry to `benches.auto.tfvars`, run `terraform apply`, and
that apply installs packages, creates users, writes and starts the runner units, arms the timers,
installs k3s, mounts the cache and mirror volumes, and authorizes the seat public key. The
onboarding prose loses its step list and keeps only the facts a person must supply (address,
`mac`, role, seat name).

**What `bench-standard.sh` stops doing.** It stops *provisioning* and stops being the only drift
detector. It keeps its job as the in-sandbox network probe and as the acceptance witness that
runs after an apply: it still counts listeners, checks go and sbcl, checks the bins, checks the
seat and refuses plaintext keys — but those checks now confirm what Terraform guaranteed rather
than repair it. `--apply` still kills stray listeners and nothing more.

**What must never be in state.** A secret value, in any form: the plaintext of a sealed yaml, an
API key, a token, the sops **age private key**, or a `kubectl` credential that can read one.
Terraform state is plaintext JSON in a backend, and `sensitive = true` redacts output, not state.
The store stays in git behind sops; the cluster gets ciphertext (Part 2); the seat's key file
never leaves the seat. A `terraform output` that is a secret is a defect, and so is a
`local_file` that writes one.

## Part 2 — Kubernetes on the Linux benches (k3s)

**One single-node k3s per bench, not one fleet cluster.** The capacity line and the warm cache
are properties of a *bench*, so the scheduler must never move a card across benches; moving work
is the queue layer's job (the mirror timer and the steal), where SPEC-JOBS already owns it. Each
Linux bench runs its own k3s and schedules only its own cards. The Studio is not a node.

**The worker shape: a Job per card.** The pool's unit of accounting is the card — one
`RESULT.md`, one `usage.tsv` row, one deadline, one clip — so one card gets one pod. The Job
gives the card its own cgroup, its own env, its own log stream, its own `activeDeadlineSeconds`,
and a failure whose effect is defined by the platform. It also makes the capacity line a
*scheduler* fact rather than a number a person counts. **The pull is not replaced.** A per-bench
puller (`nova-swarm pull --submit`, one replica) lists `queue/lanes/`, takes one card by
`rename(<name>.card, taken/<worker>-<name>.card)` — atomic within the directory, as SPEC-JOBS
rule 2 already says — and creates the Job for that card. The puller does not choose a worker or
an order; the take *is* the ownership, and the lanes are the order. Two pullers cannot take one
card because the rename decides.

**Resource requests and limits are the capacity line.** Each Job requests `cpu: 1`,
`memory: 2Gi`, `ephemeral-storage: 2Gi`, and sets `limits.memory: 2Gi`. The node's
kube-reserved/system-reserved reserves the fixed **25 GiB** floor, so
`(free_gb-25)/2` and `memfree_gb/2` are enforced by the scheduler's own admission of a 2 GiB
request against allocatable. The first term, `cores*1.5 - load1`, is not a Kubernetes primitive
and is **not reinvented**: the puller reads `load1` (`nova-wake probe --here`) and declines to
submit while `cores*1.5 - load1 <= 0`. Kubernetes enforces the two terms it can; the puller
enforces the one it cannot.

**One admission owner while both run.** In slice 2 a bench runs the native `nova-pulse launch`
and the puller at once, and the capacity line must be owned by exactly one of them or both admit
the same free slot. **The pull worker is the admission owner on a mixed bench.** It reads the
capacity line (`nova-wake probe --here` for `load1`, the scheduler's allocatable for the two
memory terms), decrements it for the Job it is about to submit, and writes the decremented value
to the bench's admission file under `taken/` before it creates the Job. The native launcher keeps
no copy of the line: it **consults that file** and admits a card only into what the puller has
not taken, so one owner runs both drivers without over-admitting. (The alternative is allowed and
simpler: **retire the native launcher on that bench first**, and the puller is the only admission
owner.) **And an admission that is retried is reconciled.** A puller that fails between writing
the decrement and creating the Job leaves a slot charged to nothing; the retry completes the
first admission rather than making a second, and a card **admitted twice is refused by its
`guid`** — the take already renames `<name>.card` to `taken/<worker>-<name>.card`, and the
admission file records that same `guid` against the slot, so a second take of a card already held
is refused instead of charged twice. A slot held with no Job is the state the reconciliation
names, and the red test below measures it.

**Node labels and affinity.** Each bench labels its node
`nova.mas-bandwidth.com/bench=<name>` and one or more
`nova.mas-bandwidth.com/kind=go|lisp|docs|schema-leg`. A Job carries the kind of its card and
selects it with `nodeSelector`, so a lisp card is only ever placed where SBCL is warm. Warm
caches are `preferredDuringSchedulingIgnoredDuringExecution` affinity to the repo/cache label,
and the `local` volume below pins placement hard when a card needs a specific checkout.

**PersistentVolumes per bench.** One `local` PersistentVolume for the fetch-only **mirror**
(`ReadOnlyMany`, `volumeBindingMode: WaitForFirstConsumer`) and one for the shared **Go cache**
and kept worktrees (`ReadWriteOnce`), both on the bench's disk under `$HOME/nova-bench`. They
are the thing that survives a Job: a fresh pod on the same node resumes with the same module
cache and the same mirror, so setup is paid once, as SPEC-JOBS §4 requires.

**The work queue.** A directory on a shared volume with the atomic take above —
`queue/lanes/{red,green,small,next}/` and `taken/`, the exact layout `nova-pulse cut` writes.
The kernel's ready set is not a thing Kubernetes exposes and is not invented here; the directory
*is* the ready set, and the take is its lock. A card whose Job dies is returned by the puller to
the lane it came from; `taken/` is the only place a card waits on a lease.

**Secrets: sealed, decrypted at apply, named key by key.** The store stays sops. At apply, the
plaintext is produced from the store and immediately sealed into a SealedSecret, so state and git
hold only the ciphertext, and the sealed-secrets controller decrypts it in-cluster into an
ordinary Secret. **That Secret holds only the keys one worker kind is entitled to, and no other
key exists in it** — it is generated per kind (`nova-secrets-<kind>`), not one fleet-wide Secret a
container could draw from. **A Job never uses `envFrom`.** `envFrom` projects every key a Secret
holds and cannot promise a subset, so it cannot keep the promise this section makes. Each
container names the exact keys it gets, one `valueFrom.secretKeyRef` per key: a **`go`** worker
gets `DEEPSEEK_API_KEY` and `GH_TOKEN`; a **`lisp`** worker gets `ANTHROPIC_API_KEY` and
`GH_TOKEN`; a **`docs`** worker gets `ANTHROPIC_API_KEY` and `GH_TOKEN`; a **`schema-leg`** worker
gets `ANTHROPIC_API_KEY` and `GH_TOKEN`. The `<kind>` Secret is sealed with exactly those keys,
so a key a worker is not named to hold is not in its Secret and cannot be projected even by a
mistake. The container's command is the same `nova-secrets exec --only <those names> --require
<the required one>` the native launcher runs, so `--require` refuses before the harness starts
if the Secret is short. **Environment only, never a `volumeMount`, never an image layer, never a
build argument.** A Job whose card names a secret path (`.key`, the store path, `auth.json`, a
`secretRef` mounted as a file) is refused by the puller, because the exec-env rule of
SPEC-SECRETS is the only delivery a process may have.

**Logs, the timeline, and the clip.** The pod's stdout is the harness log and goes to the
node's log store; the Job's start, end, exit and cost are appended to the **same `usage.tsv`**
that `nova-swarm` already writes, on the shared volume, so `nova-pulse status` and `progress`
read one schema and one file whether the card ran under the launcher or a pod. After each card
the pod's last step is the clip — commit the card's branch, harvest its `RESULT.md`, reset the
kept worktree to base — exactly SPEC-JOBS §6, so the next Job sees warmth and never another
card's uncommitted diff.

## Part 3 — What nova-sandbox still adds, and what the Studio keeps

**Inside the container, nova-sandbox is the second wall.** A container gives namespaces, cgroups
and a capability set; it does not give the card a *read set* and a *write set*. The pod still
sees the seat's env, the service-account token, the shared cache and the mirror, and the kernel
would let the harness read anything it can open. `nova-sandbox` keeps its `--read`/`--write`
lists and `--net-deny`, enforced by Landlock on Linux and its siblings elsewhere, so the wall
inside the wall is the same binary and the same rules as on the Studio. What it no longer needs:
the per-dispatch reference checkout (the mirror PV is it), per-user directories as the only
boundary (the pod's user namespace and the seat key are), lifetime leases and survivor-killing
(the Job and the pull take own those), and temp/`HOME` setup that Kubernetes already gives —
`HOME` is still set to the job data home, per sandbox rule 9, because a `HOME` outside the lists
is still a refusal.

**The Studio keeps running natively.** macOS, the coordinator's seat, the window that cuts,
harvests and decides; no k3s, no pods, no agent. The same `nova-sandbox` binary carries the
darwin wall, and the Studio's job is unchanged because a coordinator is not a worker.

## Part 4 — Three migration slices

Each slice shadows the last: the old path stays runnable until a measured number moves, and each
number is read from `usage.tsv` and the queue, never from a report body.

1. **Declare the fleet (Terraform).** Add `fleet/`, import the hand-built state of one spare
   bench behind the `light` role, and apply it while `bench-standard.sh` and today's launcher run
   untouched beside it. **Measure:** hand steps removed (the onboarding list's length before and
   after an apply) and drift lines `plan` catches that the hand path missed. **Rollback:** the
   module is additive to a spare bench; `terraform state rm` (or `destroy` on that bench) leaves
   the hand-built fleet exactly as it was.
2. **One bench, one kind, behind the launcher (k3s + Job per card).** Install k3s on one medium
   bench, run the puller on the `go` lane, and leave `nova-pulse launch` filling every free slot
   as it does today; the two share the bench and the `usage.tsv` schema. **Measure:** cards per
   hour, minutes per card (p50 and p90 against 4 / 15.7), and tokens per landed card. The first
   hurt is a slot idle while the window holds a card. **Rollback:** stop the puller and k3s;
   cards are files in `queue/` and `taken/` and return to the launcher's lanes unchanged.
3. **All benches, all kinds, retire the launcher.** Turn on lanes, the shared-clone batch and
   the clip on every Linux bench, sealed secrets from the store, and node labels for each kind;
   the puller becomes the only driver for admitted work. **Measure:** all three again — cards per
   hour, minutes per card, tokens per landed card — plus hand steps removed, against slice 2, and
   keep the pull only where the three move together; a lane that raises cards per hour while
   lowering landed quality is a regression and the measurement says so. **Rollback:** drain the
   k3s nodes and re-enable the launcher for the lanes that did not move; no card is ever only in
   a pod.

## Part 5 — Red tests

Each test is red before the work and names what it proves.

- `terraform-plan-on-a-fixture-shows-exactly-the-expected-drift` — a fixture bench whose state
  says one go version and whose declaration says another plans the one differing resource, and a
  clean fixture plans nothing.
- `a-job-with-the-fixture-card-produces-RESULT-md-and-a-usage-tsv` — the fixture card run as a
  Job lands a `RESULT.md` whose line 1 is the contract line and exactly one `usage.tsv` row, both
  visible to `nova-swarm result` and `nova-pulse progress` unchanged.
- `a-worker-that-dies-mid-card-returns-the-card-to-the-queue` — a Job killed before its clip is
  observed by the puller, and the card is back in its lane once and re-runnable, its partial
  `RESULT.md` kept as evidence.
- `a-card-that-names-a-secret-path-is-refused` — a card mentioning a `.key`, the store path or a
  file-mounted secret is refused by the puller before a Job is created, and no pod ever starts.
- `a-hand-edited-remote-file-is-shown-by-the-plan-not-by-the-declaration` — a fixture bench whose
  remote file is edited by hand while the declaration is unchanged plans that file, because the
  read-each-plan data source reports the observed hash and the resource's precondition fails;
  the same edit with only a `null_resource` trigger stays clean, proving the trigger is not the
  witness.
- `a-job-gets-only-the-keys-its-kind-names` — a fixture `<kind>` Secret carrying the named keys
  and one extra key turns the test red, and the pod's environment holds exactly the named keys
  through `valueFrom.secretKeyRef` and nothing else; a pod built with `envFrom` projects the
  extra key and is the mutation.
- `a-card-admitted-twice-is-refused-by-its-guid` — after a puller fails between the decrement and
  the Job, the retry charges one slot and not two, and a second take of the same `guid` already
  held is refused before a Job is created.
