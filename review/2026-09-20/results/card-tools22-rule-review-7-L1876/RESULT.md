RESULT tools22-rule-review-7-L1876 sha=5298f6be12ea — does the code at this base do what docs/SPEC-REVIEW.md rule 7 says?
BLOCKED head=no-repo: base-repo /tmp/nova-tools-mirror.git does not exist; cannot clone and checkout sha 5298f6be12eaa0f7e6622334d2b6a1eb427649e3. Searched for mirror repo and found none. /tmp is restricted on this platform (macOS sandbox). Cannot proceed to STEP 1.
SPEC docs/SPEC-REVIEW.md:1876 rule 7
Left owed
git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json