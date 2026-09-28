;;;; criterion-e08-f01-02.lisp --- nova-work criterion E08-F01-02:
;;;; "Provide bounded family/verb help and machine discovery with schema hash"
;;;; (docs/roadmaps/nova-work.sexp:1006, feature E08-F01 "Versioned typed
;;;; command schema and discovery"; the verbs it describes are the block of
;;;; docs/SPEC-WORK.md:2258-2348, whose last line is `nova-work help`).
;;;;
;;;; The criterion was proved UNMET by a red card of wave 1 (nova-tools#2056:
;;;; "nine cards proved their criterion UNMET with a red test"): the engine had
;;;; no help and no discovery, so a reader learned a verb only by carrying the
;;;; whole verbs block in context. These cases pin the four halves of the
;;;; sentence:
;;;;
;;;;   bounded     -- every answer is at most --max rows and says how many it
;;;;                  left (more=, next=), never a silent cut; the top level
;;;;                  names families and verb counts, never a usage line;
;;;;   family/verb -- `help <family>` names that family's verbs and nothing
;;;;                  else, `help <verb>` prints that verb's forms and nothing
;;;;                  else;
;;;;   machine     -- `help --machine` is one DISCOVERY VERB row per verb, every
;;;;                  field named, paged by --after;
;;;;   schema hash -- every answer carries schema=<sha256> of the one schema
;;;;                  the rows come from, the same on every page and every
;;;;                  view, and it moves when the schema moves.
;;;;
;;;; And one drift guard: the schema holds exactly the verbs and forms of the
;;;; spec's verbs block, read from the file at test time.

(in-package #:nova-work/tests)

(defun %help (request)
  "(values OK LINES EXIT) for one help REQUEST line."
  (nova-work::help-answer request))

(defun %field (line key)
  "The value of KEY= in LINE, up to the next space; NIL when absent."
  (let* ((tag (concatenate 'string " " key "="))
         (at (search tag line)))
    (when at
      (let* ((start (+ at (length tag)))
             (end (or (position #\Space line :start start) (length line))))
        (subseq line start end)))))

(defun %tail-field (line key)
  "The value of KEY=, the LAST field of LINE, to the end of the line."
  (let* ((tag (concatenate 'string " " key "="))
         (at (search tag line)))
    (when at (subseq line (+ at (length tag))))))

(defun %rows (lines prefix)
  (remove-if-not (lambda (l) (and (>= (length l) (length prefix))
                                  (string= prefix l :end2 (length prefix))))
                 lines))

(defun %hex64-p (s)
  (and (stringp s) (= (length s) 64)
       (every (lambda (c) (find c "0123456789abcdef")) s)))

(defun %spec-verbs-block ()
  "The verb names and form counts of docs/SPEC-WORK.md's verbs block, read from
the file: the fenced block that ends with the line `nova-work help`."
  (let* ((path (asdf:system-relative-pathname :nova-work "../../docs/SPEC-WORK.md"))
         (lines (with-open-file (in path :external-format :utf-8)
                  (loop for l = (read-line in nil) while l collect l)))
         (help-at (position "nova-work help" lines :test #'string=))
         (open-at (position-if (lambda (l) (and (>= (length l) 3) (string= "```" l :end2 3)))
                               lines :end help-at :from-end t))
         (forms '()))
    (ok help-at "the spec's verbs block ends with `nova-work help`")
    (loop for l in (subseq lines (1+ open-at) (1+ help-at))
          when (and (> (length l) 10) (string= "nova-work " l :end2 10))
            do (let* ((words (nova-work::%request-words (subseq l 10)))
                      (second (second words))
                      (name (if (and second (not (find (char second 0) "-([<")))
                                (format nil "~A ~A" (first words) second)
                                (first words))))
                 (push name forms)))
    (nreverse forms)))

(deftest "e08-f01-02-top-level-names-families-never-usage"
    "docs/roadmaps/nova-work.sexp:1006" "HELP FAMILY rows + HELP OK schema="
  (multiple-value-bind (ok lines exit) (%help "help")
    (ok ok "help answers OK")
    (check-equal 0 exit "help exit")
    (let ((families (%rows lines "HELP FAMILY "))
          (last (car (last lines))))
      (check-equal 12 (length families) "family rows")
      (check-equal 73 (reduce #'+ families
                              :key (lambda (l) (parse-integer (%field l "verbs"))))
                   "verbs summed over the families")
      (ok (null (%rows lines "HELP USAGE ")) "the top level carries no usage line")
      (ok (every (lambda (l) (< (length l) 120)) lines)
          "every top-level line is short: the manual is not carried")
      (check-string= "HELP OK" (subseq last 0 7) "the last line is HELP OK")
      (check-equal "0" (%field last "more") "more=0 when nothing was left")
      (ok (%hex64-p (%field last "schema")) "schema= is a sha256: ~S" last))))

(deftest "e08-f01-02-family-help-is-that-family-only"
    "docs/roadmaps/nova-work.sexp:1006" "help structure: its six verbs, nothing else"
  (multiple-value-bind (ok lines exit) (%help "help structure")
    (ok ok "family help answers OK")
    (check-equal 0 exit "family help exit")
    (let ((verbs (%rows lines "HELP VERB ")))
      (check-equal 6 (length verbs) "structure verb rows")
      (ok (every (lambda (l) (string= "structure" (%field l "family"))) verbs)
          "every row names family=structure")
      (check-equal '("node add" "node edit" "node move" "node remove" "node require" "decompose")
                   (mapcar (lambda (l) (%tail-field l "verb")) verbs)
                   "the family's verbs, in the spec's order")
      (ok (null (%rows lines "HELP USAGE ")) "a family view carries no usage line"))))

(deftest "e08-f01-02-verb-help-is-that-verb-only"
    "docs/SPEC-WORK.md:2317,2335,2337" "help node add: one form; help heartbeat: two"
  (multiple-value-bind (ok lines exit) (%help "help node add")
    (ok ok "verb help answers OK")
    (check-equal 0 exit "verb help exit")
    (let ((usage (%rows lines "HELP USAGE ")))
      (check-equal 1 (length usage) "node add forms")
      (let ((u (%tail-field (first usage) "usage")))
        (ok (string= "nova-work node add " u :end2 19) "the usage is node add's own line: ~S" u))
      (check-equal 1 (length (remove-if-not (lambda (l) (search "HELP" l)) (butlast lines)))
                   "nothing but the verb's own form")))
  (multiple-value-bind (ok lines) (%help "help heartbeat")
    (ok ok "heartbeat help answers OK")
    (let ((usage (%rows lines "HELP USAGE ")))
      (check-equal 2 (length usage) "heartbeat has a node form and an allocation form")
      (check-equal '("1" "2") (mapcar (lambda (l) (%field l "form")) usage) "form numbers")
      (ok (every (lambda (l) (string= "2" (%field l "forms"))) usage) "forms=2 on each row"))))

(deftest "e08-f01-02-discovery-is-bounded-and-paged"
    "docs/roadmaps/nova-work.sexp:1006" "help --machine --max 10: ten rows, more=, next=; pages cover all 73"
  (let ((seen '()) (schemas '()) (after 0) (pages 0))
    (loop
      (multiple-value-bind (ok lines exit)
          (%help (format nil "help --machine --max 10 --after ~D" after))
        (ok ok "discovery page answers OK")
        (check-equal 0 exit "discovery exit")
        (let ((rows (%rows lines "DISCOVERY VERB "))
              (last (car (last lines))))
          (ok (<= (length rows) 10) "a page holds at most --max rows, got ~D" (length rows))
          (ok (every (lambda (r) (and (%field r "family") (%field r "forms")
                                      (%hex64-p (%field r "sha256")) (%tail-field r "verb")))
                     rows)
              "every DISCOVERY VERB row names family=, forms=, sha256= and verb=")
          (check-string= "DISCOVERY OK" (subseq last 0 12) "the last line is DISCOVERY OK")
          (check-equal "1" (%field last "protocol") "protocol=1")
          (push (%field last "schema") schemas)
          (dolist (r rows) (push (%tail-field r "verb") seen))
          (incf pages)
          (let ((more (parse-integer (%field last "more"))))
            (when (zerop more)
              (check-equal "-" (%field last "next") "next=- on the last page")
              (return))
            (check-equal (- 73 after (length rows)) more "more= counts what is left")
            (setf after (parse-integer (%field last "next")))))))
    (check-equal 8 pages "73 verbs at ten a page")
    (check-equal 73 (length (remove-duplicates seen :test #'string=)) "every verb once")
    (check-equal 73 (length seen) "no verb twice")
    (check-equal 1 (length (remove-duplicates schemas :test #'string=)) "one schema= on every page")))

(deftest "e08-f01-02-schema-hash-is-one-and-moves-with-the-schema"
    "docs/roadmaps/nova-work.sexp:1006" "same schema= in help and discovery; a changed schema moves it"
  (let* ((h1 (%field (car (last (nth-value 1 (%help "help")))) "schema"))
         (h2 (%field (car (last (nth-value 1 (%help "help assignment")))) "schema"))
         (h3 (%field (car (last (nth-value 1 (%help "help take")))) "schema"))
         (h4 (%field (car (last (nth-value 1 (%help "help --machine")))) "schema")))
    (ok (%hex64-p h1) "schema= is a sha256")
    (check-equal (list h1 h1 h1) (list h2 h3 h4) "one schema hash in every view")
    (check-string= (nova-work::help-schema-hash) h1 "the answered hash is the schema's own")
    (let ((nova-work::*verb-schema*
            (cons '("frobnicate" "meta" "nova-work frobnicate --session <path>")
                  nova-work::*verb-schema*)))
      (let ((moved (%field (car (last (nth-value 1 (%help "help")))) "schema")))
        (ok (and (%hex64-p moved) (string/= moved h1))
            "a changed schema answers a different hash: ~S vs ~S" moved h1)))))

(deftest "e08-f01-02-refusals-are-one-line-exit-2"
    "docs/SPEC-WORK.md:2347" "HELP FAIL ...; run: nova-work help at exit 2"
  (dolist (req '("help frobnicate" "help --max 0" "help --max x" "help --machine structure"
                 "help --bogus" "help --machine --after -1"))
    (multiple-value-bind (ok lines exit) (%help req)
      (ok (not ok) "~A is refused" req)
      (check-equal 2 exit (format nil "~A exit" req))
      (check-equal 1 (length lines) (format nil "~A answers one line" req))
      (let ((l (first lines)))
        (check-string= "HELP FAIL" (subseq l 0 9) (format nil "~A first tokens" req))
        (ok (search "; run: nova-work help" l) "~A names the remedy: ~S" req l)))))

(deftest "e08-f01-02-schema-is-the-spec-verbs-block"
    "docs/SPEC-WORK.md:2258-2348" "every verb and form of the block, in its order, and no other"
  (let* ((spec-forms (%spec-verbs-block))
         (spec-verbs (remove-duplicates spec-forms :test #'string= :from-end t)))
    (check-equal spec-verbs (mapcar #'first nova-work::*verb-schema*) "verbs, in the block's order")
    (dolist (v spec-verbs)
      (check-equal (count v spec-forms :test #'string=)
                   (length (cddr (assoc v nova-work::*verb-schema* :test #'string=)))
                   (format nil "forms of ~A" v)))))
