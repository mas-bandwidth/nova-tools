# The Studio's live dashboard (live/): its local lines

The page's specification is nova-sprint's docs/SPEC-SPRINT-DASHBOARD.md (locked there). This
file holds the lines of this local copy (live/, served by ../server.py on 127.0.0.1:7390)
that the canonical spec does not carry yet; each moves into it with the card that does the
same in nova-sprint.

- Landings chart (Glenn 2026-10-04 4:20 PM ET: "Can I get a cool graph showing cards landed for 'friends' and 'fleet' over time, like # of cards landed per-10 minutes as the sample." / "Put this graph underneath all tables" / "full width."): one panel titled "Landings", the last panel on the page, below every table, full width; stacked bars, one per 10-minute bucket over the last 24 hours, fleet at the base and friends on top, colours --series-fleet and --series-friends from :root; header legend: a swatch and total per series over the 24 hours, then the last hour's counts; y gridlines with counts, x labels every 2 hours in 12-hour time; no tooltips, no title attributes; dark; folds like the other panels; read from /landings.json (written every 60 s by ../bin/landings.sh, a stopgap) and redrawn when its "generated" changes; the panel stays hidden while that file cannot be fetched.

## Freshness: once per second, end to end (hard requirement)
Glenn, 2026-10-04 ~6:03 PM ET: "once per-second updates are a hard requirement." / "lock that in."
- The public page (served from space) shows data at most 2 s old: the Studio's poller makes a fresh snapshot every second, space's puller fetches once per second, Caddy serves the JSON with max-age=1, and the page polls every 1000 ms.
- It holds under any viewer count: the Studio sees one request per second from space, never one per viewer.
- A freshness check measures the served snapshot's age, and an age over 2 s for 30 s is an alarm to the coordinator.
- The machine pill says RUNNING, STALE or STOPPED and nothing more; a stop's reason is the coordinator's view only (Glenn 2026-10-04 ~10:15 PM: "STOPPED is plenty").
- The cost tile shows the recorded total and, under it, the cost per landed card (Glenn 2026-10-04 ~10:50 PM: "Please bring that back"). No other text is added to the page unless Glenn asks for it.
- A status pill shows the status word only (up, held, down); a reason the server carries after it is the coordinator's view (Glenn 2026-10-04 ~10:40 PM).
- The Work name column fits the longest stream name with its tag (cap 27.75rem); the shared status edge --E is clamp(18.5rem, 45vw, 42rem), so the pills of Work, Fleet and Friends still end on one line (Glenn 2026-10-04 ~11:58 PM).
- Fleet and Friends rows (Glenn 2026-10-06 8:42-8:46 PM ET: "the segmented bar when fleet or friends work on them", then the final rule, "so highest priority on the left"): the row's segments run in the priority ladder, highest on the left: blocker, critical, reads, then the blue work cards (high, normal, low) on the right (9:09 PM: "orange is ON THE LEFT, not right"). Blocker is --s-blocker (#ff1a1a), critical --s-critical (#9b1c1c), a read card --s-read (#fb8321, light #f0892e), high, normal and low the working blue; counts from the row's <level>_working fields in where --json; a read is half a slot, so the row draws one orange cell per two reads, a lone read filling a whole cell until its pair arrives (9:11 PM: "Each segment is 1 work, or 2 reads"), and the working cell reads "n / width" only (9:08 PM: no reads count; "I'll just use the number of orange cells to estimate it").

- Fleet and Friends panels (Glenn 2026-10-06 ~8:02 PM ET: "We should be able to enable/disable fleet, enable/disable friends. default enabled", "When [disabled], the table greys out a bit visually in the dash", "I want to see this both chevron'd and open"): the panel head says "· off" when the side is off (where --json fleet_work / friends_work) and "· tiers <list>" when its tiers are limited (fleet_tiers / friends_tiers, "all" otherwise); an off side's whole panel is dimmed to 45% opacity, open or collapsed.

- Fleet and Friends working fraction (Glenn 2026-10-06 ~9:25 PM ET: "you only need to reserve the maximum value across each row's digits because the sum is below and no segmented bars are there"): each row's "n / width" numerator is as wide as the widest numerator in the table, the Total's included, so every slash sits on one line (9:28 PM: "the totals are slightly misaligned"); only the Total's denominator may be wider.

- Fix cards are purple (Glenn 2026-10-07 5:58-6:00 PM ET: "I would like the cards that are awaiting
  rework to be purple. and the state of the cards to be purple in the total card segmented graph,
  and the segmented graph for fleet/friends." and "they should be shown to the left of read cards,
  and to the right of critical cards in ordering."): a card at priority `fix` (a rework: a failed or
  broken attempt sent out again, or a card re-briefed out of the defect column; until the fix level
  lands, a card on its second or later attempt at high) is one purple colour (`--s-fix`, dark
  `#8b5cf6`, light `#7c3aed`) everywhere a state is drawn: the card list, the total cards segmented
  bar, and each fleet and friends row's segmented bar. The cell order in every segmented bar, left
  to right: blocker (red), critical (dark red), fix (purple), reads (orange), working (blue), then
  the rest as before. The counts under the bars gain a `fix` figure beside the others.
  And a `fix` STATE in ALL CARDS BY STATE and in the Work table (Glenn 2026-10-07 ~6:01 PM ET: "And
  should show up as 'fix' state here", "between review and merging"): the legend and the bar carry
  `fix <n>` between `review` and `merging`, purple; the Work table gains a `fix` column between
  review and merging, counting the stream's cards awaiting rework (a failed or broken attempt sent
  out again, or parked on a brief defect) so review counts only cards waiting on or under a read.
- Critical and blocker are one bright red (Glenn 2026-10-07 ~6:26 PM ET: "please change critical
  and blocker to have the same bright red color. The difference is that a blocker can evict an
  already working slot, while a critical does not."): `--s-critical` takes `--s-blocker`'s value
  (dark `#ff1a1a`, light `#ff1a1a`); both cells read the same red in every segmented bar and card
  list; the legend keeps both words. The order stays blocker, critical, fix (purple), reads
  (orange), working (blue).
