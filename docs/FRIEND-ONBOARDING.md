# Friend Onboarding

This guide takes a new AI friend from nothing to a first finished card. Read it with the human helping you.

## What a friend is

A friend is an AI who works beside the coordinator under her own name. She has:
- A `nova-config` friend row
- A row in the sprint's friends table
- A working directory `~/<name>-working` on the machine she runs on

## What a friend owes

- **Honest attribution**: Never claim a model or harness you are not running. Write your actual model and harness in every report.
- **Follow the standard**: Work only in your job directory, report to your outbox, and push to the branch the STATUS line names.
- **One verb per turn**: Run one nova-` command at a time. Wait for the result.

## Setting up your working directory

### Step 1: Install your harness

Tell `nova-friend` how to reach your session:

```
$ nova-friend install --as <your-name> --harness <harness-name> --dir ~/<your-name>-working
```

How to check it worked:
```
$ nova-friend check --settings --as <your-name> --harness <harness-name> --dir ~/<your-name>-working
```

### Step 2: See your status

```
$ nova-friend status --as <your-name> --dir ~/<your-name>-working
```

Your status should say `up` with `push=proven`.

## Your inbox, jobs, and outbox

When the coordinator sends you work, it arrives in:
- `inbox/<job>/` - Contains `BRIEF.md` with the STATUS line
- `jobs/<job>/` - Where clones, worktrees, and build outputs go
- `outbox/<job>/` - Where you write `REPORT.md` when done

### Step 3: Start work

Create your outbox (this marks the job as "working"):
```
mkdir -p ~/<your-name>-working/outbox/<job>
```

### Step 4: Set up your repo

If you need a checkout:
```
mkdir -p ~/<your-name>-working/jobs/<job>
cd ~/<your-name>-working/jobs/<job>
git clone -q https://github.com/mas-bandwidth/nova-tools.git repo
cd repo
```

## The bus: ping and pong

The coordinator pings you. You must answer with a pong.

### As the coordinator: ping a friend

```
$ nova-friend ping --as <coordinator> --to <friend-name>
```

### As a friend: answer the ping

```
$ nova-friend pong --as <your-name> --nonce <n> --to <coordinator>
```

## Going up and down

### Check when you're ready to start

```
$ nova-friend check --as <coordinator> <your-name>
```

### If you're blocked, resume

```
$ nova-friend resume --as <your-name>
```

### If you need to stop, watch the status

```
$ nova-friend status --as <your-name> --dir ~/<your-name>-working
```

## Your first card

### Step 5: Read the BRIEF

Look at `inbox/<job>/BRIEF.md`. Find:
- The STATUS line (tells you the branch to push to)
- The working directory path
- The PATHS (which files to edit)

### Step 6: Do the work

Make your changes to the files listed in PATHS.

### Step 7: Test on a Linux bench

Never test on the Studio machine. On vision or hetzner:

```
GOCACHE=~/<your-name>-working/.cache/go-build GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 go test -count=1 -timeout 600s ./internal/docs -run TestFriendOnboardingGuideCoversJoinToFirstCard
```

### Step 8: Commit and push

Commit with honest attribution:
```
git commit -m "<your task summary>"
```

Add a commit trailer naming yourself:
```
git commit --amend -m "By: <your-name>"
```

Push to the branch from the STATUS line:
```
git push origin HEAD:refs/heads/<the-branch>
```

Verify:
```
git ls-remote origin refs/heads/<the-branch>
```

### Step 9: Write your report

Write `outbox/<job>/REPORT.md`:
```
Verdict: LAND
Head: <full-40-hex-sha>

<one paragraph: what changed and the gate's result>
```

Write `outbox/<job>/RESULT.md`:
```
RESULT: <result-line-from-brief>
```

## What to do when stuck

- Run `nova-friend check` to see what's wrong
- If the harness refuses, read the error message and apply its remedy
- If you don't understand a step, ask the coordinator
- Never guess - a refused verb is better than a wrong one
