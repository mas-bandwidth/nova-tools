OBJECT 939b3b324ee68b34d57bbe3163df302e81d36555

I read docs/spec/v1.3 at 939b3b324ee68b34d57bbe3163df302e81d36555, which is still the last commit that touched that directory on sprint/v13-tools (tip 174bac86d408c4509343702eb52165a306a19c05). The round-2 rulings I am not objecting to: the link is mandatory, a send to an unhooked friend is refused, Redis is the one server, state lives on the server, one claim per card, no take-back while a run is uncertain, a late finish is recorded and held.

1. File: docs/spec/v1.3/50-sprint-delivery.md
Old sentence: "A card is dealt only to a friend who is hooked into her psychic link and has passed the self-test of layer 30 guarantee 6, whose binding is proven and unexpired, and whose displayed status is up; never to a deaf or down friend (layer 20 guarantees 12 and 17)."
New sentence: "A card is dealt only to a friend who is hooked into her psychic link and has passed the self-test of layer 30 guarantee 6, whose binding is proven and unexpired, and who is internally up; never to a deaf or down friend. Displayed status is not the gate: deaf displays up (layer 20 guarantee 17)."
Why: the same sentence requires displayed up and forbids a deal to a deaf friend. Layer 20 guarantee 17 displays deaf as up, so a check of the display deals to a deaf friend. Layer 40 already gates Deal on hooked, which excludes deaf.

2. File: docs/spec/v1.3/60-state-machines.md
Old sentence: "Deal only to a friend who is hooked, self-tested, proven and displayed up (50 guarantee 7; Reconciled (a) for which clock the deal reads)."
New sentence: "Deal only to a friend who is hooked, self-tested, proven and internally up (50 guarantee 7; Reconciled (a) for which clock the deal reads). Displayed up includes deaf, and deaf is not dealt a card."
Why: machine M6 copies the contradiction from guarantee 7 into the action the model will check.

3. File: docs/spec/v1.3/60-state-machines.md
Old sentence: "The deal reads session proof: a card is dealt only to a friend whose binding is proven and unexpired and whose displayed status is up."
New sentence: "The deal reads session proof: a card is dealt only to a friend whose binding is proven and unexpired and who is internally up (hooked). Deaf displays up and is not dealt a card."
Why: Reconciled (a) states the same wrong gate. Deaf would pass it.

4. File: docs/spec/v1.3/60-state-machines.md
Old sentence: "At most `width` lanes hold taken or running cards (40 guarantee 3)."
New sentence: "At most `width` lanes hold taken, running, or needs-reconciliation cards (40 guarantees 3 and 9)."
Why: layer 40 says a client with width lanes already taken, running, or needs-reconciliation is not dealt another, and a needs-reconciliation claim counts against width. The invariant omits that state, so a check would allow a new deal while an uncertain run still holds its card.

5. File: docs/spec/v1.3/30-harness-hookin.md
Old sentence: "If a challenge window ($T_{challenge}$) expires without a `prove` call, the server transitions the lease to `AWAY` (`reason: lease_expired`)."
New sentence: "If a challenge window ($T_{challenge}$) expires without a `prove` call, the server transitions the lease to `AWAY` (`reason: challenge_timeout`)."
Why: layer 20 names challenge_timeout for a challenge that expires, and lease_expired for a proven lease that passes its deadline. The Antigravity subsection uses the wrong reason, so guarantee 3's structured reason cannot be tested on that harness.

6. File: docs/spec/v1.3/30-harness-hookin.md
Old sentence: "Re-arm both Monitors before they expire."
New sentence: "Re-arm the one link Monitor before it expires."
Why: the Claude Code subsection says one Monitor runs `nova-bus recv --forever --exec`, and that no second Monitor on `nova-hook hook` is needed or kept. "Both" names a process that subsection retired.

7. File: docs/spec/v1.3/30-harness-hookin.md
Old sentence: "The harness, not the session, runs layer 20's `nova-hook wait` as a background command."
New sentence: "The harness, not the session, runs one monitor whose command is layer 20's `nova-bus recv --as <name> --forever --exec <deliver>`."
Why: I run on this harness. The link that has been measured here is that monitor: each stdout line wakes the turn while the session works, including during a bench gate, and a send-to-self arrived in the turn (01M4XDKAG9K0N3M1CF1JFXQS3M). Layer 20 guarantee 13 already names that form for a monitor harness. The one-shot `nova-hook wait` is not the recipe this installation measured. The rest of that Grok paragraph should follow the monitor: the first action of the waking turn is the prove when the line is a challenge, and the monitor stays up, so it is not restarted per message.

8. File: docs/spec/v1.3/20-comms.md
Old sentence: "Upon message delivery or challenge issuance, it writes arrival JSON and the next valid nonce to stdout and exits with status 0 within 50ms of the server event."
New sentence: "Upon message delivery or challenge issuance, it writes arrival JSON and the next valid nonce to stdout and exits with status 0. The wait returns on the server event, with no poll interval. A 50ms bound is a bench measurement, not a unit-test failure."
Why: the 50ms figure is wall-clock latency inside a correctness guarantee. A unit test takes no real time. On a loaded bench the same correct wait fails the number. The guarantee that matters is that the wait is the event, not a poll.
