This directory is nova-wake's fixture: two job directories, each holding a
RESULT.md, referenced by nothing outside cmd/nova-wake/testdata. The watcher
looks for RESULT.md at any depth under a --reports directory and for nothing
else, so the file beside them is here to prove it is ignored.
