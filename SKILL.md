---
name: pp-laccd-class-search
description: "Search every Los Angeles Community College District class from your terminal, with a local SQLite mirror, offline full-text search, cross-college comparison, and free unlimited seat-watch. Trigger phrases: `find open MATH classes at Pierce`, `is ENGL 101 open at ELAC this fall`, `compare CHEM 101 across the LA community colleges`, `watch LACCD class 13079 for an open seat`, `what LACCD sections fit my schedule`, `use laccd-class-search`, `run laccd-class-search`."
author: "johnnyrobot"
license: "Apache-2.0"
argument-hint: "<command> [args] | install cli|mcp"
allowed-tools: "Read Bash"
metadata:
  openclaw:
    requires:
      bins:
        - laccd-class-search-pp-cli
    install:
      - kind: go
        bins: [laccd-class-search-pp-cli]
        module: github.com/johnnyrobot/laccd-class-search-cli/cmd/laccd-class-search-pp-cli
---

# LACCD Class Search — Printing Press CLI

## Prerequisites: Install the CLI

This skill drives the `laccd-class-search-pp-cli` binary. **You must verify the CLI is installed before invoking any command from this skill.** If it is missing, install it first:

1. Install via Go (requires Go 1.26.4 or newer). This installs into `$GOPATH/bin` (default `$HOME/go/bin`):
   ```bash
   go install github.com/johnnyrobot/laccd-class-search-cli/cmd/laccd-class-search-pp-cli@latest
   ```
2. Verify: `laccd-class-search-pp-cli --version`
3. Ensure the install directory is on `$PATH` for the agent/runtime that will invoke this skill.

If Go is unavailable, download a pre-built binary from the [latest release](https://github.com/johnnyrobot/laccd-class-search-cli/releases/latest) and put it on `$PATH`.

If `--version` reports "command not found" after install, the runtime cannot see the binary directory on `$PATH`. Do not proceed with skill commands until verification succeeds.

A scriptable, agent-native CLI over the LACCD MyCollege guest class-search portal. Sync any college, term, and subject into a local database, then search and filter offline with `--json` output. Beyond the portal: `compare-colleges` finds an open seat for the same course across all 9 colleges, `watch` tracks a section's seats over time, `ccn` bridges the new AB1111 course renumbering, and `schedule build` checks a set of sections for time conflicts.

## When to Use This CLI

Use this CLI to browse, search, and compare the live LACCD class schedule across the district's 9 colleges without logging in. It is ideal for finding open sections of a specific course, comparing availability across colleges, checking exact seat and waitlist counts, tracking whether a section's seats are changing, and assembling a conflict-free schedule. It reads the public guest portal, so it works for prospective students, current students planning before they register, and counselors advising across colleges.

## Anti-triggers

Do not use this CLI for:
- Do not use this CLI to register for, add, drop, or waitlist a class — it is read-only guest search; enrollment requires the authenticated MyCollege student portal.
- Do not use this CLI for colleges outside the Los Angeles Community College District.
- Do not use this CLI for grades, transcripts, financial aid, or any logged-in student record — the guest portal exposes none of that.
- Do not use this CLI for professor ratings or reviews — it reports scheduled sections, not instructor quality.

## Unique Capabilities

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

## Command Reference

**search** — Search LACCD class sections by college, term, and subject

- `laccd-class-search-pp-cli search` — Search class sections by college, term, and subject


### Finding the right command

When you know what you want to do but not which command does it, ask the CLI directly:

```bash
laccd-class-search-pp-cli which "<capability in your own words>"
```

`which` resolves a natural-language capability query to the best matching command from this CLI's curated feature index. Exit code `0` means at least one match; exit code `2` means no confident match — fall back to `--help` or use a narrower query.

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

## Auth Setup

No login or API key. The portal is the public guest class search; the CLI establishes a PeopleSoft guest session automatically (a cookie warm-up) on each run.

Run `laccd-class-search-pp-cli doctor` to verify setup.

## Whole-subject completeness

For `search` and `sync` without `--catalog`, the CLI uses the official LACCD
schedule discovery service to enumerate every catalog offered for the requested
college, term, and subject. It then performs one catalog-scoped PeopleSoft
request per discovered catalog. Do not interpret the collapsed no-catalog
PeopleSoft sample as a complete subject result; the CLI deliberately does not
merge it.

`--max-courses N` is an explicit fan-out bound. A positive value scans the first
N normalized catalogs in deterministic order and reports `truncated=true` if
the subject has more catalogs. `--max-courses 0` means unbounded discovery and
fan-out. Negative values are usage errors. The command defaults are 30 for
`search` and 50 for `sync`; use 0 when a complete mirror is required.

Whole-subject local and automatic fallback reads require a valid version-2
mirror marker: `completeness_version=2`, nonnegative counts, a non-null
normalized, unique, sorted catalog list whose length equals `courses_total`,
equal total/scanned counts, `truncated=false`, and a sync timestamp. Complete
syncs exactly replace current scope rows while retaining seat history; local
reads take their marker and rows from one SQLite generation.
`discovery_source=laccd-schedule-api` is retained as provenance, not as an
additional exact-value validity gate. Older mirrors remain on disk and are
still usable for an explicit `--catalog` read, but a whole-subject local read
must resync:

```bash
laccd-class-search-pp-cli sync --college LAPC --term 2268 --subject MATH --max-courses 0
```

An explicit `--catalog` request bypasses discovery and retains the direct
catalog-scoped PeopleSoft request. This is the degraded, useful path when the
official discovery service is unavailable; it does not claim whole-subject
completeness.

## Agent Mode

Add `--agent` to any command. Expands to: `--json --compact --no-input --no-color --yes`.

- **Pipeable** — JSON on stdout, errors on stderr
- **Filterable** — `--select` keeps a subset of fields. Dotted paths descend into nested structures; arrays traverse element-wise. Critical for keeping context small on verbose APIs:

  ```bash
  laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --agent --select id,name,status
  ```
- **Previewable** — `--dry-run` shows the request without sending
- **Offline-friendly** — sync/search commands can use the local SQLite store when available
- **Non-interactive** — never prompts, every input is a flag
- **Read-only** — do not use this CLI for create, update, delete, publish, comment, upvote, invite, order, send, or other mutating requests

### Response envelope

Commands that read from the local store or the API wrap output in a provenance envelope:

```json
{
  "meta": {"source": "live" | "local", "synced_at": "...", "reason": "..."},
  "results": <data>
}
```

Parse `.results` for data and `.meta.source` to know whether it's live or local. A human-readable `N results (live)` summary is printed to stderr only when stdout is a terminal AND no machine-format flag (`--json`, `--csv`, `--compact`, `--quiet`, `--plain`, `--select`) is set — piped/agent consumers and explicit-format runs get pure JSON on stdout.

## Paths and state

Agents should treat the CLI's path resolver as part of the runtime contract:

- Use `--home <dir>` for one invocation, or set `LACCD_CLASS_SEARCH_HOME=<dir>` to relocate all four path kinds under one root.
- Use per-kind env vars only when a specific kind must diverge: `LACCD_CLASS_SEARCH_CONFIG_DIR`, `LACCD_CLASS_SEARCH_DATA_DIR`, `LACCD_CLASS_SEARCH_STATE_DIR`, `LACCD_CLASS_SEARCH_CACHE_DIR`.
- Resolution order is per-kind env var, `--home`, `LACCD_CLASS_SEARCH_HOME`, XDG (`XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`), then platform defaults.
- `config` contains settings like `config.toml` and profiles. `data` contains `credentials.toml`, `data.db`, cookies, and auth sidecars. `state` contains persisted queries, jobs, and `teach.log`. `cache` contains regenerable HTTP/cache files.
- Stored secrets live in `credentials.toml` under the data dir. Existing legacy `config.toml` secrets are read for compatibility and leave `config.toml` on the first auth write.
- Run `laccd-class-search-pp-cli doctor --fail-on warn` to surface path and credential-location warnings. `agent-context` exposes a schema v4 `paths` block for agents that need the resolved dirs.
- For MCP, pass relocation through the MCP host config. The MCP binary does not inherit CLI flags:

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

Fleet precedence: an inherited per-kind env var overrides an explicit `--home` for that kind. Use `LACCD_CLASS_SEARCH_HOME` or per-kind vars as durable fleet levers, and use `--home` only for a single invocation. Relocation is not reversible by unsetting env vars; move files manually before clearing `LACCD_CLASS_SEARCH_HOME`, or `doctor` will not find credentials left under the former root.

## Agent Feedback

When you (or the agent) notice something off about this CLI, record it:

```
laccd-class-search-pp-cli feedback "the --since flag is inclusive but docs say exclusive"
laccd-class-search-pp-cli feedback --stdin < notes.txt
laccd-class-search-pp-cli feedback list --json --limit 10
```

Entries are stored locally as `feedback.jsonl` under the resolved data dir. They are never POSTed unless `LACCD_CLASS_SEARCH_FEEDBACK_ENDPOINT` is set AND either `--send` is passed or `LACCD_CLASS_SEARCH_FEEDBACK_AUTO_SEND=true`. Default behavior is local-only.

Write what *surprised* you, not a bug report. Short, specific, one line: that is the part that compounds.

## Output Delivery

Every command accepts `--deliver <sink>`. The output goes to the named sink in addition to (or instead of) stdout, so agents can route command results without hand-piping. Three sinks are supported:

| Sink | Effect |
|------|--------|
| `stdout` | Default; write to stdout only |
| `file:<path>` | Atomically write output to `<path>` (tmp + rename) |
| `webhook:<url>` | POST the output body to the URL (`application/json` or `application/x-ndjson` when `--compact`) |

Unknown schemes are refused with a structured error naming the supported set. Webhook failures return non-zero and log the URL + HTTP status on stderr.

## Named Profiles

A profile is a saved set of flag values, reused across invocations. Use it when a scheduled agent calls the same command every run with the same configuration - HeyGen's "Beacon" pattern.

```
laccd-class-search-pp-cli profile save briefing --json
laccd-class-search-pp-cli --profile briefing search --college LAPC --term 2268 --subject MATH
laccd-class-search-pp-cli profile list --json
laccd-class-search-pp-cli profile show briefing
laccd-class-search-pp-cli profile delete briefing --yes
```

Explicit flags always win over profile values; profile values win over defaults. `agent-context` lists all available profiles under `available_profiles` so introspecting agents discover them at runtime.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 2 | Usage error (wrong arguments) |
| 3 | Resource not found |
| 5 | API error (upstream issue) |
| 7 | Rate limited (wait and retry) |
| 10 | Config error |

## Argument Parsing

Parse `$ARGUMENTS`:

1. **Empty, `help`, or `--help`** → show `laccd-class-search-pp-cli --help` output
2. **Starts with `install`** → ends with `mcp` → MCP installation; otherwise → see Prerequisites above
3. **Anything else** → Direct Use (execute as CLI command with `--agent`)

## MCP Server Installation

1. Install the MCP server:
   ```bash
   go install github.com/johnnyrobot/laccd-class-search-cli/cmd/laccd-class-search-pp-mcp@latest
   ```
2. Register with Claude Code:
   ```bash
   claude mcp add laccd-class-search-pp-mcp -- laccd-class-search-pp-mcp
   ```
3. Verify: `claude mcp list`

## Direct Use

1. Check if installed: `which laccd-class-search-pp-cli`
   If not found, offer to install (see Prerequisites at the top of this skill).
2. Match the user query to the best command from the Unique Capabilities and Command Reference above.
3. Execute with the `--agent` flag:
   ```bash
   laccd-class-search-pp-cli <command> [subcommand] [args] --agent
   ```
4. If ambiguous, drill into subcommand help: `laccd-class-search-pp-cli <command> --help`.
