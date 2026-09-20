RESULT tools22-pre-244-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#244 at head 9efe6d40c337: nova-run: SPEC draft 1
PREREAD 244 claims=24 proven=0 unproven=24 defects=0 high=0
PR 244
HEAD 9efe6d40c337e5a19256365f3f503d466e79dbb6
BASE dev
MERGE-BASE 31e351956a59a63e3da1b323abecb63da7b92cdd
BEHIND 39
FILES 1 production, 0 test
LINES +1516 -0
1. nova-run is one binary at the line layer that brings one declared line to a running state on one box, from one declaration, with one command, over and over.
2. One declaration, one line, from a flag, in git — no default path, no search of cwd, no environment variable consulted for anything, no binary found by PATH.
3. Every path is absolute, comes from the declaration, and must already exist or be named as a refusal; process.log is created when absent at mode 0600 and never truncated.
4. Two scopes: ACCOUNT (unix user, age key, wall, no self) and HOME (account plus self and presence); account runs steps 1-5 and stops, home runs all eleven.
5. Measure, then act; a step already done prints SKIP and changes nothing; readiness is never a memory.
6. The home is cloned or fast-forwarded, never rewritten; no reset --hard, checkout -B, stash, clean, --force, or force push.
7. Writes nothing inside a line's home; never commits; home.path is permitted in its own sandbox.write, every other working copy is refused.
8. A command is argv, never a shell; version_argv, canary.argv, start_argv, and ready.argv are executed directly.
9. The files a harness loads are the line's own, at declared absolute paths, and this tool makes links and never copies.
10. The harness is present at the pinned version, or the line does not start; a pin sees a version, not a build.
11. A canary runs before the line does, through the same nesting the line will use; the wrapper's line is read before the number; expect_exit: 125 is refused.
12. READY is a measurement, and it is the only thing that makes a line up; each poll is bounded; reads from process.log as a file starting at the offset recorded before exec.
13. Every wait has a deadline and a default action, and the whole run has a budget; the line itself is started in a process group of its own.
14. This tool never opens a key file; secrets arrive by exactly one route: nova-secrets exec; no value, fragment, or length appears on any line.
15. The nesting order is load-bearing and fixed: secrets outside, wall inside, harness innermost; every child's environment is built, not inherited.
16. A line whose harness takes its credential from a file is refused by name with two remedies.
17. The wall is not optional and is proved before the first start; up on linux refuses at step 5 on every declaration today because nova-sandbox has a darwin body and no other.
18. One process per line per box, and its data home is its own; the state file records pid, start time, home_env, and process.log.
19. role is a label this tool prints and never interprets, including "keeper".
20. What this tool can hold about the self, it holds; what it cannot, it says; down pushes home.path to its own remote; holder= is on RUN UP OK.
21. Bounded output, by design, at the largest plausible state; every subprocess has stdout/stderr bounded at 64 KB; process.log is created at mode 0600 and never truncated.
22. Every refusal says what the input wants and names the next command; a fact of the declaration is exit 2, a fact of the box is exit 1.
23. Everything it reads is data, and nothing it reads can author a line.
24. Nothing in this binary is specific to us; no forge, branch name, bench path, harness name, or friend's name in source or runnable examples.
DEFECTS none
QUESTIONS FOR THE REVIEWER
1. What implementation work remains for nova-run to match this specification?
2. When is the darwin body for nova-sandbox expected to be available?
3. How does ideas#766 acceptance flow work with the keyless line case?
Left owed
Nothing owed; this PR adds a single documentation file (SPEC-RUN.md) with no code or test changes.

git status --short
git rev-parse HEAD
9efe6d40c337e5a19256365f3f503d466e79dbb6
