# Sprint dashboard: the specification (the owner's decisions of 2026-10-02, written at the clock below)

This is the sprint dashboard: a page that is a second view of `nova-sprint where --json`,
served by `nova-sprint dashboard`. The terminal table that `where` draws stays the
canonical view, and its output is locked (internal/sprint/TABLES.lock); the page reads
the same JSON and adds nothing to the sprint. The one thing the served copy carries beyond
where's own is the fix marks (Progress bar, "Fix"), each absent when it is zero, so a copy with
no card awaiting rework is served exactly as where prints it. The page's files live in
`internal/sprintdash/page/` and are embedded in the binary, the wordmark's face
(Nunito 800, SIL Open Font License, its licence beside it) included, so the page loads
nothing from anywhere else.

A user tunes the page by editing this specification and the page together: a change to
`index.html` or `app.js` is a change to the line below that says it, in the same commit.
`TestDashboardPageIsTheSpec` (internal/sprintdash) holds part of the two equal: it reads
the quoted rules of this file and the page's markup (and app.js's legend states) and
compares the panel titles and their order, each table's column headers and their order,
which columns are numeric (right-aligned), the hero tiles and their labels, the progress
bar's label and legend, the header's wordmark and pills, and the footer line. A change to
any of these in one without the other is red. The rest (sizes, colours, motion, layout)
is checked by eye against this file, at the widths its Responsive section names. In
this repository the specification is locked (its lock sections): a line changes only
with the owner's words, quoted with the date. A copy of nova-tools is its owner's to
tune the same way.

## Serving and publishing

`nova-sprint dashboard [--listen <address:port>[,<address:port>...] | none] [--pull <address:port>[,<address:port>...] | none | <url>] [--logo <file>] [--every 1s]`
serves the page on each `--listen` address (default `127.0.0.1:7390`), one listener
each, sharing one cached copy of the sprint. It reads the sprint in-process the way
`where --json --cards` does (through the sprint's server when `NOVA_SPRINT_SERVER` names
one, else on the store `--redis` names), in one place, once per `--every` whether or not
a page is open, so the copy is never older than a tick (the owner, 2026-10-04: "I need to
be able to always trust the dashboard"; "Golang nova-tools and nova-sprint verbs only"):
`/api/sprint` is that copy, with the build number and the throughput, and `/events`
pushes each new copy as it is read (server-sent events); a request between two ticks is
answered from the copy and reads nothing. The page is served by this verb from the
installed release and by nothing else: no side build, no other server in front of the
reads. The pull routes a worker reads its own view from (`/friend/<name>`,
`/machine/<name>`, their `/api/` and `/events/` forms) are served on the `--pull`
listeners (default `127.0.0.1:7395`), never on the page's, from the same copy:
[SPEC-SPRINT.md](SPEC-SPRINT.md), the dashboard. The copy also names the ready
buffer: `ready`, the ready primaries across streams; `width`, the total width of the
members that are up; `buffer`, the string `"<ready>/<2*width>"`; and `low`, true while
`ready` is under `width`. Every answer
is no-store; the page reloads itself when the build number changes (a new binary, or a
new `--logo` file). A read that fails holds the last good copy, the page says nothing,
and the dashboard's output takes one line per new failure, and once a minute a line of
the reads' count, failures and read times. `--logo` names an image file
served as the logo and the favicon; with none, the slot renders nothing.
`/healthz` answers `ok`.

The reads have one poller: the verb makes the first read before any listener opens, then
one each `--every` (back to back when a read takes longer than `--every`, never two at
once), and while it polls a page, a pull route or a stream reads the copy and never the
sprint, however many are open. A failed read sets `ok` false and `error` to a short
reason (`where exited <n>`, `the upstream dashboard did not answer`, `where JSON has no tables`); what the read itself printed goes to the dashboard's output only, never to the
page. A read still running after a minute is marked failed (`the read timed out after 1m0s`) and the next read waits for it to end. `/api/sprint` also carries `attemptAt` and
`readSeconds`, the last read's end and length, good or not, and `minInterval`, the
seconds of `--every`. An svg `--logo` is drawn inline in the page's text colour and
served as `/favicon.svg`, coloured for the light or dark theme; any other image is
`/logo`, and the raster logo routes a page or bookmark may still name (`/favicon.png`,
`/logo-icon.png`, `/logo-tile-192.png`, `/logo-tile-384.png`, `/logo.webp`, `/logo.png`)
answer with the same file. The font's licence is `/OFL.txt`. Test:
`TestDashboardServesWhatServerPyServedFromOnePoller` (internal/sprintdash).

One freshness check: the served data's age is the time since its read (before any good
read, since the dashboard started). Older than 2 s for 30 s raises the alarm: one line
on the dashboard's output (`ALARM stale: ...`), once an episode; while it stands
`/healthz` answers 503 with why, on the page's listeners and the pull routes', and
`/api/sprint` carries `"stale": true`. The first fresh read clears it, with one line
(`FRESH again: ...`).

The public copy is the same verb as a puller: `nova-sprint dashboard --pull <url>`, the
`http://` or `https://` URL of another dashboard (its `/api/sprint`, or the base it is
under), reads that dashboard's copy once per `--every` in place of the sprint and serves
the page alone (no pull routes). It serves the copy as the upstream serves it (its data,
its read time and its throughput), so the two pages agree; an upstream holding a failed
read is a failed read here too, and the puller's freshness check is on the upstream's
read time, so a page that has stopped moving upstream raises the alarm on both.

The verb itself listens only inside the fleet's private network: `--listen
127.0.0.1:7390,<tailnet-address>:7390` serves this machine and the tailnet, and an
address every network reaches (0.0.0.0, ::) or any public address is refused, because
the page checks no credential. The page itself may be public: it carries no credential,
and what it shows of the sprint is fine for anyone to see. To publish it, put a reverse
proxy (Caddy, for example) on a machine of the owner's choosing in front of a loopback
listener; the proxy holds the public address and the verb never binds one. The sprint's
server, the verbs and the Television token stay inside the tailnet. The dashboard
exits 3 when its binary is replaced on disk, so its supervisor starts the new build
(docs/FLEET.md shows its loop row).

In Television the dashboard is a URL artifact on a channel, pointing at the page's
address (`http://127.0.0.1:7390/` on the machine that runs it); the Electron app shows a
URL artifact in a webview, a browser client a placeholder. The token that publishes to
Television is the owner's: it is never in this repository, never on the dashboard's
command line, and the dashboard never reads it.

## The specification

From here to the end is the owner's locked text.

Every change to the page is a change to this file first; the page conforms to the file; before every restart the
builder checks each line below against a screenshot at 1440 and 375 and fixes any drift before serving. A request
from the owner edits one line here and nothing else moves.

## Page
- Dark only: no theme toggle (the owner, 2:21 PM 2026-10-04: "just remove the toggle light/dark. always dark."). Panels full width, stacked: header, progress bar, Work, Fleet,
  Friends, Lanes, footer. No readers or merge panel (available at ?all=1 only). No two-column layout at any width.
- Base type 28 px (doubled). Labels and headers: system proportional face. All numbers: monospace (ui-monospace,
  Menlo), right-aligned. Headers over numeric columns right-aligned too.
- Refresh: the page keeps `/events` open and patches in place as each copy arrives; while the stream is not open it
  polls the server's cached copy every 1 s on a fixed timer (never after an answer); the server reads the sprint at
  most once a second. A failed read: hold the previous data, change nothing, say nothing on the page (log only).
- Caching: no-store on everything; versioned asset links; the page reloads itself when the build number changes.

## Header (one flex row, align-items center)
- Logo: the image file `--logo` names (the owner's: a robot on the blocks, the sky version), 96 px (120 px from 1200 px wide), the
  tile's own rounded corners; also the favicon. With no `--logo` the slot and the favicon render nothing.
- The word "nova-sprint" in Nunito 800 (lowercase), the page's primary white (never cream), cap height about two
  thirds of the tile, optically centered with the pills.
- Pills: coordinator <name>, epoch <n>, machine <state>; then the Updated clock with its live dot; no theme toggle (dark
  only, the owner, 2:21 PM 2026-10-04: "just remove the toggle light/dark. always dark.").
  The clock never flashes.

## Hero row: five tiles, one row at 2000 px, three and two below, two per row below 1100 px, large figure (72 px)
1. LANDED: n of all; sub-line "<pct>% complete". Narrow: the number alone, sub-line "of <all> · <pct>%".
2. ETA: "2h 9m"; sub-line "around 9:06 PM".
3. COST: total to the cent; sub-line "$0.24 per card" (never "per landed card").
4. IN FLIGHT: n; sub-line "14 working · 9 review" on one line.
5. THROUGHPUT: cards landed per hour over the last 60 min; "—" until ten minutes of samples; sub-line "cards / hour".
   A lone tile on its row spans the width with its figure centered.
- No FLEET tile. Flash on change: LANDED only; the others never.

## Progress bar: "ALL CARDS BY STATE" with the legend (landed, merging, fix, review, working, ready, waiting) and counts;
  one cell per card (per N cards when they would be under 4 px; no "1 cell = N" label); working cells pulse steadily
  (2 s); other cells still.

- Fix (the owner, 2026-10-07; his lines are quoted under LOCK 2): a card awaiting
  rework is one purple, `--s-fix` (#8b5cf6 on this dark page; #7c3aed on a light one), everywhere a state is
  drawn: the progress bar and its legend carry `fix <n>` between review and merging (in the bar's order,
  landed, merging, fix, review), a Work row's `fix` column, a fleet or friends row's track, and its mark. The
  view (dashboard.go) marks the copy it serves: a dealt card is at fix when its level is `fix` (the fix
  level, card a-rework-is-priority-fix-bb), or, until where prints that level, when it is an attempt after
  the first (its `attempt`, else its id's `.w<n>`) and not blocker or critical, which keep their red; such a
  card carries `fix: true`; a fleet or friends row's `fix` is its fix cards working (where's `fix_working`
  once it prints one); a Work row's `fix` is its primaries at fix, taken off its `working`, where a primary
  sent out again sits (review -> working on rework), unless where prints the row's `fix` itself; the copy's
  `fix` is the Work rows' summed; and `priorities.fix` lists those primaries, off high and low. A track's lit
  cells run in the ladder, highest on the left: blocker (`--p-blocker`), critical (`--p-critical`), fix
  (purple), reads (orange, one cell per two reads, a lone read a whole cell), then the working blue; the
  Total row says "<n> fix" under the tracks when any row has one. The In flight tile counts a card at fix as
  working. Owed from where: a primary in review awaiting the coordinator's rework, and one parked on a brief
  defect, carry no dealt card, so the view cannot see them until where prints the Work row's `fix`.
  `TestFixView*` and `TestFixPage*` (internal/sprintdash/view_fix_test.go) hold it.

## Work (title exactly "Work"; subtitle "<n> streams · <l> landed · <h> held")
- Columns: stream | status | waiting | ready | working | review | fix | merging | landed | cost (headers exactly so, all lowercase). The "landed" header is centred over its "n / total" cell (the owner, 7:34 PM: "Landed column in work stream table, please horizontal center align the column header"); every other numeric header stays right-aligned. The status column with its pills stays (the owner, after the lock, 7:32 PM: "we just lost the nice state tabs in the workstream table. undo pls."); the sort by status stays; a thin rule separates the groups (landed, working, stopped, held).
- Stream column capped (~220 px) and the Status column takes part in the even spread like the count columns, so the gap between the name and Status is as generous as the gap between any two count columns; names in full-strength text always (a label, never dimmed); the remaining width is
  spread evenly across the count columns (fixed table layout); Landed and Cost a little wider; gutters at least 40 px.
- Rows sorted by status like the fleet table: landed first, then working, then stopped, then held; within a group by stream name. A row moves when its status changes (no animation).
- Status pill in Work: landed (green), working (blue), held (amber), stopped (red); the subtitle keeps "<n> streams · <l> landed · <h> held". Zero counts muted grey.
- Landed as "n / total" with the slash on one vertical line (left number right-aligned to it, total left of it), the
  gap before Landed (after merging) equal to every other column gutter in the row, never tighter. No per-stream bar. Total row: numbers only.
- Flash: the count columns and Landed flash when their value differs from the previous second; Cost never.
- Cost counts from the last `nova-sprint stats tidy --streams` (docs/SPEC-SPRINT.md section 11, Statistics; the owner,
  2026-10-06: "I would like a semi-fresh start to stats now"): the cell the page reads is the tidied one, and the page
  computes nothing of its own. Not yet: "since <time>" beside it; `where --json` carries no stats_since yet
  (cmd/nova-sprint/reads.go, owed).

## Fleet (title "Fleet"; subtitle "<u> up · <h> held · <d> down")
- Columns: machine | status | ready | working | done | ok% | load (headers exactly so, all lowercase).
- Machine column capped, and the Status column takes part in the even spread like the numeric columns (as in Work
  streams); names never dimmed. Status pill: up (green), held (amber), down (red).
- Working: a cell track, one cell per slot of the machine's width (nothing drawn beyond its width), cells 1.5x their current
  width (~27 px wide, height unchanged) with a 4 px gap, so the Working column is about 1.5x as wide, lit by level for working (blocker, critical, fix purple, reads, then blue: the Fix line under the progress bar), dark fill for free, aligned on one grid down
  the column; then the "n / width" figure right after the track. Gaps: Ready to track and track to figure equal and
  wide (double the first attempt, ~64 px); every column gutter ~56 px.
- Cells: no steady pulse; a cell flashes once when it lights or unlights. No numeric column in Fleet ever flashes.
- OK% and Load: plain numbers, right-aligned, no bars, no dots. Total row: Ready and Done and OK% numbers, no cells. OK% is landed-or-ok over attempts a worker actually ran to an end, excluding launch refusals, withdrawn attempts, coordinator take-backs, and provider failures. A launch the member refused and a take the provider failed are counted in the fleet table's own `refused` and `provider` columns (a handed-back or taken-back attempt in `withdrawn`), never in ok% nor failed; the page does not render those three columns yet.
- Done and OK% (and the Friends table's) count from the last `nova-sprint stats tidy --fleet` (`--friends`): the tidy takes
  the history off the done cells the page's counts are read from. Not yet: "since <time>" beside OK%, as for Cost.

## Friends (title "Friends"): same eight-column shape as the live Fleet table, including load (friend, status, ready, working track and fraction, done, ok%, load; headers lowercase); honest empty
  state until the JSON carries tables.friends.

## Footer: one link, "https://github.com/mas-bandwidth/nova-sprint", matching the live page.

## Responsive (change what is shown, never squeeze; no horizontal scroll at any width; 16 px gutters on a phone)
- Below the breakpoint (where the full layout no longer fits): Fleet shows Machine | Status (dot only) | Working as
  "n / width" | OK%; no Ready, no cells, no Done, no Load. Work shows stream | landed (no status column); Friends like
  Fleet. Hero figures step down one size on a phone; the logo 64 px. Tested at 375, 430, 760, 1024, 1440, 2000.
7:19 PM

- Column headers in every table are lowercase (the owner, after trying capitals: "Lowercase all column title names pls for all tables").
- The stream state with cards in flight is called "working" wherever it is named (not "active"); the owner: "honestly, 'working' is better."

## LOCKED (the owner, 7:29 PM ET 2026-10-02: "OK please lock this in. Do not change anymore. We are done.")
This specification is locked. No line changes without his words, quoted here with the date, as TABLES.lock does for the terminal tables.
- 7:32 PM, the owner, a quoted change after the lock: the status pills in Work are restored ("undo pls").

## Column alignment across the three tables (the owner, 7:36 PM)
"Can we horizontally ALIGN the status columns across the three tables pls: work, fleet, friends" / "so they scan nicely as the eye goes top to bottom scrolling down." / "aligned on the right align (column right side)". The status column of Work, Fleet and Friends shares one right edge (one grid template or one fixed column width and offset for all three tables, so the pills line up as the page scrolls). Nothing else changes.

## LOCK 2 (the owner, 7:40 PM): "ok this is perfect. lock this in." The page as checked at this time (landed header centred, one status right edge across the three tables, the Fleet width column at 8rem) is the page. No change without a quoted line from him.
- 2026-10-06 ~5:30 PM ET, the owner (relayed by the seat), a quoted change after the lock: "The dashboard
  shows priority by colour on the card marks: a blocker card bright red, a critical card dark red, a reader
  card the orange of the robot's shoes ..., work cards blue whatever their priority." The Priority row under the progress bar.
- 2026-10-03 11:30 AM, the owner, a quoted change after the lock: "nova sprint website is not updating once
  per-second. something is chug." The page's refresh is the event stream, the timer's poll its fallback (the
  Refresh line above); nothing else moves.
- 2026-10-05 ~11:45 PM ET, the owner, a quoted change after the lock: "I would like you to remove all the
  already landed work streams." The Work panel shows only the live streams by default; the archived
  ones (`stream archive`, read from `where --json --archived`'s `archived`) are behind one line in the
  panel head after the subtitle, "<N> archived streams, <M> cards landed, $<X> · show", which shows
  them in the table (and reads "· hide") when clicked and hides them again on the next click. The
  subtitle counts the streams shown; the total row, the progress bar, the hero and its cost count only
  the streams on the table, an archived one shown or not (the owner, 2026-10-06 2:43 PM ET: "I really
  don't think we have 2.8k cards post-archive..."), and the archived line carries the archived ones;
  an archived row shown carries the tag "archived", so a reader sees why the rows do not add up to the
  total; the cost tile covers the same streams, a sprint done (`done`) every stream of
  the epoch as the hero's count does. Its subtitle shows only the amount per landed card;
  work/read breakdowns, unpriced counts and unreconciled spend are not shown in the tile;
  the throughput samples the epoch's landed cards (`landed + archived_landed`, `landed` alone when
  `done`), so an archive does not start it again and a finish does not spike it. Nothing
  else moves.
- 2026-10-07 5:58-6:01 PM ET, the owner, a quoted change after the lock: "I would like the cards that are
  awaiting rework to be purple. and the state of the cards to be purple in the total card segmented graph,
  and the segmented graph for fleet[/]friends." / "they should be shown to the left of read cards, and to the
  right of critical cards in ordering." / "And should show up as 'fix' state here", "between review and
  merging". The Fix line under the progress bar, the legend's `fix`, Work's `fix` column and the tracks'
  order. Nothing else moves.
- 2026-10-04, the owner, a quoted change after the lock, asking after the merge backlog:
  "Is this progress visible in the sprint dashboard yet?" The page shows one Merge row under the progress bar (the Merge section
  above), read from `where --json`'s `merge_row`. Nothing else moves.


## Live deployment contract (2026-10-08)

The owner uses one live dashboard. The packaged page is the source of that page, not an alternative dashboard. Preserve its responsive layout and common status edge when deploying. Both blocker and critical are the same bright red; fix is purple. Fleet and Friends tracks share a fixed span equivalent to sixteen original cells; the cell width shrinks as the largest configured width grows so their endpoints stay aligned. Friends retain the live load column.

The plain `where --json` response carries cached fix counts without requiring `--cards`: ready and working primaries with explicit fix priority, and review primaries whose work result failed, are counted under fix. Blocker and critical retain their priority; a successful repair awaiting an independent read stays in review. JSON Work counts partition these cards into fix rather than counting them twice; the text table schema is unchanged. The cache is computed with the tick snapshot, not a separate dashboard scan.



### Landings chart

The existing Landings panel remains the last panel, below all tables, at full width.
It shows stacked ten-minute buckets over the last twenty-four hours, fleet below
friends, using `--series-fleet` and `--series-friends`. Its header shows each series'
twenty-four-hour total and last-hour count. Count gridlines label the vertical axis;
the horizontal axis uses twelve-hour time labels every two hours. The panel folds
like the other panels and has no tooltips or title attributes.

The chart reads `/landings.json`, redraws when its `generated` value changes, and
stays hidden while that file cannot be fetched. Its historical series is refreshed
every sixty seconds; this does not change the one-second live snapshot requirement.

## Freshness: once per second, end to end (hard requirement)

The live dashboard uses a one-second tick end to end, including the public puller.
The page receives each snapshot through server-sent events; its polling fallback
runs every 1000 ms. Viewer count does not multiply upstream reads.
The dashboard keeps the last successful snapshot and
measures its age from the read time. A snapshot older than two seconds for thirty seconds
raises one stale alarm; the first fresh snapshot clears it. `/healthz` reports stale state,
`/api/sprint` carries it, and the page does not expose the read's error text.

### Requested removals (2026-10-08)

The live page has no priority-square strip under All cards by state, Merge panel,
Lanes panel, archived-stream summary or show/hide control, or tiers suffix on Fleet.
Work keeps its existing stream, landed and held counts. Archived streams stay excluded.
These removals supersede earlier display descriptions. No new dashboard content is added
without an explicit request.
