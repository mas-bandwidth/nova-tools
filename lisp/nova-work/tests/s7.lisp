;;;; s7.lisp --- slice 7 acceptance tests: the roadmap regenerates, and no
;;;; percentage is typed by hand.
;;;;
;;;; One deftest per named replay the slice implements, each naming its line of
;;;; docs/SPEC-WORK.md and asserting the sentence the spec states. The slice is
;;;; implemented as pure functions and records in src/s7-*.lisp; these tests call
;;;; those functions directly, because the connection to the command thread is a
;;;; later wiring card.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; Fixture helpers.
;;; ------------------------------------------------------------------

(defun s7-create-request (&key (node "acme/board") (title "Board")
                               (members '("acme/work/f1" "acme/work/f2"))
                               (permitted-roots '("docs"))
                               (request "s7-create"))
  (list :verb :s7-roadmap-create :node node :title title :under '(:node "acme/work")
        :row-kind :feature :aggregation :required-members
        :completion-policy :all-required-features
        :axes '() :members members :permitted-roots permitted-roots
        :request request))

(defun s7-projection-add-request (&key (projection "p1") (root "docs") (repo "acme/nova-tools")
                                       (path "ROADMAP.md") (start "<!-- ROADMAP:START -->")
                                       (end "<!-- ROADMAP:END -->") (request "s7-proj"))
  (list :verb :s7-roadmap-projection-add :projection projection :root root :repo repo
        :path path :start start :end end :policy :markdown-table
        :request request))

(defun s7-created (&key (members '("acme/work/f1" "acme/work/f2"))
                        (permitted-roots '("docs")))
  (nth-value 2 (s7-roadmap-create (s7-create-request :members members
                                                  :permitted-roots permitted-roots))))

(defun s7-with-projection ()
  (nth-value 2 (s7-roadmap-projection-add (s7-created) (s7-projection-add-request))))

(defun fixture-table ()
  "The marker region a file projection writes between. Every other byte is
  bystander text the file mode must preserve exactly."
  (format nil "top matter~%<!-- ROADMAP:START -->~%old table body~%<!-- ROADMAP:END -->~%bottom matter~%"))

;;; ------------------------------------------------------------------
;;; 1. chat-and-file-render-are-byte-identical   SPEC-WORK.md:5635
;;; ------------------------------------------------------------------

(deftest "chat-and-file-render-are-byte-identical" "docs/SPEC-WORK.md:5635"
    "expected=chat-bytes==marker-region-bytes;every-other-byte-preserved;missing/dup/reversed-marker-refused"
  (let ((view (s7-with-projection)))
    (let ((chat (s7-render-chat view :projection "p1"))
          (before (fixture-table)))
      (ok (and (stringp chat) (plusp (length chat)))
          "s7-render-chat returned non-empty bytes")
      (let ((rendered (s7-render-file view :projection "p1" :input before)))
        (ok rendered "s7-render-file refused a valid projection")
        (let* ((region-start (+ (search "<!-- ROADMAP:START -->" rendered)
                                (length "<!-- ROADMAP:START -->")))
               (region-end (search "<!-- ROADMAP:END -->" rendered :start2 region-start))
               (region (subseq rendered region-start region-end)))
          (check-string= chat region "chat and the marker region carried the same bytes")
          (ok (search (format nil "top matter~%<!-- ROADMAP:START -->") rendered)
              "the marker-region edit lost the leading boundary")
          (ok (search "bottom matter" rendered)
              "the marker-region edit lost the trailing boundary"))
        ;; A missing, duplicate or reversed marker pair refuses, writes nothing.
        (ok (not (s7-render-file view :projection "p1" :input "no markers here~%"))
            "a missing marker pair rendered instead of refusing")
        (ok (not (s7-render-file view :projection "p1"
                              :input (format nil "~A~%A~%~A~%A~%~A~%"
                                             "<!-- ROADMAP:START -->" "<!-- ROADMAP:START -->"
                                             "<!-- ROADMAP:END -->" "<!-- ROADMAP:END -->")))
            "a duplicate marker pair rendered instead of refusing")
        (ok (not (s7-render-file view :projection "p1"
                              :input (format nil "~A~%A~%~A~%"
                                             "<!-- ROADMAP:END -->" "<!-- ROADMAP:START -->")))
            "a reversed marker pair rendered instead of refusing")))))

;;; ------------------------------------------------------------------
;;; 2. render-refuses-a-target-outside-its-roots  SPEC-WORK.md:5635
;;; ------------------------------------------------------------------

(deftest "render-refuses-a-target-outside-its-roots" "docs/SPEC-WORK.md:5635"
    "expected=target-outside-permitted-roots-refused-file-untouched-never-guessed"
  (let* ((view (s7-created :permitted-roots '("docs")))
         (view (nth-value 2 (s7-roadmap-projection-add view (s7-projection-add-request :root "docs" :path "ROADMAP.md")))))
    (multiple-value-bind (okp line) (s7-render-check view :projection "p1" :path "outside/ROADMAP.md")
      (ok (not okp) "a target outside the permitted roots rendered instead of refusing")
      (ok (search "refused" line) (format nil "the refusal line does not say refused: ~A" line)))
    (ok (not (s7-render-file view :projection "p1" :path "docs/../../ROADMAP.md" :input (fixture-table)))
        "a symlink/.. escape rendered instead of refusing")))

;;; ------------------------------------------------------------------
;;; 3. roadmap-outlives-its-work               SPEC-WORK.md:1648-1664
;;; ------------------------------------------------------------------

(deftest "roadmap-outlives-its-work" "docs/SPEC-WORK.md:1648-1664,5622"
    "expected=settled-roadmap-lists-completed-rows;remaining-only;no-replay-on-open"
  (let* ((view (s7-created :members '("acme/work/f1")))
         (view (s7-mark-member-closed view "acme/work/f1")))
    ;; A settled row outlives its work: the named open still lists it.
    (multiple-value-bind (rows line) (s7-query-roadmap view :node "acme/board")
      (ok (member "acme/work/f1" rows :test #'string=)
          "a completed row was pruned instead of left in the table")
      (ok (search "ROADMAP OK" line) (format nil "no ROADMAP OK line: ~A" line)))
    ;; `remaining` is an explicit filter, not the default.
    (multiple-value-bind (rows line) (s7-query-roadmap view :node "acme/board" :remaining t)
      (ok (not (member "acme/work/f1" rows :test #'string=))
          "the completed row was not dropped by the remaining-only filter")
      (ok (search "remaining only" line) (format nil "no remaining only note: ~A" line)))
    ;; A named open reads the retained view record plus bounded indexed reads,
    ;; never a parse of C and never a replay of it.
    (with-instrumentation
      (multiple-value-bind (rows) (s7-query-roadmap view :node "acme/board")
        (declare (ignore rows)))
      (check-equal 0 *parses* "the roadmap open parsed C")
      (check-equal 0 *replays* "the roadmap open replayed C"))))

;;; ------------------------------------------------------------------
;;; 4. roadmap-opened-after-the-window         SPEC-WORK.md:1648-1664
;;; ------------------------------------------------------------------

(deftest "roadmap-opened-after-the-window" "docs/SPEC-WORK.md:1648-1664,5622"
    "expected=named-roadmap-open-never-narrowed-by-default-window"
  (let* ((view (s7-created :members '("acme/work/f1")))
         (view (s7-mark-member-closed view "acme/work/f1" :archived t)))
    (multiple-value-bind (rows line) (s7-query-roadmap view :node "acme/board" :now "2026-09-17T12:00:00Z")
      (ok (member "acme/work/f1" rows :test #'string=)
          "an old row was narrowed by the default window")
      (ok (search "gap=0" line) (format nil "an old row read as a coverage gap: ~A" line)))))

;;; ------------------------------------------------------------------
;;; 5. closed-row-with-archive-absent          SPEC-WORK.md:5421
;;; ------------------------------------------------------------------

(deftest "closed-row-with-archive-absent" "docs/SPEC-WORK.md:5421"
    "expected=archive-absent-prints-gap-and-coverage-gap-note-not-shorter-list"
  (let* ((view (s7-created :members '("acme/work/f1" "acme/work/f2")))
         (view (s7-mark-member-closed view "acme/work/f1" :archived t)))
    (multiple-value-bind (rows line) (s7-query-roadmap view :node "acme/board" :archive-absent t)
      (ok (search "gap=" line) (format nil "no gap= where the archive is absent: ~A" line))
      (ok (search "coverage-gap" line) (format nil "no coverage-gap note: ~A" line))
      (ok (not (member "acme/work/f1" rows :test #'string=))
          "a missing archived body read as a silently shorter list"))))

;;; ------------------------------------------------------------------
;;; 6. applicable-cap-never-hides-a-deny       SPEC-WORK.md:5829
;;; ------------------------------------------------------------------

(deftest "applicable-cap-never-hides-a-deny" "docs/SPEC-WORK.md:5829"
    "expected=deny-in-cut-note-still-excludes;NOTES-MORE;unloadable-notes-index-prints-FAIL"
  (let ((notes '((:id "n1" :sort 1 :deny nil)
                 (:id "n2" :sort 2 :deny nil)
                 (:id "n3" :sort 3 :deny "deepseek/chat"))))
    (multiple-value-bind (rows line) (s7-ask-applicable notes :max 1 :candidate "deepseek/chat")
      (ok (search "excluded" line) (format nil "the deny was hidden by the display cap: ~A" line))
      (ok (search "n3" line) (format nil "the excluded row does not name its note: ~A" line))
      (ok (search "NOTES MORE" line) (format nil "the capped answer did not print NOTES MORE: ~A" line))
      (ok (null (intersection (mapcar (lambda (n) (getf n :id)) rows) '("n3") :test #'string=))
          "the deny was listed among the eligible verdict rows"))
    (multiple-value-bind (rows line) (s7-ask-applicable notes :max 1 :candidate "deepseek/chat"
                                                           :notes-index-unloadable t)
      (ok (search "FAIL" line) (format nil "an unloadable notes index printed no FAIL: ~A" line))
      (ok (not (search "eligible" line)) "an unloadable notes index printed an eligible row")
      (ok (null rows) "an unloadable notes index printed verdict rows"))))
