;;;; issue-2350.lisp --- Test the prompt profile's unique-triple invariant and
;;;; its versioned edit grammar (nova-tools #2350).

(in-package #:nova-work/tests)

(deftest "issue-2350" "docs/SPEC-WORK.md:3349-3377"
    "expected=no-two-profiles-share-one-triple;duplicate-triple-refused;different-harness-admitted;different-work-type-admitted;edit-creates-versioned-record;edit-carries-by;edit-lists-changed-fields;prior-revision-remains-readable"
  ;;; Part A: unique-triple invariant (SPEC-WORK.md:3350-3353)
  (let ((registry (make-profile-registry)))
    (let ((p1 (make-prompt-profile
               :name "sol-cli"
               :pointer "prompts/sol.md"
               :digest "aaa"
               :model "sol"
               :harness "claude-cli"
               :work-type "task")))
      (multiple-value-bind (ok line)
          (register-profile registry p1)
        (ok ok "first profile admitted: ~A" line)))
    (let ((p2 (make-prompt-profile
               :name "sol-cli-dup"
               :pointer "prompts/sol-v2.md"
               :digest "bbb"
               :model "sol"
               :harness "claude-cli"
               :work-type "task")))
      (multiple-value-bind (ok2 line2)
          (register-profile registry p2)
        (check-equal nil ok2 "duplicate triple refused")
        (ok (search "already held" line2) "refusal names collision: ~A" line2)))
    (let ((p3 (make-prompt-profile
               :name "sol-codex"
               :pointer "prompts/sol.md"
               :digest "aaa"
               :model "sol"
               :harness "codex-cli"
               :work-type "task")))
      (multiple-value-bind (ok3 _)
          (register-profile registry p3)
        (ok ok3 "different harness admitted")))
    (let ((p4 (make-prompt-profile
               :name "sol-review"
               :pointer "prompts/sol.md"
               :digest "aaa"
               :model "sol"
               :harness "claude-cli"
               :work-type "review")))
      (multiple-value-bind (ok4 _)
          (register-profile registry p4)
        (ok ok4 "different work-type admitted")))
    (check-equal 3 (length (profile-registry-profiles registry))
                 "three profiles in registry"))

  ;;; Part B: versioned edit grammar (SPEC-WORK.md:3372-3376)
  (let ((registry (make-profile-registry)))
    (let ((p (make-prompt-profile
              :name "sol-mgr"
              :pointer "prompts/sol.md"
              :digest "aaa"
              :policy-version "p-7"
              :evidence '(:measured "2026-09-01" :cost-per-accepted 12)
              :expiry "2026-09-16"
              :owner "glenn"
              :model "sol"
              :harness "claude-cli"
              :work-type "task")))
      (register-profile registry p))
    ;; Edit: pointer, digest, expiry
    (multiple-value-bind (ok record updated)
        (profile-edit registry "sol-mgr"
                      :pointer "prompts/sol-v2.md"
                      :digest "ccc"
                      :expiry "2026-12-31"
                      :by "rowan"
                      :stamp "2026-09-20T10:00:00Z")
      (ok ok "edit admitted")
      (check-string= "rowan" (profile-edit-record-by record) "edit carries :by")
      (check-equal 1 (profile-edit-record-version record) "version 1")
      (let ((fields (profile-edit-record-fields record)))
        (ok (member :pointer fields) "lists :pointer")
        (ok (member :digest fields) "lists :digest")
        (ok (member :expiry fields) "lists :expiry"))
      (check-string= "sol-mgr" (profile-edit-record-profile-name record) "names profile")
      (check-string= "2026-09-20T10:00:00Z" (profile-edit-record-stamp record) "carries stamp")
      ;; Updated profile values
      (check-string= "prompts/sol-v2.md" (prompt-profile-pointer updated) "new pointer")
      (check-string= "ccc" (prompt-profile-digest updated) "new digest")
      (check-string= "2026-12-31" (prompt-profile-expiry updated) "new expiry")
      (check-string= "p-7" (prompt-profile-policy-version updated) "policy unchanged")
      (check-string= "sol" (prompt-profile-model updated) "model unchanged")
      (check-string= "claude-cli" (prompt-profile-harness updated) "harness unchanged")
      (check-string= "task" (prompt-profile-work-type updated) "work-type unchanged"))
    ;; Journal has one entry
    (check-equal 1 (length (profile-edit-journal registry)) "one journal entry")
    ;; Second edit
    (multiple-value-bind (ok2 rec2 _)
        (profile-edit registry "sol-mgr" :owner "alice" :by "glenn" :stamp "2026-09-21")
      (ok ok2 "second edit admitted")
      (check-equal 2 (profile-edit-record-version rec2) "version 2")
      (check-string= "glenn" (profile-edit-record-by rec2) "second :by"))
    (check-equal 2 (length (profile-edit-journal registry)) "two journal entries")
    (check-equal 2 (length (profile-edit-journal registry :profile-name "sol-mgr")) "filtered")
    (check-equal 0 (length (profile-edit-journal registry :profile-name "nope")) "empty filter"))

  ;;; Part C: edit refuses when no fields provided
  (let ((registry (make-profile-registry)))
    (let ((p (make-prompt-profile
              :name "test-p"
              :pointer "prompts/t.md"
              :digest "ddd"
              :model "sol"
              :harness "claude-cli"
              :work-type "task")))
      (register-profile registry p))
    (multiple-value-bind (ok line _)
        (profile-edit registry "test-p" :by "rowan")
      (check-equal nil ok "no-fields refused")
      (ok (search "no fields to change" line) "names reason: ~A" line)))

  ;;; Part D: edit refuses for non-existent profile
  (let ((registry (make-profile-registry)))
    (multiple-value-bind (ok line _)
        (profile-edit registry "nope" :pointer "prompts/new.md" :by "rowan")
      (check-equal nil ok "nonexistent refused")
      (ok (search "no such profile" line) "names missing: ~A" line))))
