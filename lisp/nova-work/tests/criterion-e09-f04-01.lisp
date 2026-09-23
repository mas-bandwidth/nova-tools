;;;; criterion-e09-f04-01.lisp --- E09-F04-01: Separate absorb from default link and require selected scope/authority
;;;;
;;;; A deftest named for E09-F04-01 in lisp/nova-work/tests/criterion-e09-f04-01.lisp
;;;; proves "Separate absorb from default link and require selected scope/authority"

(in-package #:nova-work/tests)

(deftest "e09-f04-01-separate-absorb-from-default-link"
    "Separate absorb from default link and require selected scope/authority"
    "docs/SPEC-WORK.md:7586-7617"
    "expected=absorb-disabled-by-default;link-is-default;absorb-requires-explicit-scope-and-authority"
  ;; The v1 pilot rule: absorb is disabled and link is the default (SPEC-WORK.md:7614)
  
  ;; Test 1: By default, absorb should NOT be allowed (link is the default)
  (let ((stage (make-capture-stage)))
    (ok (not (nova-work::capture-absorb-allowed-p stage)) 
        "absorb should be disabled by default"))
  
  ;; Test 2: Explicitly enabling absorb should work
  (let ((stage-with-absorb (make-capture-stage :absorb-allowed t)))
    (ok (nova-work::capture-absorb-allowed-p stage-with-absorb)
        "absorb should be allowed when explicitly enabled with scope/authority"))
  
  ;; Test 3: The default mode is link (absorb-allowed is nil by default)
  (let ((default-stage (make-capture-stage)))
    (check-equal nil (nova-work::capture-stage-absorb-allowed default-stage)
                 "default stage should have absorb-allowed = nil (link mode)")))
