# AI Agent Prompts

This directory contains self-contained implementation prompts for each development phase.
Each prompt is designed to be given directly to an AI coding agent (such as GitHub Copilot
in agent mode) to implement that phase independently.

---

## Usage

Each `phaseN-*.prompt.md` file is a complete, self-contained specification. To use:

1. Open a new Copilot agent session
2. Paste (or reference) the prompt file contents
3. The agent will implement that phase in isolation

**Prerequisites:** Read the agent in phase order. Each phase depends on the outputs of
prior phases as described in the prompt. The repository must already contain the package
stubs (doc.go files) before running any agent.

---

## Phase order and dependencies

```
Phase 0: Repo setup            (no deps)
    └── Phase 1: Core DSL      (no deps)
            ├── Phase 2: Parser       (needs Phase 1 types)
            │       ├── Phase 3: Codegen   (needs Phase 2 IR)
            │       └── Phase 4: Artifacts (needs Phase 2 IR)
            └── Phase 5: Runtime      (needs Phase 1 types)
                    └── Phase 6: CLI  (needs Phases 3, 4)
                            └── Phase 7: Integration (needs Phases 5, 6)
                                    └── Phase 8: Docs
```

---

## Files

| File | Phase | Goal |
|------|-------|------|
| [phase1-dsl.prompt.md](phase1-dsl.prompt.md) | 1 | Core DSL package + SchemaIR types |
| [phase2-parser.prompt.md](phase2-parser.prompt.md) | 2 | AST-based schema parser |
| [phase3-codegen.prompt.md](phase3-codegen.prompt.md) | 3 | Go code generator |
| [phase4-artifacts.prompt.md](phase4-artifacts.prompt.md) | 4 | .env, yaml, JSON schema, docs generators |
| [phase5-runtime.prompt.md](phase5-runtime.prompt.md) | 5 | Runtime validators + IsLive watcher |
| [phase6-cli.prompt.md](phase6-cli.prompt.md) | 6 | cfgx CLI binary |
| [phase7-integration.prompt.md](phase7-integration.prompt.md) | 7 | Example project + e2e tests |

---

## General agent guidelines

When using these prompts, the agent should:

- **Not implement Phase N+1 while working on Phase N.** Each phase has a clear boundary.
- **Run `go test ./...` and `go vet ./...`** before marking a phase complete.
- **Use golden files for all generator tests** — input IR JSON → expected output text.
- **Never use `fmt.Sprintf` to construct Go source code** in the code generator; always use `text/template`.
- **Run `go/format.Source()` on all generated Go code** before writing to disk.
- **Follow the invariants** in `docs/design/schema-ir-spec.md`; parsers enforce them, generators assume them.
