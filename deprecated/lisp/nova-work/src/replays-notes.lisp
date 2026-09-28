;;;; replays-notes.lisp --- the coordinator's notes: the pure core the six notes
;;;;; replays name. A note is one record, its id the content digest, refused or
;;;; superseded by the same rules docs/SPEC-WORK.md:4184-4300 writes, with no
;;;; session, CLI, socket or provider here (see kernel.lisp for the boundary).
;;;;
;;;; The note record itself is `assignment.lisp`'s plist (`make-note`), so the
;;;; applicable/delegation replays keep reading the same representation; this
;;;; file adds the write/supersede core and the accessors the notes replays use.

(in-package #:nova-work)

(defstruct (notes-config (:conc-name nconfig-))
  max-active max-text-bytes max-constraint-nodes participants groups)

(defstruct (notes-store (:constructor %make-notes-store) (:conc-name nstore-))
  config notes)

(defparameter *note-kinds* '(:instruction :observation :heuristic))

;;; Reads over a note (the plist `make-note` returns) and a store. Superseded is
;;; nil (active) or (:superseded-by id date).

(defun note-id (note) (getf note :id))
(defun note-scope (note) (getf note :scope))
(defun note-author (note) (getf note :author))
(defun note-date (note) (getf note :date))
(defun note-source (note) (getf note :source))
(defun note-kind (note) (getf note :kind))
(defun note-text (note) (getf note :text))
(defun note-constraint (note) (getf note :constraint))
(defun note-uncertain (note) (getf note :uncertain))
(defun note-superseded (note) (getf note :superseded))

(defun notes-notes (store) (nstore-notes store))
(defun active-note-p (n) (null (note-superseded n)))
(defun notes-active (store) (remove-if-not #'active-note-p (nstore-notes store)))
(defun notes-active-count (store) (length (notes-active store)))
(defun note-by-id (store id)
  (find id (nstore-notes store) :key #'note-id :test #'string=))

(defun make-notes-store (&key config notes)
  (%make-notes-store :config config :notes (or notes '())))

;;; Identity is the content (SPEC-WORK.md:4212-4217): the id is `note:` followed
;;; by SHA-256 over the canonical serialization of the eight fields, so the same
;;; preimage is the same id on every bench and a second write is refused.

(defun note-did-for (&key scope author date source kind text constraint uncertain)
  (note-id-fields scope author date source kind text constraint uncertain))

;;; Bounds and writer scope, the refusals (SPEC-WORK.md:4227-4265).

(defun string-utf8-bytes (text)
  (loop for ch across text summing (char-utf8-bytes ch)))

(defun constraint-nodes (form)
  "The number of atomic leaves in a constraint form; absent is none."
  (if (absentp form)
      0
      (typecase form
        (null 0)
        (cons (+ (constraint-nodes (car form)) (constraint-nodes (cdr form))))
        (t 1))))

(defun %field-bytes (value)
  (if (absentp value) 0 (string-utf8-bytes value)))

(defun %group-members (config group)
  (cdr (assoc group (nconfig-groups config) :test #'string=)))

(defun %participant-p (config name)
  (member name (nconfig-participants config) :test #'string=))

(defun %writer-of-scope (config as scope)
  (let ((tag (and (consp scope) (car scope))))
    (cond
      ((eq tag :coordinator)
       (and (%participant-p config as) (string= as (second scope))))
      ((eq tag :group)
       (and (%participant-p config as)
            (member as (%group-members config (second scope)) :test #'string=)))
      ((eq tag :node)
       (%participant-p config as))
      (t nil))))

(defun %require-field (request key field)
  (let ((value (getf request key)))
    (when (or (null value) (and (stringp value) (string= value "")))
      (error 'unsupported-input :what (format nil "missing ~A" field)))
    value))

(defun %required-bound (value name)
  (or value (error 'unsupported-input :what (format nil "refusing to guess ~A" name))))

(defun %check-bounds (config note)
  (let ((lim (%required-bound (nconfig-max-text-bytes config) ":max-text-bytes")))
    (dolist (entry (list (list "text" (note-text note))
                         (list "source" (note-source note))
                         (list "uncertain" (note-uncertain note))))
      (let ((n (%field-bytes (second entry))))
        (when (> n lim)
          (error 'unsupported-input
                 :what (format nil "~A=~D past :max-text-bytes=~D" (first entry) n lim))))))
  (let ((lim (%required-bound (nconfig-max-constraint-nodes config) ":max-constraint-nodes")))
    (let ((n (constraint-nodes (note-constraint note))))
      (when (> n lim)
        (error 'unsupported-input
               :what (format nil "constraint=~D past :max-constraint-nodes=~D" n lim))))))

(defun %check-max-active (config store)
  (let ((lim (%required-bound (nconfig-max-active config) ":max-active")))
    (let ((n (notes-active-count store)))
      (when (>= n lim)
        (error 'unsupported-input
               :what (format nil "active=~D past :max-active=~D" n lim))))))

(defun %line-note-id (request)
  (let ((source (getf request :source))
        (date (getf request :date))
        (kind (getf request :kind))
        (as (getf request :as))
        (scope (getf request :scope)))
    (if (and (stringp source) (plusp (length source))
             (stringp date) (plusp (length date))
             (member kind *note-kinds*)
             (stringp as) (plusp (length as))
             (consp scope))
        (note-did-for :scope scope :author as :date date :source source :kind kind
                      :text (getf request :text)
                      :constraint (getf request :constraint)
                      :uncertain (getf request :uncertain))
        "-")))

;;; write (SPEC-WORK.md:4278-4281).

(defun %notes-write (store request)
  (let ((config (nstore-config store))
        (source (%require-field request :source ":source"))
        (date (%require-field request :date ":date"))
        (text (%require-field request :text ":text"))
        (kind (getf request :kind))
        (as (getf request :as))
        (scope (getf request :scope)))
    (unless (member kind *note-kinds*)
      (error 'unsupported-input
             :what (format nil "kind ~A is not one of :instruction :observation :heuristic" kind)))
    (unless (%writer-of-scope config as scope)
      (error 'unsupported-input :what "not a writer of the scope"))
    (let ((note (make-note :scope scope :author as :date date :source source
                           :kind kind :text text
                           :constraint (getf request :constraint +absent+)
                           :uncertain (getf request :uncertain +absent+))))
      (%check-bounds config note)
      (when (note-by-id store (note-id note))
        (error 'unsupported-input :what (format nil "already written note=~A" (note-id note))))
      (%check-max-active config store)
      (%make-notes-store :config config :notes (append (nstore-notes store) (list note))))))

(defun notes-write (store request)
  (handler-case
      (let ((new (%notes-write store request)))
        (let ((n (car (last (nstore-notes new)))))
          (values t (format nil "NOTES OK note=~A change=write" (note-id n)) 0 new)))
    (unsupported-input (c)
      (values nil (format nil "NOTES FAIL note=~A: ~A"
                          (%line-note-id request) (unsupported-input-what c))
              2 store))))

;;; supersede (SPEC-WORK.md:4282-4299): one mutation, two events, all-or-none.

(defun check-not-weaker-kind (old-kind new-kind old-id)
  "A :heuristic or :observation cannot supersede an :instruction."
  (when (and (eq old-kind :instruction)
             (member new-kind '(:observation :heuristic)))
    (error 'unsupported-input :what (format nil "kind ~A weaker than ~A" new-kind old-id)))
  t)

(defun make-replacement-note (old owner source date &key text constraint uncertain)
  "The replacement inherits :scope and :kind, the author is the superseder, the
  rest (a new :source above all) comes from the flags."
  (let ((kind (note-kind old)))
    (check-not-weaker-kind (note-kind old) kind (note-id old))
    (make-note :scope (note-scope old) :kind kind :author owner
               :source source :date date
               :text text :constraint constraint :uncertain uncertain)))

(defun %notes-supersede (store request)
  (let* ((config (nstore-config store))
         (as (getf request :as))
         (old-id (getf request :id))
         (old (note-by-id store old-id)))
    (unless (and old (active-note-p old))
      (error 'unsupported-input
             :what (format nil "not active: superseded-by ~A"
                           (if (and old (note-superseded old))
                               (getf (note-superseded old) :superseded-by)
                               "-"))))
    (unless (%writer-of-scope config as (note-scope old))
      (error 'unsupported-input :what "not a writer of the scope"))
    (let ((repl (make-replacement-note old as
                                       (%require-field request :source ":source")
                                       (%require-field request :date ":date")
                                       :text (%require-field request :text ":text")
                                       :constraint (getf request :constraint +absent+)
                                       :uncertain (getf request :uncertain +absent+))))
      (%check-bounds config repl)
      (when (note-by-id store (note-id repl))
        (error 'unsupported-input :what (format nil "already written note=~A" (note-id repl))))
      ;; supersede never changes the active count, so no :max-active check here.
      (%make-notes-store
       :config config
       :notes (append
               (mapcar (lambda (n)
                         (if (string= (note-id n) old-id)
                             (list* :superseded
                                    (list :superseded-by (note-id repl) (note-date repl))
                                    (copy-list n))
                             n))
                       (nstore-notes store))
               (list repl))))))

(defun notes-supersede (store request)
  (handler-case
      (let ((new (%notes-supersede store request)))
        (let ((n (car (last (nstore-notes new)))))
          (values t (format nil "NOTES OK note=~A change=supersede superseded=~A"
                            (note-id n) (getf request :id))
                  0 new)))
    (unsupported-input (c)
      (values nil (format nil "NOTES FAIL note=~A: ~A"
                          (or (getf request :id) "-") (unsupported-input-what c))
              2 store))))
