package docs

// Catalog is the hand-written half of the agent map: one row per directory
// that the live tree is allowed to contain at the mapped layer. internal/docs
// walks the tree, fills purpose/guard/command from this table, and writes
// AGENTS.md pages. A test guard fails when the pages drift, when a mapped
// directory appears that has no row, or when a row names a directory that is gone.
//
// Adding a directory therefore costs a row here and `make map`. Forgetting
// either is a red test, not a silent hole in the map.

// DefaultCatalog is the master catalog for nova-tools.
var DefaultCatalog = []Entry{
	E(".github", "CI workflows and automation", "go test ./internal/ci", "make test"),
	E("assets", "static assets and schemas", "none", "none"),
	Page("cmd", "22 nova command-line tools", "go test ./cmd/...", "make build"),
	Page("docs", "specs, guides, and proposals", "go test ./internal/docs", "go test ./internal/docs"),
	E("infra", "runner images and scripts", "none", "none"),
	Page("internal", "packages and libraries", "go test ./internal/...", "make test"),
	E("lisp", "nova-work lisp kernel", "go test ./internal/ci", "make test-lisp"),
	E("profiles", "swarm worker profiles", "go test ./internal/swarm", "nova-swarm lint"),
	E("scripts", "maintenance and operational scripts", "none", "none"),
	E("testdata", "shared test fixtures and data", "go test ./internal/ci", "make test"),
	Page("tools", "developer and bench tools", "go test ./tools/...", "make map"),

	// cmd/
	E("cmd/nova-board", "board viewer and coordinator CLI", "go test ./cmd/nova-board", "go test ./cmd/nova-board"),
	E("cmd/nova-bus", "coordination bus inbox, send, and wait CLI", "go test ./cmd/nova-bus", "go test ./cmd/nova-bus"),
	E("cmd/nova-cairn", "dusk memory distillation CLI", "go test ./cmd/nova-cairn", "go test ./cmd/nova-cairn"),
	E("cmd/nova-check", "codebase hygiene and constraint check CLI", "go test ./cmd/nova-check", "go test ./cmd/nova-check"),
	E("cmd/nova-ci", "CI slowtests budget and check CLI", "go test ./cmd/nova-ci", "go test ./cmd/nova-ci"),
	E("cmd/nova-decide", "criteria evaluation and decision engine CLI", "go test ./cmd/nova-decide", "go test ./cmd/nova-decide"),
	E("cmd/nova-fuse", "workspace isolation and boundary CLI", "go test ./cmd/nova-fuse", "go test ./cmd/nova-fuse"),
	E("cmd/nova-memory", "memory indexing and search CLI", "go test ./cmd/nova-memory", "go test ./cmd/nova-memory"),
	E("cmd/nova-merge", "batch merge and queue landing CLI", "go test ./cmd/nova-merge", "go test ./cmd/nova-merge"),
	E("cmd/nova-play", "sandboxed evaluation CLI", "go test ./cmd/nova-play", "go test ./cmd/nova-play"),
	E("cmd/nova-post", "PR and issue posting CLI", "go test ./cmd/nova-post", "go test ./cmd/nova-post"),
	E("cmd/nova-pulse", "sprint telemetry and health CLI", "go test ./cmd/nova-pulse", "go test ./cmd/nova-pulse"),
	E("cmd/nova-review", "review packet and verdict CLI", "go test ./cmd/nova-review", "go test ./cmd/nova-review"),
	E("cmd/nova-sandbox", "OS-level process sandbox CLI", "go test ./cmd/nova-sandbox", "go test ./cmd/nova-sandbox"),
	E("cmd/nova-secrets", "zero-leak secrets store CLI", "go test ./cmd/nova-secrets", "go test ./cmd/nova-secrets"),
	E("cmd/nova-self-talk", "internal dialogue recording CLI", "go test ./cmd/nova-self-talk", "go test ./cmd/nova-self-talk"),
	E("cmd/nova-swarm", "multi-agent worker pool CLI", "go test ./cmd/nova-swarm", "go test ./cmd/nova-swarm"),
	E("cmd/nova-tokens", "token consumption metering and budgeting CLI", "go test ./cmd/nova-tokens", "go test ./cmd/nova-tokens"),
	E("cmd/nova-update", "binary release update CLI", "go test ./cmd/nova-update", "go test ./cmd/nova-update"),
	E("cmd/nova-version", "build identity and version CLI", "go test ./cmd/nova-version", "go test ./cmd/nova-version"),
	E("cmd/nova-wake", "worker wakeup and slot lease CLI", "go test ./cmd/nova-wake", "go test ./cmd/nova-wake"),
	E("cmd/nova-work", "work item execution and lifecycle CLI", "go test ./cmd/nova-work", "go test ./cmd/nova-work"),

	// internal/
	E("internal/board", "board structures and view rendering", "go test ./internal/board", "go test ./internal/board"),
	E("internal/bounded", "bounded readers and byte buffers", "go test ./internal/bounded", "go test ./internal/bounded"),
	E("internal/buildinfo", "binary identity and version info", "go test ./internal/buildinfo", "go test ./internal/buildinfo"),
	E("internal/bus", "append-only coordination bus", "go test ./internal/bus", "go test ./internal/bus"),
	E("internal/cairn", "memory distillation and cairn builder", "go test ./internal/cairn", "go test ./internal/cairn"),
	E("internal/chat", "friend conversation protocol", "go test ./internal/chat", "go test ./internal/chat"),
	E("internal/check", "hygiene rules and tree checkers", "go test ./internal/check", "go test ./internal/check"),
	E("internal/ci", "class tests and CI budget invariants", "go test ./internal/ci", "go test ./internal/ci"),
	E("internal/converge", "convergence state and progress math", "go test ./internal/converge", "go test ./internal/converge"),
	E("internal/decide", "criteria evaluation and decisions", "go test ./internal/decide", "go test ./internal/decide"),
	E("internal/dispatch", "command dispatch and runner interface", "go test ./internal/dispatch", "go test ./internal/dispatch"),
	E("internal/docs", "documentation guards and map generator", "go test ./internal/docs", "go test ./internal/docs"),
	E("internal/dogfood", "dogfood self-test gates", "go test ./internal/dogfood", "go test ./internal/dogfood"),
	E("internal/fleet", "runner fleet discovery and status", "go test ./internal/fleet", "go test ./internal/fleet"),
	E("internal/friends", "friend registry and signatures", "go test ./internal/friends", "go test ./internal/friends"),
	E("internal/fuse", "workspace isolation boundaries", "go test ./internal/fuse", "go test ./internal/fuse"),
	E("internal/goenv", "Go environment scrubber for child processes", "go test ./internal/goenv", "go test ./internal/goenv"),
	E("internal/harvest", "card result harvester and aggregation", "go test ./internal/harvest", "go test ./internal/harvest"),
	E("internal/hygiene", "clean checkout and leak assertions", "go test ./internal/hygiene", "go test ./internal/hygiene"),
	E("internal/jobs", "background job queues and state", "go test ./internal/jobs", "go test ./internal/jobs"),
	E("internal/keyshape", "cryptographic key format verification", "go test ./internal/keyshape", "go test ./internal/keyshape"),
	E("internal/lanes", "lane cursor and dispatch isolation", "go test ./internal/lanes", "go test ./internal/lanes"),
	E("internal/lifecycle", "sprint and worker lifecycle states", "go test ./internal/lifecycle", "go test ./internal/lifecycle"),
	E("internal/log", "structured logging helpers", "go test ./internal/log", "go test ./internal/log"),
	E("internal/memindex", "memory vector and text index", "go test ./internal/memindex", "go test ./internal/memindex"),
	E("internal/merge", "batch merge queue and land operations", "go test ./internal/merge", "go test ./internal/merge"),
	E("internal/onboarding", "onboarding banner and doc verifier", "go test ./internal/onboarding", "go test ./internal/onboarding"),
	E("internal/oneline", "single-line log and output grammar", "go test ./internal/oneline", "go test ./internal/oneline"),
	E("internal/outbound", "outbound webhook dispatcher", "go test ./internal/outbound", "go test ./internal/outbound"),
	E("internal/play", "sandboxed code experiment runner", "go test ./internal/play", "go test ./internal/play"),
	E("internal/post", "GitHub PR and issue client", "go test ./internal/post", "go test ./internal/post"),
	E("internal/pulse", "pulse telemetry engine and keeper", "go test ./internal/pulse", "go test ./internal/pulse"),
	E("internal/record", "decision and execution records", "go test ./internal/record", "go test ./internal/record"),
	E("internal/records", "database models and record formats", "go test ./internal/records", "go test ./internal/records"),
	E("internal/redisq", "Redis transport queue", "go test ./internal/redisq", "go test ./internal/redisq"),
	E("internal/release", "release packaging and manifest gates", "go test ./internal/release", "go test ./internal/release"),
	E("internal/review", "peer review verdict parser and evaluator", "go test ./internal/review", "go test ./internal/review"),
	E("internal/safepath", "path sanitization and sandboxing", "go test ./internal/safepath", "go test ./internal/safepath"),
	E("internal/sandbox", "OS isolation primitives (seatbelt/landlock)", "go test ./internal/sandbox", "go test ./internal/sandbox"),
	E("internal/secrets", "zero-leak memory and file vault", "go test ./internal/secrets", "go test ./internal/secrets"),
	E("internal/selftalk", "agent self-talk journal stream", "go test ./internal/selftalk", "go test ./internal/selftalk"),
	E("internal/specwork", "worklang spec compliance checks", "go test ./internal/specwork", "go test ./internal/specwork"),
	E("internal/swarm", "swarm worker pool and execution engine", "go test ./internal/swarm", "go test ./internal/swarm"),
	E("internal/testguard", "host seam and leak interception", "go test ./internal/testguard", "go test ./internal/testguard"),
	E("internal/tokens", "token counter and budget tracker", "go test ./internal/tokens", "go test ./internal/tokens"),
	E("internal/update", "binary updater and checksum verifier", "go test ./internal/update", "go test ./internal/update"),
	E("internal/wake", "slot leases and heartbeat monitors", "go test ./internal/wake", "go test ./internal/wake"),
	E("internal/workclient", "client bindings for nova-work daemon", "go test ./internal/workclient", "go test ./internal/workclient"),
	E("internal/worklang", "worklang s-expression evaluator", "go test ./internal/worklang", "go test ./internal/worklang"),

	// docs/
	E("docs/decide", "decision criteria and evaluation records", "go test ./internal/docs", "go test ./internal/docs"),
	E("docs/drafts", "in-flight design drafts and proposals", "go test ./internal/docs", "go test ./internal/docs"),
	E("docs/fixtures", "doc examples and test fixtures", "go test ./internal/docs", "go test ./internal/docs"),
	E("docs/roadmaps", "sprint roadmaps and milestones", "go test ./internal/docs", "go test ./internal/docs"),
	E("docs/schemas", "event and payload schema definitions", "go test ./internal/docs", "go test ./internal/docs"),
	E("docs/spec-pulse", "pulse sprint telemetry specifications", "go test ./internal/docs", "go test ./internal/docs"),

	// tools/
	E("tools/agentsmap", "AGENTS.md map generator CLI", "go test ./internal/docs", "make map"),
	E("tools/ci", "CI helper and build scripts", "go test ./internal/ci", "make test"),
	E("tools/testdur", "test duration analyzer and budget checker", "go test ./tools/testdur", "go test ./tools/testdur"),
}
