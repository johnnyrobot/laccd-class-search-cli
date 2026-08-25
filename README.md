# LACCD Class Search CLI

**Search every Los Angeles Community College District class from your terminal, with a local SQLite mirror, offline full-text search, cross-college comparison, and free unlimited seat-watch.**

A scriptable, agent-native CLI over the LACCD MyCollege guest class-search portal. Sync any college, term, and subject into a local database, then search and filter offline with `--json` output. Beyond the portal: `compare-colleges` finds an open seat for the same course across all 9 colleges, `watch` tracks a section's seats over time, `ccn` bridges the new AB1111 course renumbering, and `schedule build` checks a set of sections for time conflicts.

Learn more at [LACCD Class Search](https://mycollege-guest.laccd.edu).

Created by [@johnnyrobot](https://github.com/johnnyrobot).

## Install

`laccd-class-search-pp-cli` is all you need. The separate `laccd-class-search-pp-mcp` server is only for chat apps that can't run shell commands — see [Use with Claude Desktop](#use-with-claude-desktop).

### Go install

Requires Go 1.26.4 or newer:

```bash
go install github.com/johnnyrobot/laccd-class-search-cli/cmd/laccd-class-search-pp-cli@latest
```

To also install the MCP server binary:

```bash
go install github.com/johnnyrobot/laccd-class-search-cli/cmd/laccd-class-search-pp-mcp@latest
```

### Pre-built binary

Download a binary for your platform from the [latest release](https://github.com/johnnyrobot/laccd-class-search-cli/releases/latest).

On macOS, clear the Gatekeeper quarantine after downloading:

```bash
xattr -d com.apple.quarantine laccd-class-search-pp-cli
```

On Unix, mark it executable:

```bash
chmod +x laccd-class-search-pp-cli
```

### From source

```bash
git clone https://github.com/johnnyrobot/laccd-class-search-cli.git
cd laccd-class-search-cli
make build          # or: go build ./cmd/laccd-class-search-pp-cli
make build-all      # CLI + MCP server
```

### Agent skill

`SKILL.md` in this repo is the agent skill. Point your agent at it directly, or copy it into your agent's skills directory (for Claude Code: `~/.claude/skills/pp-laccd-class-search/SKILL.md`).

## Use with Claude Desktop

Claude Desktop has no shell, so it can't invoke the CLI. For that, `laccd-class-search-pp-mcp` bridges the gap: a thin MCP server that shells out to the same CLI binary on your behalf.

Install the MCP binary:

```bash
go install github.com/johnnyrobot/laccd-class-search-cli/cmd/laccd-class-search-pp-mcp@latest
```

Add to your Claude Desktop config (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "laccd-class-search": {
      "command": "laccd-class-search-pp-mcp"
    }
  }
}
```


## Authentication

No login or API key. The portal is the public guest class search; the CLI establishes a PeopleSoft guest session automatically (a cookie warm-up) on each run.

## Quick Start

```bash
# confirm the portal is reachable and the session warm-up works
laccd-class-search-pp-cli doctor --dry-run

# see the 9 college codes (LAPC, ELAC, LACC, ...)
laccd-class-search-pp-cli colleges

# list the currently-active term codes (e.g. 2268 = Fall 2026)
laccd-class-search-pp-cli terms --college LAPC

# find open MATH sections at Pierce for Fall 2026
laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --open

# exact seats and waitlist for one section
laccd-class-search-pp-cli section 13079

```

## Unique Features

These capabilities aren't available in any other tool for this API.

### Cross-college intelligence
- **`compare-colleges`** — Compare one course across all 9 LACCD colleges and see open-seat counts side by side.

  _Reach for this when a course is full at one college and you need to find any LACCD college that still has an open seat._

  ```bash
  laccd-class-search-pp-cli compare-colleges --subject MATH --catalog 261 --term 2268 --agent
  ```
- **`ccn`** — Resolve a legacy catalog number to its new AB1111 Common Course Number (and back), then list sections under both.

  _Reach for this when a course number returns nothing because it was renumbered under California's Common Course Numbering._

  ```bash
  laccd-class-search-pp-cli ccn MATH 261 --college LAPC --term 2268 --agent
  ```

### Seat availability
- **`watch`** — Fetch a section's current seats and waitlist and show the change since the last check.

  _Reach for this to monitor whether a specific section is gaining or losing open seats during a registration window._

  ```bash
  laccd-class-search-pp-cli watch 13079 --college LAPC --term 2268 --subject MATH --agent
  ```
- **`changed`** — Diff the current mirror against the previous sync to list new sections, dropped sections, and open-to-closed flips.

  _Reach for this to see what moved in the schedule since you last looked, without re-reading every section._

  ```bash
  laccd-class-search-pp-cli changed --since 24h --agent
  ```

### Schedule planning
- **`schedule build`** — Given a set of class numbers, report the conflict-free combination or the exact overlapping pairs.

  _Reach for this to validate that a candidate set of sections actually fits together before registering._

  ```bash
  laccd-class-search-pp-cli schedule build 13079 14512 15003 --agent
  ```

## Recipes


### Find an open seat for a full course anywhere in the district

```bash
laccd-class-search-pp-cli compare-colleges --subject MATH --catalog 261 --term 2268
```

Shows every LACCD college offering MATH 261 this term with open-seat counts side by side.

### Narrow a large result set with agent-native field selection

```bash
laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --agent --select sections.class_nbr,sections.section,sections.status,sections.days_times,sections.instructor
```

Returns only the fields an agent needs from a deeply nested section list, instead of the full payload.

### Watch a section for an opening

```bash
laccd-class-search-pp-cli watch 13079 --agent
```

Fetches current seats/waitlist for class number 13079 and reports the change since the last check.

### Check a candidate schedule for time conflicts

```bash
laccd-class-search-pp-cli schedule build 13079 14512 15003
```

Reports whether the three sections fit together or names the overlapping pairs.

### See what changed in the schedule since yesterday

```bash
laccd-class-search-pp-cli changed --since 24h
```

Lists new sections, dropped sections, and open-to-closed flips since the previous sync.

### Whole-subject completeness and bounds

When `search` or `sync` omits `--catalog`, the CLI first asks the official
LACCD schedule service for the complete catalog list for the college, term, and
subject. It then fans out one catalog-scoped PeopleSoft request per discovered
course. The collapsed no-catalog PeopleSoft sample is never merged, because it
can omit courses and can contain rows from a different term.

Use `--max-courses N` to make the fan-out explicit and reproducible. A positive
`N` scans the first `N` normalized catalog numbers in deterministic order and
marks the result `truncated` when more courses remain. `--max-courses 0` means
no bound (scan every discovered catalog). A negative value is a usage error.

Whole-subject syncs persist a version-2 completeness marker with
`discovery_source=laccd-schedule-api`, the authoritative catalog order, total
and scanned counts, and `truncated=false`. `--data-source local` and `auto`
only treat such a marker as a complete whole-subject mirror. A legacy mirror
without the marker is retained for explicit `--catalog` reads, but whole-subject
local reads require a resync:

```bash
laccd-class-search-pp-cli sync --college LAPC --term 2268 --subject MATH --max-courses 0
```

With `--catalog`, discovery is intentionally bypassed and the existing single
catalog-scoped PeopleSoft request remains available. This direct-catalog path
is useful when discovery is unavailable and preserves access to legacy mirror
rows; it does not certify the whole subject as complete.

## Usage

Run `laccd-class-search-pp-cli --help` for the full command reference and flag list.

## Paths & environment variables

This CLI separates local files into four path kinds:

| Kind | Contents |
|------|----------|
| `config` | User-editable settings such as `config.toml` and saved profiles |
| `data` | Durable local data such as `data.db` |
| `state` | Runtime state such as persisted queries, jobs, and `teach.log` |
| `cache` | Regenerable HTTP/cache files |

Each kind resolves independently. The ladder is:

1. Per-kind env var: `LACCD_CLASS_SEARCH_CONFIG_DIR`, `LACCD_CLASS_SEARCH_DATA_DIR`, `LACCD_CLASS_SEARCH_STATE_DIR`, or `LACCD_CLASS_SEARCH_CACHE_DIR`
2. `--home <dir>` for this invocation
3. `LACCD_CLASS_SEARCH_HOME` for a flat relocated root
4. XDG env vars: `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`
5. Platform defaults matching existing installs

For containers and agent sandboxes, prefer a single relocated root:

```bash
export LACCD_CLASS_SEARCH_HOME=/srv/laccd-class-search
laccd-class-search-pp-cli doctor
```

Under `LACCD_CLASS_SEARCH_HOME=/srv/laccd-class-search`, the four dirs resolve to `/srv/laccd-class-search/config`, `/srv/laccd-class-search/data`, `/srv/laccd-class-search/state`, and `/srv/laccd-class-search/cache`.

MCP servers do not receive CLI flags from the host. Put relocation in the host `env` block:

```json
{
  "mcpServers": {
    "laccd-class-search": {
      "command": "laccd-class-search-pp-mcp",
      "env": {
        "LACCD_CLASS_SEARCH_HOME": "/srv/laccd-class-search"
      }
    }
  }
}
```

Precedence matters in fleets: an ambient per-kind variable such as `LACCD_CLASS_SEARCH_DATA_DIR` overrides an explicit `--home` for that kind. Use `LACCD_CLASS_SEARCH_HOME` or the per-kind variables for durable fleet relocation; treat `--home` as the weaker per-invocation lever.

Relocation is one-way. Unsetting `LACCD_CLASS_SEARCH_HOME` does not move files back to platform defaults, and `doctor` cannot find files left under a former root. Move the files manually before unsetting relocation variables.

Existing installs keep working because the platform-default rung matches the legacy layout. Run `laccd-class-search-pp-cli doctor --fail-on warn` to check path warnings in automation.

## Commands

### search

Search LACCD class sections by college, term, and subject, with offline mirror, filters, and cross-college comparison.

- **`laccd-class-search-pp-cli search`** - Search class sections by college, term, and subject


## Output Formats

```bash
# Human-readable table (default in terminal, JSON when piped)
laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH

# JSON for scripting and agents
laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --json

# Filter to specific fields
laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --json --select id,name,status

# Dry run — show the request without sending
laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --dry-run

# Agent mode — JSON + compact + no prompts in one flag
laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --agent
```

## Agent Usage

This CLI is designed for AI agent consumption:

- **Non-interactive** - never prompts, every input is a flag
- **Pipeable** - `--json` output to stdout, errors to stderr
- **Filterable** - `--select id,name` returns only fields you need
- **Previewable** - `--dry-run` shows the request without sending
- **Read-only by default** - this CLI does not create, update, delete, publish, send, or mutate remote resources
- **Offline-friendly** - sync/search commands can use the local SQLite store when available
- **Agent-safe by default** - no colors or formatting unless `--human-friendly` is set

Exit codes: `0` success, `2` usage error, `3` not found, `5` API error, `7` rate limited, `10` config error.

## Health Check

```bash
laccd-class-search-pp-cli doctor
```

Verifies configuration and connectivity to the API.

## Configuration

Run `laccd-class-search-pp-cli doctor` to see the resolved config, data, state, and cache directories. The platform-default config path is `~/.config/laccd-class-search-pp-cli/config.toml`; `--home`, `LACCD_CLASS_SEARCH_HOME`, and per-kind env vars can relocate it.

Static request headers can be configured under `headers`; per-command header overrides take precedence.

## Troubleshooting
**Not found errors (exit code 3)**
- Check the resource ID is correct
- Run the `list` command to see available items

### API-specific
- **Search returns nothing for a course you know exists** — The course may have been renumbered under AB1111 — run `laccd-class-search-pp-cli ccn <subject> <number>` to find both the legacy and Common Course Number.
- **'session expired' or a redirect to a cookie page** — The PeopleSoft guest session timed out; just re-run the command — the client re-establishes the session automatically.
- **A term code is rejected** — Only currently-active terms are searchable; run `laccd-class-search-pp-cli terms --college <code>` to list valid term codes.

## Sources & Inspiration

This CLI was built by studying these projects and resources:

- [**OpenCourseSearch-API**](https://github.com/abstrctn/OpenCourseSearch-API) — Python
- [**find-staff**](https://github.com/jaredfrees/find-staff) — Python
- [**ScheduleStorm-Server**](https://github.com/Step7750/ScheduleStorm-Server) — Python

Generated by [CLI Printing Press](https://github.com/mvanhorn/cli-printing-press)
