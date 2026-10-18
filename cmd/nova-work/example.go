package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const exampleDetail = `Writes a minimal pair of tree files (tree.lisp, against.lisp) and a recorded
GraphQL import replay fixture (call-01.json, call-02.json) under --dir.
These allow running nova-work verify and nova-work import offline without gh
credentials or network access.

To verify offline:
  nova-work example --dir ./example
  nova-work verify --tree ./example/tree.lisp --against ./example/against.lisp

To import offline:
  nova-work import --org acme --repo acme/widgets --fixture ./example/calls --dry-run
`

const exampleTree = `(work-tree "v1" :source "github" :org "acme" :fetched "2026-10-02T12:00:00Z"
 :repos ((repo "acme/widgets" :url "https://github.com/acme/widgets"
          :archived false :issues ())))
`

const exampleCall01 = `{
 "vars": {
  "owner": "acme",
  "repo": "widgets"
 },
 "reply": {
  "data": {
   "rateLimit": {
    "cost": 1,
    "remaining": 4999,
    "resetAt": "2026-10-02T12:00:00Z"
   },
   "repository": {
    "nameWithOwner": "acme/widgets",
    "url": "https://github.com/acme/widgets",
    "isArchived": false,
    "issues": {
     "totalCount": 0
    }
   }
  }
 }
}
`

const exampleCall02 = `{
 "vars": {
  "n": 50,
  "owner": "acme",
  "repo": "widgets"
 },
 "reply": {
  "data": {
   "rateLimit": {
    "cost": 1,
    "remaining": 4998,
    "resetAt": "2026-10-02T12:00:00Z"
   },
   "repository": {
    "issues": {
     "totalCount": 0,
     "pageInfo": {
      "hasNextPage": false,
      "endCursor": null
     },
     "nodes": []
    }
   }
  }
 }
}
`

func writeExample(c *tool.Call) *tool.Out {
	dir := c.Str("dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return tool.Refuse(fmt.Sprintf("cannot create --dir %s: %v", dir, err))
	}
	callsDir := filepath.Join(dir, "calls")
	if err := os.MkdirAll(callsDir, 0o755); err != nil {
		return tool.Refuse(fmt.Sprintf("cannot create fixture directory %s: %v", callsDir, err))
	}

	files := []struct {
		path    string
		content string
	}{
		{filepath.Join(dir, "tree.lisp"), strings.TrimSpace(exampleTree) + "\n"},
		{filepath.Join(dir, "against.lisp"), strings.TrimSpace(exampleTree) + "\n"},
		{filepath.Join(dir, "a.lisp"), strings.TrimSpace(exampleTree) + "\n"},
		{filepath.Join(dir, "b.lisp"), strings.TrimSpace(exampleTree) + "\n"},
		{filepath.Join(dir, "call-01.json"), exampleCall01},
		{filepath.Join(dir, "call-02.json"), exampleCall02},
		{filepath.Join(callsDir, "call-01.json"), exampleCall01},
		{filepath.Join(callsDir, "call-02.json"), exampleCall02},
	}

	o := tool.Done().Fact("dir", dir)
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			return tool.Refuse(fmt.Sprintf("write %s: %v", f.path, err))
		}
		o.Item("wrote", "file", f.path)
	}
	return o
}
