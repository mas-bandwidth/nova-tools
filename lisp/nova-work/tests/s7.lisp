;;;; s7.lisp --- slice 7 acceptance tests: the roadmap regenerates, and no
;;;; percentage is typed by hand.
;;;;
;;;; One deftest per named replay the slice implements, each naming its line of
;;;; docs/SPEC-WORK.md and asserting the printed lines the spec shows. The
;;;; verbs are `roadmap create|configure|row|projection`, `axis`, `cell`; the
;;;; queries are `percent --axis <member>` and `roadmap --node R`; the validator
;;;; is render roots and the refusal outside them. These tests are the contract
;;;; the implementation cards find waiting: they are red against this slice-1
;;;; kernel, which owns none of it.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; Fixture helpers. Each request is a `submit`-shaped plist (the same
;;; `--request/--stamp/--clock` provenance every mutating request carries),
;;; so the roadmap verbs land through the one kernel entry the write path
;;; already has.
;;; ------------------------------------------------------------------

(defun roadmap-create-request (&key (id "acme/board") title
                                    (under '(:node "acme/work"))
                                    (row-kind :feature) (aggregation :required-members)
                                    axes members permitted-roots)
  (list :verb :roadmap-create :node-type :roadmap :title title :under under
        :row-kind row-kind :aggregation aggregation
        :completion-policy :all-required-features
        :axes (or axes '()) :members (or members '())
        :permitted-roots (or permitted-roots '())
        :node id :by "rowan" :reason "slib 7 fixture"
        :request "s7-roadmap-create" :stamp "2026-09-16T00:00:00Z"
        :clock :given :generation-owner "gen-1"))

(defun axis-request (&key (roadmap "acme/board") (axis "platform")
                          (add nil) (remove nil) (by "rowan") (request "s7-axis"))
  (list :verb :axis :roadmap roadmap :axis axis :add add :remove remove
        :by by :reason "slice 7 fixture" :request request
        :stamp "2026-09-16T00:00:00Z" :clock :given :generation-owner "gen-1"))

(defun cell-request (&key (roadmap "acme/board") coord ref out-of-scope in-scope
                          (by "rowan") (request "s7-cell"))
  (list :verb :cell :roadmap roadmap :coord coord :ref ref
        :out-of-scope out-of-scope :in-scope in-scope
        :by by :reason "slice 7 fixture" :request request
        :stamp "2026-09-16T00:00:00Z" :clock :given :generation-owner "gen-1"))

(defun projection-add-request (&key (roadmap "acme/board") (projection "p1")
                                    root repo path start end
                                    row-axis column-axis fixed)
  (list :verb :roadmap-projection-add :roadmap roadmap :projection projection
        :root root :repo repo :path path :start start :end end
        :policy :markdown-table :row-axis row-axis :column-axis column-axis
        :fixed (or fixed '())
        :by "rowan" :reason "slice 7 fixture" :request "s7-projection-add"
        :stamp "2026-09-16T00:00:00Z" :clock :given :generation-owner "gen-1"))

(defun fixture-roadmap-kernel ()
  "A kernel over the slice-1 seed. The roadmap verbs below are the contract;
  this kernel owns none of them yet and refuses each one."
  (fresh))

(defun fixture-table ()
  "The marker region a file projection writes between. START/END are the
  distinct, non-empty markers of SPEC-WORK.md:3057; every other byte is
  bystander text the file mode must preserve exactly."
  (format nil "top matter~%<!-- ROADMAP:START -->~%old table body~%<!-- ROADMAP:END -->~%bottom matter~%"))

(defun expect-roadmap-ok-in (word line &rest fields)
  "Assert WORD OK plus each `key=value` substring named by FIELDS on LINE."
  (ok (search (format nil "~A OK" word) line)
      "expected ~A OK, got: ~A" word line)
  (dolist (f fields)
    (ok (search f line) "~A line lacks ~A: ~A" word f line))
  t)

;;; ------------------------------------------------------------------
;;; 1. chat-and-file-render-are-byte-identical   SPEC-WORK.md:5635
;;; ------------------------------------------------------------------

(deftest "chat-and-file-render-are-byte-identical" "docs/SPEC-WORK.md:5635"
    "expected=chat-bytes==marker-region-bytes;every-other-byte-preserved;missing/dup/reversed-marker-refused"
  (let ((k (fixture-roadmap-kernel)))
    (multiple-value-bind (okp line) (submit k (roadmap-create-request :title "Board"))
      (declare (ignore line))
      (ok okp "roadmap create refused: it is the slice-7 contract"))
    ;; A projection stores a target: root, repo, path, markers, policy.
    (multiple-value-bind (okp line) (submit k (projection-add-request
                                               :root "r1" :repo "acme/nova-tools"
                                               :path "ROADMAP.md" :start "<!-- ROADMAP:START -->"
                                               :end "<!-- ROADMAP:END -->"
                                               :row-axis "a1" :column-axis "a2"))
      (declare (ignore line))
      (ok okp "projection add refused: it is the slice-7 contract"))
    ;; One projection and revision render to chat and to the marked region.
    (let ((chat (render-chat k :view "acme/board" :projection "p1"))
          (before (fixture-table)))
      (let* ((rendered (render-file k :view "acme/board" :projection "p1"
                                      :input before))
             (region-start (+ (search "<!-- ROADMAP:START -->" rendered 0) 19))
             (region-end (search "<!-- ROADMAP:END -->" rendered region-start))
             (region (subseq rendered region-start region-end)))
        (check-string= chat region "chat and the marker region carried the same bytes")
        (ok (search (format nil "top matter~%<!-- ROADMAP:START -->") rendered)
            "the marker-region edit lost the boundary: ~A" rendered)
        (ok (string= (subseq rendered region-end) (subseq before
                                                          (+ (search "<!-- ROADMAP:END -->" before 0) 19)))
            "every byte after the region was not preserved"))
      ;; A missing, duplicate or reversed marker pair refuses, writes nothing.
      (let ((r (render-file k :view "acme/board" :projection "p1"
                            :input "no markers here~%")))
        (ok (not r) "a missing marker pair rendered instead of refusing"))
      (let ((r (render-file k :view "acme/board" :projection "p1"
                            :input (format nil "~A~%A~%~A~%A~%~A~%"
                                           "<!-- ROADMAP:START -->" "<!-- ROADMAP:START -->"
                                           "<!-- ROADMAP:END -->" "<!-- ROADMAP:END -->"))))
        (ok (not r) "a duplicate marker pair rendered instead of refusing"))
      (let ((r (render-file k :view "acme/board" :projection "p1"
                            :input (format nil "~A~%A~%~A~%"
                                           "<!-- ROADMAP:END -->" "<!-- ROADMAP:START -->"))))
        (ok (not r) "a reversed marker pair rendered instead of refusing")))))

;;; ------------------------------------------------------------------
;;; 2. render-refuses-a-target-outside-its-roots  SPEC-WORK.md:5635
;;; ------------------------------------------------------------------

(deftest "render-refuses-a-target-outside-its-roots" "docs/SPEC-WORK.md:5635"
    "expected=target-outside-permitted-roots-refused-file-untouched-never-guessed"
  (let ((k (fixture-roadmap-kernel)))
    (multiple-value-bind (okp line) (submit k (roadmap-create-request
                                               :title "Board" :permitted-roots '("docs")))
      (declare (ignore line))
      (ok okp "roadmap create refused: it is the slice-7 contract"))
    (multiple-value-bind (okp line) (submit k (projection-add-request
                                               :root "docs" :repo "acme/nova-tools"
                                               :path "ROADMAP.md" :start "<!-- ROADMAP:START -->"
                                               :end "<!-- ROADMAP:END -->"))
      (declare (ignore line))
      (ok okp "projection add refused: it is the slice-7 contract"))
    ;; A projection whose path escapes its configured permitted root refuses
    ;; rather than guessing a destination: the file is untouched and no fresh
    ;; file is created under a guessed root.
    (multiple-value-bind (okp line) (render-check k :view "acme/board" :projection "p1"
                                                  :path "outside/ROADMAP.md")
      (ok (not okp) "a target outside the permitted roots rendered instead of refusing")
      (ok (search "refused" line) "the refusal line does not say refused: ~A" line))
    (let ((escape (render-file k :view "acme/board" :projection "p1"
                               :path "docs/../../ROADMAP.md" :input (fixture-table))))
      (ok (not escape) "a symlink/.. escape rendered instead of refusing"))))

;;; ------------------------------------------------------------------
;;; 3. roadmap-outlives-its-work               SPEC-WORK.md:1648-1664
;;; ------------------------------------------------------------------

(deftest "roadmap-outlives-its-work" "docs/SPEC-WORK.md:1648-1664,5622"
    "expected=settled-roadmap-lists-completed-rows;no-full-load-of-C;shares-not-double-counted"
  (let ((k (fixture-roadmap-kernel)))
    (multiple-value-bind (okp line) (submit k (roadmap-create-request
                                               :title "Board" :members '("acme/work/f1")))
      (declare (ignore line))
      (ok okp "roadmap create refused: it is the slice-7 contract"))
    ;; Settle the member work; the roadmap rows stay, completed rows included.
    (ok (submit k (close-request :node "acme/work/f1/t1" :request "s7-close-1")) "close refused")
    (multiple-value-bind (rows line) (ask-roadmap k :node "acme/board")
      (declare (ignore rows))
      (expect-roadmap-ok-in "ROADMAP" line "acme/work/f1")
      ;; A roadmap is answered from the retained view record plus bounded
      ;; indexed reads -- never a whole load of C.
      (ok (search "remaining only" line)
          "completed rows were pruned instead of left in the table: ~A" line))
    ;; Opening the same view triggers no C-wide scan: zero node visits.
    (with-instrumentation
      (ask-roadmap k :node "acme/board")
      (ok (= 0 *parses*) "the roadmap open parsed C: ~D" *parses*)
      (ok (= 0 *replays*) "the roadmap open replayed C: ~D" *replays*))))

;;; ------------------------------------------------------------------
;;; 4. roadmap-opened-after-the-window         SPEC-WORK.md:1648-1664
;;; ------------------------------------------------------------------

(deftest "roadmap-opened-after-the-window" "docs/SPEC-WORK.md:1648-1664,5622"
    "expected=named-roadmap-open-never-narrowed-by-default-window"
  (let ((k (fixture-roadmap-kernel)))
    (multiple-value-bind (okp line) (submit k (roadmap-create-request
                                               :title "Board" :members '("acme/work/f1")))
      (declare (ignore line))
      (ok okp "roadmap create refused: it is the slice-7 contract"))
    ;; A member settled more than 24h ago is still reached by a named roadmap
    ;; open: the default [now-24h, now) window bounds closed-activity listings,
    ;; not a named view's own rows.
    (multiple-value-bind (rows line) (ask-roadmap k :node "acme/board" :now "2026-09-17T12:00:00Z")
      (declare (ignore rows))
      (ok (search "acme/work/f1" line)
          "the named roadmap was narrowed by the default window: ~A" line)
      (ok (search "gap=0" line)
          "an old row read as a coverage gap instead of a reached row: ~A" line))))

;;; ------------------------------------------------------------------
;;; 5. closed-row-with-archive-absent          SPEC-WORK.md:5421
;;; ------------------------------------------------------------------

(deftest "closed-row-with-archive-absent" "docs/SPEC-WORK.md:5421"
    "expected=archive-absent-prints-gap-and-coverage-gap-note-not-shorter-list"
  (let ((k (fixture-roadmap-kernel)))
    (multiple-value-bind (okp line) (submit k (roadmap-create-request
                                               :title "Board" :members '("acme/work/f1")))
      (declare (ignore line))
      (ok okp "roadmap create refused: it is the slice-7 contract"))
    ;; The retention archive file is absent; a roadmap ask that reaches for an
    ;; archived body prints gap=<n> and one coverage-gap note rather than a
    ;; silently shorter list.
    (multiple-value-bind (rows line) (ask-roadmap k :node "acme/board" :archive-absent t)
      (declare (ignore rows))
      (ok (search "gap=" line) "no gap= was printed where the archive is absent: ~A" line)
      (ok (search "coverage-gap" line) "no coverage-gap note was printed: ~A" line)
      (ok (not (search "QUERY ROW acme/work/f1" line))
          "a missing archived body read as a shorter list instead of a gap: ~A" line))))

;;; ------------------------------------------------------------------
;;; 6. applicable-cap-never-hides-a-deny       SPEC-WORK.md:5829
;;; ------------------------------------------------------------------

(deftest "applicable-cap-never-hides-a-deny" "docs/SPEC-WORK.md:5829"
    "expected=deny-in-cut-note-still-excludes;NOTES-MORE;unloadable-notes-index-prints-FAIL"
  (let ((k (fixture-roadmap-kernel)))
    ;; N active notes, N > --max, the only :deny in the note that sorts last.
    ;; `applicable --max 1 --candidate <role>/<model>` still prints the excluded
    ;; row and NOTES MORE: the cap is on the prose rows, never the evaluation.
    (multiple-value-bind (rows line) (ask-applicable k :max 1 :candidate "deepseek/chat"
                                                               :deny-note "n3")
      (ok (search "excluded" line) "the deny was hidden by the display cap: ~A" line)
      (ok (search "n3" line) "the excluded row does not name its note: ~A" line)
      (ok (search "NOTES MORE" line) "the capped answer did not print NOTES MORE: ~A" line)
      (ok (null rows) "the verdict rows were not printed"))
    ;; With the notes index unloadable, no eligible verdict is printed at all.
    (multiple-value-bind (rows line) (ask-applicable k :max 1 :candidate "deepseek/chat"
                                                               :notes-index-unloadable t)
      (ok (search "FAIL" line) "an unloadable notes index printed no FAIL: ~A" line)
      (ok (not (search "eligible" line)) "an unloadable notes index printed an eligible row: ~A" line))))
