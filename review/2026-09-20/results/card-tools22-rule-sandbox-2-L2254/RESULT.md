RESULT tools22-rule-sandbox-2-L2254 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 2 says?
BLOCKED head=fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree. Use '--' to separate paths from revisions, like this: 'git <command> [<revision>...] -- [<file>...]'
SPEC docs/SPEC-SANDBOX.md:2254 rule 2
PKG internal/sandbox

ASK (from STEP 2, reading lines 2234–2280): The implementation must run the entire test suite under an unprivileged user identity and assert (via os.Geteuid() != 0 on Unix) that the process is not root before treating a test result as trustworthy.

Greps attempted (none could succeed without the repo):
  grep -rn "Geteuid" --include='*.go' ./internal/sandbox/   → repo does not exist
  ls internal/sandbox/   → directory does not exist

The job directory is an empty git repo (zero commits). The BASE repo at dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3 lives at /tmp/nova-tools-mirror.git, which the launch wall explicitly denied ("WALL REFUSED denied /tmp/nova-tools-mirror.git"). No clone of the source tree was created inside the sandbox; consequently `cd repo && git rev-parse HEAD` cannot print the expected hash, and STEP 1 stops here.

Left owed
(Reproduction outside the sandbox: clone /tmp/nova-tools-mirror.git into repo, checkout 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 on branch dev, then re-run this card.)

git status --short
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
