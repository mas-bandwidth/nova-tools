;;;; s8-replies.lisp --- pipeline reply correlation (S8).
;;;;
;;;; docs/SPEC-WORK.md:5496-5503 — `pipeline-replies-are-correlated`. Response
;;;; frames arrive out of order and in fragments; every response reaches only
;;;; its matching request and the operation id stays distinct. Unknown,
;;;; duplicate and absent response ids refuse without falsely settling an
;;;; outstanding request. This file holds the pure correlation and reassembly;
;;;; the framed wire is the endpoint card's.

(in-package #:nova-work)

(defun reassemble-frames (frames)
  "FRAMES are `(:op-id <id> :seq <n> :frag <text>)`. Fragments of one operation
  reassemble in sequence order; returned per operation, in first-appearance
  order, as `(<op-id> <payload>)`."
  (let ((order '())
        (table (make-hash-table :test #'equal)))
    (dolist (frame frames)
      (let ((opid (getf frame :op-id))
            (seq (getf frame :seq))
            (frag (getf frame :frag)))
        (unless (gethash opid table)
          (push opid order)
          (setf (gethash opid table) '()))
        (push (cons seq frag) (gethash opid table))))
    (loop for opid in (nreverse order)
          collect (list opid
                        (apply #'concatenate 'string
                               (mapcar #'cdr
                                       (sort (gethash opid table) #'< :key #'car)))))))

(defun correlate-replies (requests responses)
  "REQUESTS are `(:id <id> :op <op>)`; RESPONSES are
  `(:op-id <id> :request-id <id> :payload <text>)`. Returns, in request order,
  `(<id> <op-id> <payload>)`, with the reply fields NIL where no response
  settled that request. A response with no op id, a duplicate op id, or an
  unknown request id refuses before settling anything outstanding."
  (let ((known (make-hash-table :test #'equal))
        (matched (make-hash-table :test #'equal))
        (seen (make-hash-table :test #'equal)))
    (dolist (req requests)
      (setf (gethash (getf req :id) known) t))
    (dolist (resp responses)
      (let ((opid (getf resp :op-id))
            (rid (getf resp :request-id)))
        (when (or (null opid) (absentp opid))
          (error 'unsupported-input :what "response with no decodable id"))
        (when (gethash opid seen)
          (error 'unsupported-input :what (format nil "duplicate response id ~A" opid)))
        (setf (gethash opid seen) t)
        (unless (gethash rid known)
          (error 'unsupported-input :what (format nil "response ~A names no request ~A" opid rid)))
        (setf (gethash rid matched) resp)))
    (loop for req in requests
          for rid = (getf req :id)
          for resp = (gethash rid matched)
          collect (list rid
                        (and resp (getf resp :op-id))
                        (and resp (getf resp :payload))))))
