;;;; criterion-e09-f04-01.lisp --- E09-F04-01: Separate absorb from default link and require selected scope/authority
;;;;
;;;; A deftest named for E09-F04-01 in lisp/nova-work/tests/criterion-e09-f04-01.lisp
;;;; proves "Separate absorb from default link and require selected scope/authority"

(in-package #:nova-work/tests)

(defun %absorb-refused-p (&rest args)
  "True when MAKE-CAPTURE-STAGE refuses ARGS with an error."
  (handler-case (progn (apply #'make-capture-stage args) nil)
    (error () t)))

(deftest "e09-f04-01-separate-absorb-from-default-link"
    "Separate absorb from default link and require selected scope/authority"
    "docs/SPEC-WORK.md:7586-7617"
    "expected=absorb-disabled-by-default;link-is-default;absorb-requires-explicit-scope-and-authority"
  ;; The v1 pilot rule: absorb is disabled and link is the default (SPEC-WORK.md:7614).
  (let ((stage (make-capture-stage)))
    (ok (not (capture-absorb-allowed-p stage)) "absorb should be disabled by default")
    (check-equal :link (capture-stage-intake-mode stage) "default intake mode")
    (check-equal nil (capture-stage-absorb-allowed stage) "default absorb-allowed"))
  ;; Absorb with its explicit mode, scope and authority is allowed and recorded.
  (let ((stage (make-capture-stage :absorb-allowed t :intake-mode :absorb
                                   :absorb-repositories '("mas-bandwidth/nova-tools")
                                   :absorb-authors '("glenn" "stella")
                                   :absorb-authority "grant:glenn/2026-09-23")))
    (ok (capture-absorb-allowed-p stage) "absorb allowed with mode, scope and authority")
    (check-equal '("mas-bandwidth/nova-tools") (capture-stage-absorb-repositories stage)
                 "selected repositories")
    (check-equal '("glenn" "stella") (capture-stage-absorb-authors stage) "selected authors")
    (check-string= "grant:glenn/2026-09-23" (capture-stage-absorb-authority stage)
                   "recorded authority"))
  ;; A bare :absorb-allowed t, with no mode, scope or authority, is refused.
  (ok (%absorb-refused-p :absorb-allowed t) "bare absorb-allowed refused")
  ;; Each missing selection is refused on its own.
  (ok (%absorb-refused-p :absorb-allowed t
                         :absorb-repositories '("r") :absorb-authors '("a")
                         :absorb-authority "g")
      "absorb without explicit :absorb intake mode refused")
  (ok (%absorb-refused-p :absorb-allowed t :intake-mode :absorb
                         :absorb-authors '("a") :absorb-authority "g")
      "absorb without repositories refused")
  (ok (%absorb-refused-p :absorb-allowed t :intake-mode :absorb
                         :absorb-repositories '("r") :absorb-authority "g")
      "absorb without authors refused (author identity alone does not select)")
  (ok (%absorb-refused-p :absorb-allowed t :intake-mode :absorb
                         :absorb-repositories '("r") :absorb-authors '("a"))
      "absorb without authority refused")
  (ok (%absorb-refused-p :absorb-allowed t :intake-mode :absorb
                         :absorb-repositories '("r") :absorb-authors '("a")
                         :absorb-authority "")
      "absorb with empty authority refused")
  ;; The absorb mode is not reachable without absorb-allowed, and modes are closed.
  (ok (%absorb-refused-p :intake-mode :absorb
                         :absorb-repositories '("r") :absorb-authors '("a")
                         :absorb-authority "g")
      ":absorb intake mode without absorb-allowed refused")
  (ok (%absorb-refused-p :intake-mode :merge) "unknown intake mode refused")
  (ok (%absorb-refused-p :absorb-repositories '("r"))
      "absorb scope on a link stage refused"))
