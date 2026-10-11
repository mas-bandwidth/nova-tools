(release "v1.2.12" :status "planned" :text "Tests and models only, no product change.")

(fix "cover-member-push-with-a-scripted-git"
  :release "v1.2.12"
  :title "Pin the member's push with a scripted git"
  :text "A unit test in cmd/nova-swarm drives (g *gitPusher) Push (memberpush.go:88) against a scripted git: the head-shape and branch-name refusals, the missing repository and checkout, the bare push repository's first use, the fetch and push retries with their 1s/2s/4s waits, the head-on-branches, staged-commit and descend checks, the unforced push argv from push.git with GIT_TERMINAL_PROMPT=0, and the no-gh pull-request note. Push's unit statement coverage moves from 0.0% to 94.2%."
  :origin "cover-member-push-with-a-scripted-git.w1")
