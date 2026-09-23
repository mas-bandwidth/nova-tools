# A local trial of the who-reads question, 2026-09-19

**This file is an example and a record of one house's trial. It is not part of the contract, it
binds nothing, and no number in it tunes a floor.** The normative pair is
`../questions-reader.json` and `../criteria-reader.md`.

## The local role binding used in the trial

| role | the reader it was bound to here |
| --- | --- |
| `security-designate` | Johnny |
| `design-authority` | Stella |
| `lane-owner` | Emma |
| `child-review` | an Opus child session |
| `second-child-review` | a Fable child session |
| `coordinator` | Rowan |
| `human` | Glenn |

A model answer never creates or changes this binding. It is configured locally and read by the
machinery after the answer comes back.

## What was observed

47 answers from six shift lanes on 2026-09-19, asked with the PREVIOUS version of the question
(one hand-kept copy per lane, prose state, no typed fields, no criteria carried into the request).
They were joined afterwards to what the friends did on the message bus. Nine of those pull
requests were later HELD: #1815 and #1871 by Johnny, #1776, #1785, #1830, #1833, #1856 and #1860
by Stella, #1832 by Emma.

**A later HOLD is an observation and not adjudicated truth.** A pull request nobody held is not a
pull request that needed no reader — nobody looked. "The default would have missed it" is a
counterfactual nothing here tested. These rows therefore carry no truth label, establish no
precision and set no floor; they are kept because the two answers below show what a missing FIELD
does, which is the thing this version changes.

* **#1856** — the card named FOUR design defaults, in its state's prose. The answer was
  `opus-child` at 0.70. The criterion for the design read already existed; it named a fact the
  state did not carry. Stella later held it.
* **#1832** — the answer was `stella` at 0.55, below the floor the lane ran, so the lane's own
  fallback took it. Emma later held it, on a four-character defect that 371 green tests and two
  passing controls did not reach.

Asked again on 2026-09-19 with the criteria carried into the request and the facts as fields, the
same two states answered `stella` at 1.00 and `stella` at 0.81, and two control states — a
mechanical documentation card and a security-shaped one — answered `opus-child` at 0.91 and
`johnny` at 1.00, unchanged. **Four observations, one provider, one day.** They are a reason to
carry the fields, not evidence that the reading is calibrated.
