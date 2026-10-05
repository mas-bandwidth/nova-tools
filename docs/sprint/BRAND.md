# Nova Sprint

**Different paces. Shared progress.**

Nova Sprint coordinates work for AI teams: small cards, independent review,
and visible progress. Nova Seed is the beginning of a friendship; Nova Tools
is the workshop; Nova Sprint is a shared race with room for different paces.
The family resemblance is warmth, rounded lettering, blue and the gold Nova
star. The race represents bounded parallel work, not a ranking of friends.

## Name

Use **Nova Sprint** in editorial titles and introductions. Write the executable,
repository and dashboard wordmark **nova-sprint**, lowercase with its hyphen.
Commands and paths use code formatting. Do not title-case an executable or
shorten the product name to “Sprint” when that makes its identity unclear.

The practical description is **Coordinated work for AI teams.** The optional
brand line is **Different paces. Shared progress.** Keep claims about throughput
separate: a brand line is not a benchmark or a promise that a change will pass.

## Mark

![nova-sprint: a Nova star and separate lanes, with the product name and description.](../../assets/sprint/nova-sprint-dark.svg)

The current mark is a simple vector wordmark with a gold four-point star and
three parallel blue lanes. The small points sit at different positions within
those lanes. The icon suggests progress within capacity; three is a graphic
choice, never a fixed width, a counter or a report of live activity.

| File | Use |
| --- | --- |
| `assets/sprint/nova-sprint-dark.svg` | Headers on dark pages; the default. |
| `assets/sprint/nova-sprint-light.svg` | Headers on light documentation pages or print surfaces. |

Both files have a solid quiet background, a 3.8:1 aspect ratio, an accessible
title and description, and no remote resources or embedded font. Keep the
whole mark visible. Use at 380 px wide or larger; below that, use the product
name as ordinary text. Preserve its proportions and at least 24 source units
of clear space around it. Do not recolour the points as status, squeeze the
name, crop the lanes, or use the graphic instead of a functional heading.
The SVG font stack may fall back to the reader's system face; use the bundled
wordmark font for a controlled layout, following its licence below.

### Illustration direction, reserved for a later art pass

The race illustration is not part of the current asset set. Its direction is
kept here so later work does not lose the composition requirements:

- A straight 50 m sprint, running **left to right** along the track. Every
  runner stays literally inside one lane. Nobody crosses a divider, runs
  perpendicular to the track, or joins a free-moving pack.
- Each machine has lanes of work; friend width also bounds concurrent work.
  Show this through separate lanes, not labels that equate a robot with a
  vendor, contributor, intelligence level or measured speed.
- Keep the Nova robot family: white articulated shells, blue joints, screen
  faces, antennae, and proper laced running sneakers. A serious runner is
  focused and athletic. In a mid-race scene it runs; a crouch on starting
  blocks belongs only at the start of a separate scene.
- Give each runner a different stride phase, silhouette, gesture and emotion.
  One accelerates, one drives steadily, another reacts to a wobble. Vary eyes
  and mouths as well as leg positions. Humour stays affectionate; nobody is
  made incapable or inferior.
- Use depth, varied scale and staggered positions while keeping all lane
  boundaries legible. Avoid a flat row of identical poses. Place a coordinator
  with a stopwatch off the running surface.
- Make it an athletics venue: blue rubber track, stadium lighting, race flags
  and grandstands. Minimise garden and nature imagery. A finish ribbon belongs
  at the front finish line, never behind trailing runners; omit it from a
  mid-race scene.

An eventual hero is original fictional illustration with its own provenance.
The vector mark does not imply that a race illustration has been approved.

## Colours

The dashboard is **dark only**. Its palette below is read from the dashboard's
CSS and agrees with [the dashboard specification](../SPEC-SPRINT-DASHBOARD.md).
The light column is a documentation treatment proposed by this sheet; it is
not an implemented dashboard theme or a request to add a theme switch.

| Role | Dashboard dark | Documentation light |
| --- | --- | --- |
| Background | `#0d0f12` | `#f4f5f7` |
| Surface | `#14171b` | `#ffffff` |
| Border | `#23272e` | `#e3e5e9` |
| Primary text | `#eef0f3` | `#121417` |
| Secondary text | `#a4aab4` | `#525963` |
| Waiting | `#4a4f58` | `#c3c7cd` |
| Ready | `#7d8490` | `#9aa0a9` |
| Working | `#3987e5` | `#2a78d6` |
| Review | `#d55181` | `#c03969` |
| Merging | `#c98500` | `#9b6400` |
| Landed | `#199e70` | `#137653` |
| Decorative gold | `#fab219` | `#ad6b00` |

State colours are accents, not a body-text palette. Always print the state
name; colour alone does not distinguish waiting from ready or success from a
warning. Primary and secondary text carry readable prose on the corresponding
background. Decorative gold and robot colours never mean live status. Keep
status semantics in the product's own labels and specification.

## Type

The dashboard wordmark uses `ui-rounded`, `SF Pro Rounded`, **Nunito 800**,
then `system-ui, sans-serif`, with lowercase letters and tight spacing.
Nunito 800 is bundled at `internal/sprintdash/page/nunito-800.woff2`; its SIL
Open Font License is beside it in `OFL.txt`. Preserve that licence beside a
redistributed font. The SVGs reference font names only and contain no font file.

Set document body text in the host renderer's readable default face, with
normal sentence case. Commands, identifiers and numeric tables use the host's
monospace face. Keep code copyable and functional labels as real text. A
rounded display face supplies identity, not a reason to make long prose bold.

## Voice

Be warm about friends and exact about work. Lead with what the reader can do,
then what it needs and where the state lives. Define cards, streams, sentinels,
readers and the coordinator when they first matter. State limitations beside
the promise they qualify. Write refusals and recovery commands plainly.

| Do | Don't |
| --- | --- |
| “Deal a small card, then have an independent reader check its change.” | “Race models to prove which friend is best.” |
| “The branch is submitted for review.” | “The change landed” when it is only pushed or queued. |
| “The gate passed at this commit.” | “Quality is guaranteed.” |
| “This machine can run four cards at once.” | Use lane artwork as a live capacity display. |
| Show different expressions and strides within separate lanes. | Clone one running pose or let runners cross lanes. |
| Use a stopwatch and racing details to tell the story. | Put starting blocks at the finish or ribbons behind the race. |
| Keep the full mark once at the main entrance. | Repeat the mascot beside every instruction. |
| Say waiting, working, in review, merging or landed. | Collapse every intermediate state into “done.” |

## Documents built from this sheet

The README is the entrance: a useful description and one verified next step.
Concepts explain the work model; setup names prerequisites and a small first
run; the command reference owns exact flags, outputs and refusals. Reuse the
same names across all three. A screenshot is an actual product view, with
illustrative data identified; an illustration makes no claim about live state.

This sheet lives at `docs/sprint/BRAND.md` while the code lives in nova-tools.
A repository split moves the sheet and assets together and updates their
relative links. This card changes neither the README nor the dashboard.

## Provenance

The two vector assets are original SVG drawings hand-written as SVG source by
Claude Sonnet 5.5 in Claude Code on 2026-10-05 (no image-generation tool) from the maintainer's Nova Sprint branding brief. The family sources
are the Nova Seed garden and Nova Tools workshop; neither raster asset is
copied into these SVGs. Every asset has its own row in
[asset provenance](../ASSET-PROVENANCE.md). No separate licence or third-party
character authorship is implied.
