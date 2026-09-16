;;;; s6-savepoint.lisp --- the local durable snapshot.
;;;;
;;;; docs/SPEC-WORK.md:538-546 — a *savepoint* is this bench's validated atomic
;;;; snapshot of the resident state with its journal boundary; a *checkpoint* is
;;;; the shared clip commit. A savepoint writes neither the clip's deterministic
;;;; snapshot file nor a retention archive, holds no retention boundary, and is
;;;; never reported as a backup. `savepoint create|list|verify|restore|compare`:
;;;; the rev= is this bench's savepoint, checkpoint= the newest clipped one, so a
;;;; local success can never be read as a shared backup.

(in-package #:nova-work)

(defstruct (savepoint (:conc-name sp-))
  (id nil) (rev 0) (checkpoint nil) (pushed nil) (boundary 0) (age nil)
  (unshared 0) (manifest nil) (at nil) (verdict :unverified))

(defun savepoint-ok-line (sp &key (emitted 0))
  (format nil "SAVEPOINT OK id=~A rev=~D checkpoint=~A pushed=~A boundary=~D age=~A unshared=~D manifest=~A shown=0 emitted=~D"
          (sp-id sp) (sp-rev sp)
          (or (sp-checkpoint sp) "-") (or (sp-pushed sp) "-")
          (sp-boundary sp) (or (sp-age sp) "-") (sp-unshared sp)
          (or (sp-manifest sp) "-") emitted))

(defun verdict-name (verdict)
  (ecase verdict
    (:good "good") (:corrupt "corrupt") (:unverified "unverified") (:failed "failed")))

(defun savepoint-row-line (sp)
  (format nil "SAVEPOINT ROW id=~A rev=~D at=~A manifest=~A verdict=~A"
          (sp-id sp) (sp-rev sp) (or (sp-at sp) "-") (or (sp-manifest sp) "-")
          (verdict-name (sp-verdict sp))))

(defun savepoint-verify (sp image-digest)
  "Re-validate a savepoint's atomic image against the digest the manifest names:
good, corrupt, or unverified when the manifest is missing."
  (setf (sp-verdict sp)
        (cond ((not (sp-manifest sp)) :unverified)
              ((string= (sp-manifest sp) image-digest) :good)
              (t :corrupt)))
  sp)

(defun savepoint-compare (a b)
  "Answer the fields that differ between two savepoints, so a local success can
never be read as a shared backup."
  (let ((diffs '()))
    (flet ((getf-named (sp field)
             (ecase field
               (:rev (sp-rev sp))
               (:checkpoint (sp-checkpoint sp))
               (:pushed (sp-pushed sp))
               (:boundary (sp-boundary sp))
               (:manifest (sp-manifest sp)))))
      (dolist (field '(:rev :checkpoint :pushed :boundary :manifest))
        (unless (equal (getf-named a field) (getf-named b field))
          (push field diffs))))
    (nreverse diffs)))

(defun savepoint-note-line (kind since)
  "`SAVEPOINT NOTE recovery-gap kind=<…> since=<rev>` — reported, never rounded to
success; neither a torn tail nor a corrupt record is truncated."
  (format nil "SAVEPOINT NOTE recovery-gap kind=~A since=~A" kind since))

(defun savepoint-fail-line (sp reason)
  "`SAVEPOINT FAIL id=<id> rev=<n>: <reason>` — a cut inside an envelope, a
journal mismatch, an image revision differing from its cut: nothing published,
the previous verified savepoint kept."
  (format nil "SAVEPOINT FAIL id=~A rev=~D: ~A" (sp-id sp) (sp-rev sp) reason))
