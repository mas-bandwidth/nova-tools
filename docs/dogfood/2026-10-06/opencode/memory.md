# nova-memory dogfood report

**Date:** 2026-10-06  
**Model:** inception/mercury-2.5  
**Harness:** opencode

## Summary

nova-memory is a clean, well-designed tool for indexing and searching markdown notes. It successfully passes all the onboarding standard checks - help is comprehensive, examples run as documented, and refusals carry clear remedies.

## Findings

1. **Command:** `nova-memory search --root scratch/corpus` (missing --channels)
   **Output (first 3 lines):**
   ```
   SEARCH REFUSED: --channels is required; refusing to guess; run: nova-memory help
     --channels names a retrieval method, not a directory; the channels are bm25 and trigram, and bm25 alone is the usual start
   ```
   **Expected:** Proper refusal with channel names explained
   **Grade:** URGENT - the hint text correctly says the channels ARE bm25 and trigram, but the user might wonder what other values might be valid.

2. **Command:** `nova-memory check --root scratch/corpus` (missing --channels and --k)
   **Output (first 3 lines):**
   ```
   MEMORY REFUSED: --channels is required; refusing to guess; run: nova-memory help
     --channels names a retrieval method, not a directory; the channels are bm25 and trigram, and bm25 alone is the usual start
   ```
   **Expected:** --k also missing but only first refusal shown
   **Grade:** URGENT - the tool says "every missing flag is reported" but only reports one at a time for check.

3. **Command:** `nova-memory verify --root scratch/corpus` (missing --links)
   **Output (first 3 lines):**
   ```
   VERIFY REFUSED: --links is required; refusing to guess; run: nova-memory help
   ```
   **Expected:** Hint about what --links wants (gate vs info)
   **Grade:** NEXT - the hintFor function doesn't include a hint for --links, though help entry does explain it.

4. **Command:** `nova-memory eval --root scratch/corpus --channels bm25 --k 3 --floor 0.5`
   **Output (first 3 lines):**
   ```
   EVAL OK recall@3=1.000 floor=0.500 rows=2 hits=2 misses=0 shown=0 mrr=0.500 channels=bm25
   ```
   **Expected:** Eval harness should report per-row results in --json mode too
   **Grade:** NEXT - eval output doesn't include a breakdown by query, only aggregate stats.

5. **Command:** `nova-memory boot --root scratch/corpus --pin pin.txt` (file in pin doesn't exist)
   **Output (first 3 lines):**
   ```
   BOOT OK files=2 bytes=110
   ```
   **Expected:** Should fail if pin file references non-existent file
   **Grade:** URGENT - boot only checks files that exist, doesn't validate pin references.

## Ratings

READ 9/10 - The help is excellent, all verbs are documented with effects, and the onboarding example block actually runs. The only friction is that some hints don't list all valid options.

USE 9/10 - The tool is well-designed with no-guessing enforcement, proper refusal grammar, and consistent output structure. The only friction is eval's lack of per-row breakdown and check's single-refusal-at-a-time.

## Final Count

urgent=3 next=2
