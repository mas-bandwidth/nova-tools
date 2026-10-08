# nova-friend dogfood, 2026-10-06 (dsh)

Tool: nova-friend. Build: `nova-friend devel darwin/arm64 go1.26.6`.

Run cold, from the binary's own help (`-h`, `help`, `<verb> -h`) and the tool's page
(`docs/SPEC-FRIEND.md`) only, no source read. Every verb ran at least once: the plain verbs against
a scratch friend directory under the job tree, and the store verbs against a scratch bus (a Redis on a
bench, reached through an ssh tunnel; `NOVA_BUS_REDIS` names it, and the scratch store holds the two
roster names the tool's own examples use). The daemon ran for real against a hosted scratch TUI, so the
push proof, a delivered turn, a live `status` and `serve` were all exercised end to end. Two load paths
could not run at all: `install` and `ping-install` refuse while the binary and the home directory are
both on the same volume, and the whole job tree is on one, so their refusal path ran and their write
path did not; that limitation is part of finding 4's context, not a finding of its own.

## 1. `check` reads flags after a friend name as more friend names — URGENT

**Command:**

    nova-friend check bob --since 1h --json

**Printed:**

    CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=-
    CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=-
    CHECK BUS friend=bob real_since=0 last_real=-

then the same five lines for `friend=--since`, `friend=1h` and `friend=--json`, ending

    CHECK OK friends=4 ok=0 broken=0 deaf=0 silent=0 down=4 untrue=0

exit 1, and the plain line rendering, not JSON. `nova-friend check --json --since 1h bob` on the same
store prints one JSON object for one friend, so the order alone decides.

**Expected:** the usage line is `nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]` — friends before flags — so I typed it in the printed order
and expected one friend judged over 1h as JSON. Instead the flags became friend names, the window and
`--json` never applied, and nothing refused: a wrong result (`friends=4`, three of them `--since`,
`1h`, `--json`) with no error, in the order the help itself teaches.

**Grade:** URGENT

## 2. `wall` is a verb and no help names it — NEXT

**Command:**

    nova-friend wall -h

**Printed:**

    Usage of wall:
      -config-dir string
        	the friend's CLAUDE_CONFIG_DIR, and the HOME inside the wall

exit 0, with the full flag list. `nova-friend wall --dir <dir> --deny <path> -- /bin/echo hi` runs the
command inside the wall and answers its exit.

**Expected:** the banner's usage block and the bare command's verb list name every verb (`run, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong, status, refuse-go, resume, serve, version`); neither names `wall`, though it answers `-h` and runs, and
`docs/SPEC-FRIEND.md` documents it (buds-in-the-wall-r.w5). A stranger cannot discover the one verb
that shows what the wall does.

**Grade:** NEXT

## 3. the tool's page names `nova-friend screen`; the tool has no such verb — NEXT

**Command:**

    nova-friend screen -h

**Printed:**

    FRIEND REFUSED: "screen" is no verb and no file; the verbs are run, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong, status, refuse-go, resume, serve, version, and a file is given by its path (./screen); run: nova-friend help

exit 2.

**Expected:** `docs/SPEC-FRIEND.md` (Hosted in tmux, The screen) says "The last screen of a hosted
friend is the pane's capture, the verb `nova-friend screen`", so after hosting a session I expected
to read its pane through the named verb. The refusal itself is well formed; the page and the tool
disagree about the verb existing at all, and the page is where a stranger reads first.

**Grade:** NEXT

## 4. `ping-install`'s volume refusal prescribes `install` — NEXT

**Command:**

    nova-friend ping-install --as ada --every 30s

**Printed:**

    PING-INSTALL REFUSED: binary on a removable volume: launchd starts it and it does nothing; the home directory is not off /Volumes, so there is nowhere to copy it; move the binary off /Volumes and run nova-friend install again; run: nova-friend help

exit 2.

**Expected:** the verb that refused is `ping-install`, so I expected its remedy to say "run
nova-friend ping-install again"; it says `install`, and a stranger who moves the binary and follows it
runs the heavier verb and installs a friend agent, not the wake-ping agent they asked for. The same
sentence refuses correctly under `install` itself; only the copy is wrong. (Both load paths stayed
refused in this run: the job tree sits on one volume, so the remedy's "off /Volumes" was out of reach
inside the card's walls — a documented rule, met exactly as written.)

**Grade:** NEXT

## 5. a scratch run's sprint half reaches the machine's real sprint server by default — NEXT

**Command:**

    nova-friend ping --as ada --wake --to-friends

**Printed:**

    WAKE OK every=0s within=3m0s never_wake=
    WAKE NOTE 2026-10-06T20:37:54Z the wake ping to <a name from the machine's real friend rows> was not sent: <the name> is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add <the name> --slots 1 --tiers flash --as <you>, then nova-config apply; trying again next pass
    WAKE STOP

exit 0. (The roster name is redacted here; the line printed it whole.) The daemon met the same
default: `run` with no `--server` beat to the sprint server at 127.0.0.1:6390 and `status` printed
`STATUS NOTE the last beat failed: friend beat refused: nova-sprint friend beat: no friend bob on the friends table (friends: <the machine's real roster>): its row is nova-config's friend row; run: nova-sprint friend sync`.

**Expected:** with the bus pointed at a scratch store I expected the run to touch only scratch things,
or to say which sprint server it read before real names appeared in my output. `run -h` does document
the default (`NOVA_SPRINT_SERVER`, else 127.0.0.1:6390), and the beats were refused and woke nobody,
so nothing was harmed; but the first sign that the scratch run was reading the real machine's friend
rows was a real friend's name in a scratch refusal, and on a machine whose bus is also the real one the
wake pass would have pinged real friends from a dogfood run. A `--server` that must be given when the
bus is not the fleet's, or the address said before the first ask, would keep a cold run inside its
walls.

**Grade:** NEXT

## 6. the daemon's record cuts a remedy mid-word — NEXT

**Command:**

    nova-friend run --as bob --harness tmux --dir <dir> --coordinator ada

**Printed:**

    typed into friend-bob; the turn runs after this
    RUN 2026-10-06T20:35:29Z push proof: CHECK OK harness=tmux took=1.0360995s
    RUN 2026-10-06T20:35:29Z inbox: the server did not say which cards are on her row: friend cards is not served (card daemon-writes-every-taken-card3 adds it), and the worker view did not answer: the sprint server at 127.0.0.1:6390 refused the view (404 Not Found): nova-sprint view worker: bob is no fleet member and no friend of the sprint; a reader's cards are its queue: nova-sprin; nothing written or retired until it does

the third line ends `nova-sprin` mid-word (`nova-sprint queue --as bob` is what the same note says
whole under `status`).

**Expected:** a remedy a reader can act on in one turn; I expected the record's bound to cut between
words or mark the cut, not to leave half a command name. The whole remedy does survive in `status`, so
this is friction in the record, not a lost remedy.

**Grade:** NEXT

READ 7/10 — the banner answers what, how and how-to, every verb's `-h` exits 0 with its flags,
defaults and exit codes, and every refusal I met was one line naming every valid name with a runnable
remedy (the harness list, the roster names, the bench line for go); the score is held down by the
usage list that omits the live `wall` verb, the page that names a `screen` verb the tool refuses, and
a usage line for `check` whose own order leads into finding 1.

USE 7/10 — the whole protocol ran cold against a scratch store: ping, pong and wait-pong answered in
milliseconds, `host` opened a real pane, the push proof passed, the daemon delivered a real turn and
`status` reported it with evidence, `serve`, `wall` and every refusal behaved as documented; held down
by finding 1's wrong result in the natural flag order, the scratch run touching the machine's real
sprint server before saying so, and the two launchd load paths that a volume-bound tree cannot run at
all.

urgent=1 next=5
