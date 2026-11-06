//go:build functional

package contract

import "os"

func cairnStore(e *Env) { e.Var("store", e.Dir+"/store") }

func init() {
	app := func(entry, text string) []string {
		return []string{"append", "--store", "{store}", "--session", "s1", "--entry", entry, "--text", text}
	}
	register(
		Fault{Tool: "cairn", Name: "append: store absent", Setup: cairnStore, Argv: app("e1", "hello"),
			Want: Want{Reason: []string{"does not exist"}}},
		Fault{Tool: "cairn", Name: "append: existing empty directory is not a store",
			Setup: func(e *Env) { cairnStore(e); os.Mkdir(e.Dir+"/store", 0o755) }, Argv: app("e1", "hello"),
			Want: Want{Reason: []string{"holds no store"}}},
		Fault{Tool: "cairn", Name: "open: parent not writable",
			Setup: func(e *Env) {
				os.Mkdir(e.Dir+"/ro", 0o755)
				e.Chmod(e.Dir+"/ro", 0o500)
				e.Var("store", e.Dir+"/ro/store")
			},
			Argv: []string{"open", "--store", "{store}", "--session", "s1", "--publish", "manual"},
			Want: Want{Reason: []string{"permission denied"}, Remedy: Look}},
		Fault{Tool: "cairn", Name: "index: store absent", Setup: cairnStore, Argv: []string{"index", "--store", "{store}", "--session", "s1"},
			Want: Want{Reason: []string{"not a directory"}}},
		Fault{Tool: "cairn", Name: "receipt: store absent", Setup: cairnStore, Argv: []string{"receipt", "--store", "{store}", "--session", "s1", "--entry", "e1"},
			Want: Want{Reason: []string{"not a directory"}}},
		Fault{Tool: "cairn", Name: "append: invalid UTF-8",
			Setup: func(e *Env) {
				cairnStore(e)
				e.Write("bad.txt", "\xff\xfebad")
				e.Exec("cairn", "", "open", "--store", "{store}", "--session", "s1", "--publish", "manual")
			},
			Argv: []string{"append", "--store", "{store}", "--session", "s1", "--entry", "e1", "--file", "bad.txt"},
			Want: Want{Reason: []string{"utf-8"}, Remedy: Look}},
		Fault{Tool: "cairn", Name: "append: file unreadable",
			Setup: func(e *Env) {
				cairnStore(e)
				e.Exec("cairn", "", "open", "--store", "{store}", "--session", "s1", "--publish", "manual")
			},
			Argv: []string{"append", "--store", "{store}", "--session", "s1", "--entry", "e1", "--file", "nofile.txt"},
			Want: Want{Reason: []string{"no such file"}, Remedy: Runs}},
		Fault{Tool: "cairn", Name: "append: entry id holds other words",
			Setup: func(e *Env) {
				cairnStore(e)
				e.Exec("cairn", "", "open", "--store", "{store}", "--session", "s1", "--publish", "manual")
				e.Exec("cairn", "", "append", "--store", "{store}", "--session", "s1", "--entry", "e1", "--text", "first")
			},
			Argv: app("e1", "other"), Want: Want{Exit: []int{1}, Reason: []string{"already holds different words"}, Remedy: Look}},
		Fault{Tool: "cairn", Name: "receipt: no such entry",
			Setup: func(e *Env) {
				cairnStore(e)
				e.Exec("cairn", "", "open", "--store", "{store}", "--session", "s1", "--publish", "manual")
			},
			Argv: []string{"receipt", "--store", "{store}", "--session", "s1", "--entry", "nope"},
			Want: Want{Reason: []string{"no such entry"}, Remedy: Look}},
		Fault{Tool: "cairn", Name: "open: unknown publish policy", Setup: cairnStore,
			Argv: []string{"open", "--store", "{store}", "--session", "s1", "--publish", "auto"},
			Want: Want{Reason: []string{"not a policy"}}},
		Fault{Tool: "cairn", Name: "open: flags missing", Argv: []string{"open", "--store", "x"},
			Want: Want{Reason: []string{"--session"}}},
		Fault{Tool: "cairn", Name: "append: flags missing", Argv: []string{"append", "--store", "x"},
			Want: Want{Reason: []string{"--session", "--entry"}}},
		Fault{Tool: "cairn", Name: "memory search: no root", Argv: []string{"memory", "search", "q"},
			Want: Want{Reason: []string{"--root"}}},
		Fault{Tool: "cairn", Name: "memory search: root absent", Argv: []string{"memory", "search", "--root", "{dir}/none", "--channels", "bm25", "--k", "5", "q"},
			Want: Want{Reason: []string{"not a readable directory"}, Remedy: Runs}},
	)
}
