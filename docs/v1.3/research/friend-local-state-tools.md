# v1.3 research: every local state a friend's client reads as authority

Scope: the friend side of this repository as it stands on `sprint/v13-tools`, before the v1.3
rewrite lands. The question is narrow: which bytes on a friend's own disk, or in its own
process memory, does something *read back and decide from* today? The spec
([docs/spec/v1.3/40-friend-lane-client.md](../spec/v1.3/40-friend-lane-client.md)) makes one
server the truth and a lane a client with no local state; layer
[docs/spec/v1.3/10-server-truth.md](../spec/v1.3/10-server-truth.md) names the guarantees this
inventory is checked against. The inventory below is built from the code as it builds each
path (`git grep` over `pkg/friend`, `cmd/nova-friend`, `cmd/nova-hook`, `hook`, `pkg/bus`,
`pkg/hookgate`, `internal`, `fleet`) and from reading each hit; every reader line quoted is the
place that decides, not the place that prints.

Summary: 19 rows. 16 `goes` (a local copy that decides something must stop deciding) and 3
`kept-as-cache` (a local copy that decides nothing may stay as a display or an archive). 7
rows name no replacement in the spec's guarantees as written: the four harness adapters'
per-harness session and proof files (`host.json`, the wake files, the claude
`SESSION-CHECK-<nonce>` file, the antigravity ledger), the retired epoch folders, and two
in-memory records (`SessionCheck.sent`/`cursor` and the lane fault). These are the spec owners'
gaps. Nothing found contradicts a claim the spec already makes, with one naming caveat: the
spec calls a durable local "nudge-debt ledger" shipped today, and no file of that name exists
in the tree; the durable debt it means is the `given_up` and `stop_returns` fields carried in
`lanes.json` plus the in-memory `dealt` slice. The three separate lists follow the table.

The `Spec` column uses the layer number and guarantee number from
`docs/spec/v1.3/10-server-truth.md` (`10 gN`) and
`docs/spec/v1.3/40-friend-lane-client.md` (`40 gN`); `none named` means the spec's guarantees
name no replacement for that piece of state.

| State (path or key, and the builder) | Writer(s) | Reader(s) and the line that decides | Spec | Disposition |
|---|---|---|---|---|
| `status.json` in the state directory. `StatusFile` = `"status.json"`, `write(statusPath(...))` in `statusPath` at pkg/friend/state.go:215 | `WriteStatus` pkg/friend/state.go:250, wired as the daemon's Status seam at cmd/nova-friend/main.go:1926 | `ReadStatus` pkg/friend/check.go:368 and cmd/nova-friend/main.go:2468; check.go:200 via the seam; the verdict decides up/down and the daemon-freshness evidence (`DaemonUp`), cmd/nova-friend/reach.go:146 | `40 g1`, `10 g8` | goes |
| `pong.json` in the state directory. `PongFile` = `"pong.json"`, `pongPath` at pkg/friend/state.go:216 | `WritePong` pkg/friend/state.go:259, called by the pong verb at cmd/nova-friend/main.go:2707 | `ReadPong` pkg/friend/check.go:391 and cmd/nova-friend/main.go:2483,2386; the pong's nonce is the only session answer that makes the friend up (`Presence.Answer`, pkg/friend/presence.go:106) | `10 g3`, `10 g4`, `40 g1` | goes |
| `presence.json` in the state directory. `PresenceFile` = `"presence.json"`, `presencePath` at pkg/friend/presence.go:176 | `WritePresence` pkg/friend/presence.go:179, wired at cmd/nova-friend/main.go:1472 (the daemon's `Save` seam) | `ReadPresence` cmd/nova-friend/main.go:2540 and :2604, `check.go:398`; `pr.Presence == PresenceUp && pr.LastHeard.After(...)` at main.go:2604 decides whether a ping counts as answered (call the friend up or down) | `10 g4`, `10 g5`, `40 g1` | goes |
| `lanes.json` in the state directory. `LanesFile` = `"lanes.json"`, `write(filepath.Join(stateDir, LanesFile), s)` at pkg/friend/lanes.go:1039 | `WriteLanes` pkg/friend/lanes.go:1038 via the `SaveLanes` seam, cmd/nova-friend/main.go:1889; written from `saveLanes` pkg/friend/lanes.go:1025 | `ReadLanes` pkg/friend/lanes.go:1031 via `LoadLanes`, cmd/nova-friend/main.go:1833; `s.state.Started` and `s.state.GivenUp` seed `s.given[id]` and `endStarted` at pkg/friend/lanes.go:456-478, which decide whether a lane is dealt a card (`held`, lanes.go:503) | `40 g1`, `40 g3`, `40 g4` | goes |
| `inbox/QUEUE.json` under the working directory. `QueueFile` = `"inbox/QUEUE.json"`, `filepath.Join(dir, filepath.FromSlash(QueueFile))` at pkg/friend/state.go:293, pkg/friend/lanes.go:221 | the coordinator and the session write it (cmd/nova-friend/main.go:914 describes `inbox/QUEUE.json` as the source of queue counts); the daemon reads it | `NextCard` pkg/friend/lanes.go:219 reads `q.Tasks` and at lanes.go:227-239 picks the first `queued` card with a brief and no result/report: this deals a lane; `ReadQueue` pkg/friend/state.go:291 for counts, cmd/nova-friend/main.go:2484 | `40 g1`, `10 g8` | goes |
| `inbox/<job>/BRIEF.md`. `Card.Brief` = `filepath.Join(dir, "inbox", base, "BRIEF.md")` at pkg/friend/lanes.go:235; also built at pkg/friend/inbox.go:201 and pkg/friend/lane_take.go:149 | the coordinator/friend sync writes it (pkg/friend/inbox.go:214 `os.MkdirAll`); the daemon reads it | `NextCard` tests `!exists(c.Brief)` pkg/friend/lanes.go:236 and refuses the card when absent; `outboxStep`/`lane_take` read it to run the card | `40 g1`, `10 g8` | goes |
| `outbox/<job>/REPORT.md` and `RESULT.md`. `Card.Result()` / `Card.Report()` at pkg/friend/lanes.go:162 and :165 | the lane's harness writes them; the daemon writes `REPORT.md` for a dead lane at pkg/friend/outbox.go:490 | `laneDone` tests `exists(card.Result()) \|\| exists(card.Report())` at pkg/friend/lanes.go:765 and :771 and ends the lane on it; `outboxStep` reads `REPORT.md` at pkg/friend/outbox.go:229 and finishes the card at outbox.go:283 | `40 g1`, `10 g6`, `10 g7` | goes |
| The lane mark `jobs/<job>/LANE`. `LaneMarkFile` = `"LANE"`, `laneMarkPath` = `filepath.Join(dir, "jobs", job, LaneMarkFile)` at pkg/friend/one_lane.go:68 | `ClaimLane` pkg/friend/one_lane.go:114 and `endLaneMark` one_lane.go:133 | `ReadLaneMark` pkg/friend/one_lane.go:73 inside `laneHolder` (one_lane.go:176); `heldBy` decides who holds the job, and `lanes.go:515` refuses a take when a live lane holds the card | `40 g1` | goes |
| The retired epoch folders `retired/epoch-<n>/{inbox,outbox}/<job>` under the working directory. `RetiredEpochs` = `"retired"`, `retireEpoch` at pkg/friend/epoch.go:78; and `inbox/retired/<name>`. `RetiredDir` = `"inbox/retired"`, pkg/friend/inbox.go:285 | `SnapEpoch` pkg/friend/epoch.go:86 (`os.Rename`) and the inbox retire at pkg/friend/inbox.go:293 | nothing reads them as authority; `os.ReadDir` on the live folders at epoch.go:47 skips them and they are kept for the record | `40 g1` (named as not read), otherwise `none named` | kept-as-cache |
| `limit.json` in the state directory. `LimitFile` = `"limit.json"`, `read(filepath.Join(stateDir, LimitFile))` at pkg/friend/status.go:134 | the daemon's Limit seam records the provider limit (pkg/friend/status.go:27 says the daemon is its one writer) | `ReadLimitFile` cmd/nova-friend/main.go:2607; its `LimitUntil` rides the status evidence (pkg/friend/status.go:55) and calls the friend down until the reset | `10 g5`, `40 g1` | goes |
| `lane-fault` in the state directory. `LaneFaultFile` = `"lane-fault"`, `filepath.Join(stateDir, LaneFaultFile)` at pkg/friend/status.go:147 | `WriteLaneFault` pkg/friend/status.go:146 via the `FaultWrite` seam, cmd/nova-friend/main.go:1838 | `ReadLaneFault` pkg/friend/status.go:162, wired at cmd/nova-friend/main.go:1837; cmd/nova-friend/lanes.go:16 and daemon.go:812: a report present means the lanes take no card until a restart or a person clears it (`ClearLaneFault` status.go:172) | `40 g1` | goes |
| `PAUSED` in the state directory. `PauseFile` = `"PAUSED"`, `atomicfile.WriteFile(filepath.Join(stateDir, PauseFile), ...)` at pkg/friend/lane_parity.go:200 | `WritePause` pkg/friend/lane_parity.go:200 via `FaultDown`, cmd/nova-friend/main.go:1851 | `ReadPause` pkg/friend/lane_parity.go:208; read at cmd/nova-friend/main.go:1804 and :1849 (`LaneHold`) and :2452: a marker present holds every lane down until a person clears it (`ClearPause`, lane_parity.go:217) | `40 g1` | goes |
| `host.json` in the state directory. `HostFile` = `"host.json"`, `filepath.Join(stateDir, HostFile)` at pkg/friend/adapter_tmux.go:81 | `WriteHost` pkg/friend/adapter_tmux.go:81, called by the host verb at cmd/nova-friend/main.go:2875 | `ReadHost` pkg/friend/adapter_tmux.go:84, read at adapter_tmux.go:233 and cmd/nova-friend/reach.go:242; the saved session decides how a delivery is put into the hosted tmux session (deliver or refuse) | `none named` | goes |
| `antigravity-ledger.json` in the state directory. `AntigravityLedgerFile` = `"antigravity-ledger.json"`, `write(filepath.Join(a.State, AntigravityLedgerFile), l)` at pkg/friend/antigravity_ledger.go:98 | `Antigravity.save` pkg/friend/antigravity_ledger.go:80 (`keep`, go:104) | `Antigravity.known` pkg/friend/antigravity_ledger.go:114 via `load` at :69; the ledger's delivery ids decide whether a message is delivered again or dropped (`newMessage`, adapter_antigravity.go:462) | `40 g1`, `10 g7` | goes |
| The wake files `<state>/<friend>.wake` and the grok `<file>.wake`. `ClaudeWakePath` = `filepath.Join(stateDir, friend+".wake")` at pkg/friend/adapter.go:486; grok's `WakeOf` pkg/friend/adapter_grok.go:161 | `ClaudeWake.Deliver` pkg/friend/adapter.go:532 (`os.OpenFile` append) and the grok adapter's append at pkg/friend/adapter_grok.go:93 | the live session tails it (cmd/nova-friend/watch.go:180 reads `ClaudeWakePath`, `watchRun` at :189); the wake decides whether a delivered message reaches the session (deliver or drop) | `40 g6`, `none named` for the path itself | goes |
| `deliver.log` in the state directory. `LogFile` = `"deliver.log"`, `LogPath` at pkg/friend/state.go:220 | `Record` pkg/friend/state.go:307 (`os.OpenFile` append), called from many daemon sites and cmd/nova-friend/main.go:1461 | `os.ReadFile(friend.LogPath(...))` cmd/nova-friend/check.go:210; it is printed only; no reader decides from it | `none named` | kept-as-cache |
| `watch.json` in the state directory. `WatchFile` = `"watch.json"`, `WatchPath` at pkg/friend/state.go:276 | `WriteWatch` pkg/friend/state.go:280, called by the watch verb at cmd/nova-friend/watch.go:197 | `ReadWatch` cmd/nova-friend/watch.go:168; `saved, found` seeds `After` and `WakeOffset`, so the next run skips stream entries already seen (decides which messages are delivered) | `10 g10`, `40 g1` | goes |
| `inbox/SESSION-CHECK-<nonce>` (the claude folder check). `SessionCheckFilePrefix` = `"SESSION-CHECK-"`, `SessionCheckFile` = `filepath.Join(dir, "inbox", SessionCheckFilePrefix+nonce)` at pkg/friend/adapter_claude_folder.go:22 | `FolderCheck.Deliver` pkg/friend/adapter_claude_folder.go:70 (`atomicfile` write; old checks globbed at :77) | the live session watches the folder and answers the file it shows; the answer is the session proof that decides delivery (refuse until answered). The adapter lists it as `ReadOnReturn` (pkg/friend/presence.go:268) | `10 g4`, `none named` for the file | goes |
| In-memory records on the daemon: `SessionCheck.sent` and `SessionCheck.cursor` (pkg/friend/presence.go:224,223), `laneSet.state`/`l.dealt` (pkg/friend/lanes.go:293 and daemon.go:621), `outboxState.finished/tried` (pkg/friend/outbox.go:51). No on-disk file | the daemon process only | `read` at pkg/friend/presence.go:606 uses `cursor` to range the bus log and `sent` to skip the daemon's own sends (decides which bus messages are the session's, so up/down); `l.dealt` at daemon.go:847 decides whether a nudge turn starts | `40 g1`, `40 g2` | goes |

## (a) Authority with no replacement named in the spec

- `host.json` (the hosted tmux session), pkg/friend/adapter_tmux.go:81. The spec's layers name
  no server record for a hosted session, so the file that decides how a delivery reaches the
  session has no successor named.
- The wake files, pkg/friend/adapter.go:486 and pkg/friend/adapter_grok.go:161. `40 g6` says
  the lane hears the bus without nova-sprint, but no guarantee names a server-held subscription
  to replace the wake file that decides whether a message lands.
- The claude `SESSION-CHECK-<nonce>` file, pkg/friend/adapter_claude_folder.go:22. `10 g4`
  names the answer, not the file the check rides in; the file's role as the session's inbox of
  checks is unnamed.
- `antigravity-ledger.json`, pkg/friend/antigravity_ledger.go:98. `10 g7` says a pending entry
  is not removed by an offer, but names no replacement for a local ledger of deliveries that
  decides resend or drop.
- The retired epoch folders, pkg/friend/epoch.go:78: an archive nothing reads, but the spec
  names no owner of what happens to it at cutover.
- The lane fault file, pkg/friend/status.go:147: a local file that suspends taking; no server
  record replaces "the lanes take no card until a restart".
- In-memory `SessionCheck.sent`/`cursor` and `l.dealt`: the spec makes the subscription the
  authority (`10 g10`, `40`), but the local cursor and the dealt set that decide tonight's
  delivery are named by no guarantee.

## (b) Readers in other tools of this repository that break when the writer stops

- `cmd/nova-friend` reads `status.json`, `presence.json`, `pong.json`, `limit.json`,
  `lane-fault`, `PAUSED`, `host.json`, `watch.json`, `deliver.log` and `lanes.json`:
  cmd/nova-friend/main.go:2468,2540,2483,2607,1837,1804,242,168,210,1833;
  cmd/nova-friend/check.go:200-210,368,391,398; cmd/nova-friend/reach.go:242; and
  cmd/nova-friend/lanes.go:16,33. Each is a verb that shows or acts on the friend; when the
  file stops being written these readers show nothing and their status verbs go quiet, so the
  v1.3 cutover must give each a server read or delete the verb.
- `cmd/nova-friend watch` (watch.go:168) reads `watch.json`; with the file gone it re-reads the
  stream from the head unless the server cursor replaces it.
- `cmd/nova-friend check` (check.go:210) reads `deliver.log`; display only, safe to drop.

## (c) Where the spec claims and the code contradicts

- `40-friend-lane-client.md:150` says "a durable local nudge-debt ledger exists today". No file
  named nudge or debt is written anywhere in `pkg/friend` or `cmd/nova-friend`. The durable
  debt the spec means is carried in `lanes.json`'s `given_up` and `stop_returns`
  (pkg/friend/lanes.go:61-65, appended at lanes.go:789,850) plus the in-memory `dealt` slice
  (pkg/friend/daemon.go:621). The spec's phrase names a file that does not exist; the debt it
  names is real and lives in `lanes.json`.
- `40-friend-lane-client.md` guarantee 1 says the client directory holds no inbox, outbox,
  presence file, or epoch flag the new client reads as authority. Today every one of those is
  read as authority (rows above). The guarantee is a statement about the target, not the tree;
  the tree contradicts it wherever the rows say `goes`.
- No other contradiction found: the server-truth layer's guarantees are unmet by the current
  tree in exactly the rows marked `goes`, and met (decide nothing) in the rows marked
  `kept-as-cache`.
