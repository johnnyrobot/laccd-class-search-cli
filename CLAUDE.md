# CLAUDE.md

Agent-native, read-only Go CLI over the LACCD MyCollege guest class-search portal (`mycollege-guest.laccd.edu`, PeopleSoft). Syncs sections into a local SQLite mirror, then searches/filters offline with `--json`. Adds cross-college seat comparison, seat-watch, AB1111 course-number bridging, and schedule conflict checks. This tree is **generated output** from [CLI Printing Press](https://github.com/mvanhorn/cli-printing-press).

## Architecture
- `cmd/laccd-class-search-pp-cli/main.go` — CLI entry point (Cobra).
- `cmd/laccd-class-search-pp-mcp/main.go` — MCP server entry point (same core, exposed as MCP tools).
- `internal/cli/` — one file per command: `search`, `sync`, `section`, `colleges`, `terms`, `subjects`, `compare_colleges`, `ccn`, `watch`, `changed`, `schedule_build`, `doctor`, `agent_context`, `which`, plus `helpers.go`/`laccd_common.go`.
- `internal/laccd/` — portal client (`client.go`), HTML/JSON parsing (`parse.go`). This is where the PeopleSoft guest-session warm-up and section parsing live.
- `internal/mcp/` — MCP tool definitions (`tools.go`) mirroring the CLI commands.
- `internal/cache/`, `internal/config/`, `internal/cliutil/`, `internal/types/` — SQLite/HTTP cache, TOML config, shared helpers (paths, rate limit, date/number parsing, fanout), shared structs.
- Data flow: command → `internal/laccd` client (guest session + fetch) → parse → local SQLite mirror (`data.db`) → filtered output. Offline commands (`compare-colleges`, `changed`, etc.) read the mirror.

## Commands
```bash
make build        # go build -o bin/laccd-class-search-pp-cli ./cmd/laccd-class-search-pp-cli
make build-all    # build both the CLI and the MCP binary
make test         # go test ./...
make lint         # golangci-lint run
make install      # go install ./cmd/laccd-class-search-pp-cli
# Runtime self-discovery (prefer over a hand-copied command list):
bin/laccd-class-search-pp-cli doctor --json
bin/laccd-class-search-pp-cli agent-context --pretty
bin/laccd-class-search-pp-cli which "<capability>" --json
```
Requires Go 1.26.4+ (see `go.mod` `toolchain`). Tests use in-repo fixtures; no network or SQLite CGO needed (`modernc.org/sqlite` is pure Go).

## Conventions
- Every command supports `--json`, `--select <dotted.paths>`, `--dry-run`, and `--agent` (JSON + compact + no color + non-interactive in one flag). Human table output is the TTY default; JSON when piped.
- Errors go to stderr; structured exit codes: `0` ok, `2` usage, `3` not found, `5` API error, `7` rate limited, `10` config error.
- Read-only tool: never creates/updates/deletes/publishes remote state.
- Path kinds (config/data/state/cache) resolve independently; relocate a fleet with `LACCD_CLASS_SEARCH_HOME` or per-kind `LACCD_CLASS_SEARCH_*_DIR` vars.

## Gotchas & Constraints
- **Generated tree.** Fix systemic issues upstream in Printing Press first; keep local edits narrow. A fresh print can overwrite the whole tree, so record any intentional local change under `.printing-press-patches/` or it will silently vanish on regen.
- **Never hand-bump the version.** Do not edit `var version = ...` or touch `CHANGELOG.md` for release bookkeeping — the `mvanhorn/printing-press-library` publish workflow stamps the `YYYY.M.N` release.
- No auth/API keys — the client establishes a PeopleSoft guest session (cookie warm-up) each run; a "session expired"/redirect just means re-run.
- Search returning nothing for a known course is usually AB1111 renumbering — use `ccn <subject> <number>`. Only currently-active terms are searchable (`terms --college <code>`).
- `.gitignore` excludes `*.db`, built binaries, `*.mcpb`, and `/.printing-press*.json` — do not commit local SQLite mirrors or binaries.
- See `README.md`/`SKILL.md` for product usage and `HANDOFF.md` for build history; keep this file invariant and small.
