# Nova Tools terminology: the rules of naming

Welcome — this page is the rules for the words the specs and help use. The words themselves,
each with one line of definition, the spec section that defines it and what it replaced, are in
two glossaries, one per release:

- [GLOSSARY.md](GLOSSARY.md): nova-tools 1.2.0, the building blocks.
- [sprint/GLOSSARY.md](sprint/GLOSSARY.md): nova-sprint 1.0.0, the opinionated system built
  from them (it moves to nova-sprint's `docs/` at the split, [SPLIT-NOVA-SPRINT.md](SPLIT-NOVA-SPRINT.md)).

## The rules

1. **One word for one thing.** A reader who meets two words for one thing stops trusting both.
   The word the spec defines is the word help, comments and tests use.
2. **A term is defined where it is born.** Its definition lives in the spec section that
   introduces it; a glossary row names that section and never restates the rule.
3. **A glossary row is one line**: what the term means, the section that defines it, and the
   words it replaced if any. A spec or help word missing from the glossaries is a gap; file it.
4. **A renamed word is retired, not aliased for ever.** The old word is listed in
   [internal/docs/testdata/retired-words.txt](../internal/docs/testdata/retired-words.txt) with
   what replaced it, and the glossary row says *Replaces:*. A verb or flag may keep an old
   spelling for one release, and its row says so.
5. **Dated records keep the words of their day**: CHANGELOG.md, RESOLUTIONS.md,
   `docs/RELEASE-NOTES-*`, `docs/ratings/` and the ledgers under `testdata/`. They are never
   rewritten to the new word.

## Retired words

| retired | replaced by | since |
|---|---|---|
| asleep | down | 2026-10-03: "Please change 'asleep' to 'down' so we have consistency across all tables" |
| untiered | no tier | the sprint's cost views |
| git bus | nova-bus | 2026-10-04, when the git bus was removed |

`TestRetiredWordsAppearOnlyInRecords` (internal/docs/terminology_lint_test.go) fails when a
tracked file outside the dated records, the testdata ledgers and these three terminology
pages uses one. Places that still carry a retired word are rows of a shrink-only allowlist in
retired-words.txt, each a place still to be fixed; a new row is a refusal, and a count that
falls fails until its row is lowered.
