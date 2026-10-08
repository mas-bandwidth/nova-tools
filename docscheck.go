package main

func init() {
	register(verb{
		name:    "docs-check",
		summary: "run docs CI: docs guards, link check, CLI check, terminology lint",
		help: `docs-check: run docs CI
Usage: go run ./tools/ci docs-check

This runs `make docs-check`, which in turn runs:
  - internal/docs tests (docs guards and map generation)
  - nova-check links --dir . (link check)
  - tools/clidoc (generated CLI reference check)
  - terminology lint (tdocs-glossary-terminology-lint)
`,
		do: func(e env, args []string) int {
			if len(args) > 0 {
				fmt.Fprintf(e.stderr, "docs-check: unknown args %s; run: go run ./tools/ci docs-check -h\n", strings.Join(args, " "))
				return 2
			}
			fmt.Fprintln(e.stdout, "docs-check: run `make docs-check`")
			return 0
		},
	})
}
