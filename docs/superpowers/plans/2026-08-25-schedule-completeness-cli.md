# LACCD Class Search CLI Completeness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make whole-subject CLI searches discover and return every scheduled course without mixed-term sample rows or falsely complete local mirrors.

**Architecture:** Add an injectable discovery boundary backed by LACCD's official class-schedule JSON service. Use its normalized catalog list to drive existing catalog-scoped PeopleSoft requests, persist an additive completeness marker, and forbid partial sync persistence.

**Tech Stack:** Go 1.26.5, Cobra, `net/http`, `encoding/json`, pure-Go SQLite, `httptest`, golangci-lint.

**Spec:** `docs/superpowers/specs/2026-08-25-schedule-completeness-design.md`

## Global Constraints

- Execute in an isolated worktree created with `superpowers:using-git-worktrees`; the primary checkout contains user-owned CCN/version changes.
- Base the worktree on commit `5ab6563` or a descendant containing the approved specification.
- Preserve existing JSON keys, direct `--catalog` behavior, and exit-code meanings `0/2/3/5/7/10`.
- Never merge the collapsed no-catalog PeopleSoft sample or persist a truncated/failed whole-subject sync.
- Add no credentials, CGO, or runtime dependency on the catalog CLI or chatbot.
- Record generated-tree intent in `.printing-press-patches/`.
- Use fixtures for tests; live calls are verification only.

## File Map

- Create `internal/laccd/schedule_discovery.go` and `_test.go`: discovery interface and official JSON adapter.
- Modify `internal/laccd/client.go`; create `search_scope_test.go`: complete fan-out.
- Modify `internal/store/store.go` and schema tests; create `scope_completeness_test.go`: versioned mirror markers.
- Modify `internal/cli/laccd_common.go`, `search.go`, and `sync.go`; create command regression tests.
- Create `internal/laccd/testdata/completeness/` and `tools/schedule-replay/main.go`: deterministic replay.
- Create incident/patch documentation; update `README.md`, `HANDOFF.md`, and `SKILL.md`.

---

### Task 1: Official Schedule Course Discovery

**Files:**
- Create: `internal/laccd/schedule_discovery.go`
- Create: `internal/laccd/schedule_discovery_test.go`

**Interfaces:**
- Produces: `CourseDiscovery.Catalogs(context.Context, string, string, string) ([]string, error)`.
- Produces: `NewScheduleAPIDiscovery(string, *http.Client, *cliutil.AdaptiveLimiter) CourseDiscovery`.
- Produces: `WithCourseDiscovery(CourseDiscovery) ClientOption`.

- [ ] **Step 1: Write failing discovery tests**

Cover numeric padding, alphanumeric uppercasing, normalized duplicate removal, deterministic sorting, wrong term, blank/unrelated course-bearing subject, empty offerings, malformed JSON, HTTP errors, and 429 typing. Include this exact success shape:

```go
func TestScheduleAPIDiscoveryCatalogsNormalizesAndSorts(t *testing.T) {
    body := `{"termname":"Fall 2026","subjects":[{"code":"MULTIMD","courses":[{"catalogNbr":" C1000 "},{"catalogNbr":"7"},{"catalogNbr":"007"},{"catalogNbr":"110"}]}]}`
    srv := jsonServer(t, http.StatusOK, body)
    d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))
    got, err := d.Catalogs(context.Background(), "LAMC", "2268", "multimd")
    if err != nil { t.Fatal(err) }
    if want := []string{"007", "110", "C1000"}; !slices.Equal(got, want) {
        t.Fatalf("Catalogs=%v want %v", got, want)
    }
}
```

- [ ] **Step 2: Run RED**

Run: `go test ./internal/laccd -run TestScheduleAPIDiscovery -count=1`

Expected: missing interface/constructor build failure.

- [ ] **Step 3: Implement discovery**

Implement:

```go
const DefaultScheduleAPIBaseURL = "https://services.laccd.edu/apps/api/classschedule"
type CourseDiscovery interface { Catalogs(context.Context, string, string, string) ([]string, error) }
type ClientOption func(*Client)
func WithCourseDiscovery(d CourseDiscovery) ClientOption { return func(c *Client) { c.discovery = d } }
```

Use escaped path segments, `url.Values` for `subjectlist`, an 8 MiB body limit, the supplied client/limiter, and the existing rate-limit error. Compare season/year tokens from `TermLabel(term)`. Normalize in the spec's trim → reject empty → numeric pad or uppercase → deduplicate → sort order.

- [ ] **Step 4: Run GREEN**

Run: `go test ./internal/laccd -run TestScheduleAPIDiscovery -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/laccd/schedule_discovery.go internal/laccd/schedule_discovery_test.go
git commit -m "feat: discover complete schedule catalogs"
```

### Task 2: Replace Collapsed-Sample Fan-out

**Files:**
- Modify: `internal/laccd/client.go`
- Create: `internal/laccd/search_scope_test.go`
- Create: `internal/laccd/testdata/completeness/discovery-multimd.json`
- Create: `internal/laccd/testdata/completeness/sections-100.html`
- Create: `internal/laccd/testdata/completeness/sections-110.html`
- Create: `internal/laccd/testdata/completeness/sections-125.html`

**Interfaces:**
- Consumes Task 1 discovery.
- Extends `ScopeResult` with `Catalogs []string` in normalized selected order.
- Preserves `SearchScope(ctx, college, term, subject, catalog string, maxCourses int) (ScopeResult, error)`.

- [ ] **Step 1: Write failing scope tests**

Use `fakeDiscovery` plus `httptest.Server`. Assert discovery catalogs `003,107,125` produce CINEMA 125 class numbers, a `SUMMER-ROW` present only on the collapsed fixture never appears, metadata is `3/3`, a positive bound truncates deterministically, negative bounds fail, every catalog/`ErrOverCap` failure aborts, and direct `catalog=125` never calls discovery.

```go
if diff := cmp.Diff([]string{"17490", "17491"}, onlyCatalogClassNbrs(got.Sections, "125")); diff != "" { t.Fatal(diff) }
if got.CoursesScanned != 3 || got.CoursesTotal != 3 || got.Truncated { t.Fatalf("bad metadata: %+v", got) }
```

- [ ] **Step 2: Run RED**

Run: `go test ./internal/laccd -run TestSearchScope -count=1`

Expected: omitted catalog and mixed-sample failures.

- [ ] **Step 3: Implement discovery-driven fan-out**

Change `New` to variadic options without breaking callers:

```go
func New(baseURL string, ratePerSec float64, timeout time.Duration, opts ...ClientOption) *Client
```

Default the discovery adapter, reject negative bounds, call discovery only for whole subjects, bound catalogs, call `SearchSections` once per selected catalog, abort on any error, deduplicate class numbers, sort, and never request/parse the no-catalog PeopleSoft page.

- [ ] **Step 4: Run GREEN**

Run: `go test ./internal/laccd -run 'TestSearchScope|TestParseSections|TestParseCourseCatalogs' -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/laccd/client.go internal/laccd/search_scope_test.go internal/laccd/testdata/completeness
git commit -m "fix: complete whole-subject schedule fan-out"
```

### Task 3: Version Complete Mirrors

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/schema_version_test.go`
- Create: `internal/store/scope_completeness_test.go`

**Interfaces:**
- Produces `ScopeCompleteness{Version int, DiscoverySource string, Catalogs []string, CoursesTotal int, CoursesScanned int, Truncated bool, SyncedAt time.Time}`.
- Produces `SaveScopeCompleteness(string, ScopeCompleteness) error` and `GetScopeCompleteness(string) (ScopeCompleteness, bool, error)`.
- Produces `SaveCompleteScheduleScope(scopeKey string, items []json.RawMessage, snapshots []SeatSnapshot, marker ScopeCompleteness) error`, one transaction for section rows, snapshots, and marker.

- [ ] **Step 1: Write failing migration/round-trip tests**

Assert additive columns `completeness_version`, `discovery_source`, `catalogs_json`, `courses_total`, `courses_scanned`, and `truncated`. Round-trip a marker containing a zero-section catalog; legacy rows and invalid/incomplete markers return `ok=false`.

- [ ] **Step 2: Run RED**

Run: `go test ./internal/store -run 'Test.*ScopeCompleteness|TestMigrate_AddsColumnsOnUpgrade_SyncState' -count=1`

Expected: missing columns/type/methods.

- [ ] **Step 3: Implement schema and marker methods**

Add columns to new-table DDL and `ensureColumn`. JSON-marshal `Catalogs`. A marker is valid only at version 2 with valid JSON, equal counts, and `truncated=false`; do not infer or delete legacy data. Extract the typed/generic section upsert statements used by `UpsertBatch` into transaction-scoped helpers, then make `SaveCompleteScheduleScope` hold `writeMu`, begin one transaction, upsert all section rows, insert snapshots, write the marker, and commit. Any statement failure rolls back all three.

```go
type ScopeCompleteness struct {
    Version int; DiscoverySource string; Catalogs []string
    CoursesTotal int; CoursesScanned int; Truncated bool; SyncedAt time.Time
}
func (s *Store) SaveCompleteScheduleScope(scopeKey string, items []json.RawMessage, snapshots []SeatSnapshot, marker ScopeCompleteness) error
```

- [ ] **Step 4: Run GREEN**

Run the Step 2 command; expect pass.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/schema_version_test.go internal/store/scope_completeness_test.go
git commit -m "feat: version complete schedule mirrors"
```

### Task 4: Enforce Completeness in Search and Sync

**Files:**
- Modify: `internal/cli/laccd_common.go`
- Modify: `internal/cli/search.go`
- Modify: `internal/cli/sync.go`
- Create: `internal/cli/schedule_completeness_test.go`

**Interfaces:**
- Produces `scheduleScopeKey(college, term, subject string) string`.
- Produces `readCompleteLocalScope(ctx context.Context, dbPath, college, term, subject string, maxCourses int) ([]laccd.Section, store.ScopeCompleteness, bool, error)`.

- [ ] **Step 1: Write failing command tests**

Add named tests for truncated-sync rollback, legacy local rejection with exit 10, auto fallback only to a version-2 complete mirror, negative bounds, unsupported term codes, and bounded local selection using `catalogs_json`. For catalogs `100,110,125` but rows only for `100,125`, a bound of 2 must return only `100` rows while reporting total 3, scanned 2, truncated true.

- [ ] **Step 2: Run RED**

Run: `go test ./internal/cli -run 'Test.*(Completeness|Truncated|LegacyMirror|BoundedLocal|NegativeMaxCourses)' -count=1`

Expected: old local/sync behavior fails assertions.

- [ ] **Step 3: Implement canonical scope metadata reads**

Use:

```go
func scheduleScopeKey(college, term, subject string) string {
    return "sections:" + strings.ToUpper(strings.TrimSpace(college)) + ":" + strings.TrimSpace(term) + ":" + strings.ToUpper(strings.TrimSpace(subject))
}
```

Explicit whole-subject local reads without a valid marker return `configErr`; auto fallback returns the original live error. Apply the bound to marker catalogs before filtering stored rows and populate search metadata from the marker.

- [ ] **Step 4: Make complete sync persistence atomic**

Reject `scope.Truncated` before persistence. Marshal sections and construct snapshots in `persistCompleteSections`, then call Task 3's `SaveCompleteScheduleScope`; on any failure, return non-zero and print no success envelope. Do not call the old multi-transaction `persistSections` path from whole-subject sync.

```go
func persistCompleteSections(st *store.Store, scopeKey string, secs []laccd.Section, marker store.ScopeCompleteness) error
```

- [ ] **Step 5: Run GREEN**

Run the Step 2 command; expect pass.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/laccd_common.go internal/cli/search.go internal/cli/sync.go internal/cli/schedule_completeness_test.go internal/store/store.go
git commit -m "fix: reject incomplete schedule mirrors"
```

### Task 5: Deterministic CLI Replay

**Files:**
- Create: `tools/schedule-replay/main.go`
- Create: `internal/laccd/testdata/completeness/replay-manifest.json`
- Modify: `internal/laccd/search_scope_test.go`

**Interfaces:**
- Produces: `go run ./tools/schedule-replay --fixtures <dir> --college LAMC --term 2268 --subject MULTIMD`.
- Produces JSON keys `fixture_hashes`, `catalogs`, and `class_numbers`.

- [ ] **Step 1: Write a failing replay-manifest test**

Load the manifest, verify all SHA-256 hashes, serve JSON/HTML fixtures locally, run `SearchScope`, and assert the exact catalog/class-number oracle.

- [ ] **Step 2: Run RED**

Run: `go test ./internal/laccd -run TestCompletenessReplayManifest -count=1`

Expected: missing manifest/verifier failure.

- [ ] **Step 3: Implement the offline verifier**

The tool must use the same replay helper and `SearchScope`, make no network calls, print sorted sets/hashes, and fail on hash mismatch, missing catalog HTML, or oracle mismatch.

```go
type replayReport struct {
    FixtureHashes map[string]string `json:"fixture_hashes"`
    Catalogs []string `json:"catalogs"`
    ClassNumbers []string `json:"class_numbers"`
}
```

- [ ] **Step 4: Run GREEN**

Run the produced command; expect exit 0 and exact manifest output.

- [ ] **Step 5: Commit**

```bash
git add tools/schedule-replay internal/laccd/testdata/completeness internal/laccd/search_scope_test.go
git commit -m "test: add schedule completeness replay"
```

### Task 6: Incident and Agent Documentation

**Files:**
- Create: `docs/incidents/2026-08-25-whole-subject-schedule-completeness.md`
- Modify: `README.md`, `HANDOFF.md`, `SKILL.md`
- Create: `.printing-press-patches/whole-subject-schedule-completeness.json`

- [ ] **Step 1: Write the incident record**

Include reproduction, impact, root cause, fixed flow, mirror marker, compatibility, deterministic/live evidence distinction, and exact test commands.

- [ ] **Step 2: Correct user/agent docs and patch sidecar**

Document official discovery → PeopleSoft fan-out, explicit bounds, `0` unbounded, legacy resync, and degraded direct-catalog behavior. The Printing Press sidecar JSON must name the generated files, required behavior, and regression tests.

- [ ] **Step 3: Validate**

```bash
jq empty .printing-press-patches/whole-subject-schedule-completeness.json
rg -n "discovery|completeness_version|--max-courses" README.md HANDOFF.md SKILL.md docs/incidents/2026-08-25-whole-subject-schedule-completeness.md
git diff --check
```

Expected: valid JSON, concepts present, no whitespace errors.

- [ ] **Step 4: Commit**

```bash
git add docs/incidents README.md HANDOFF.md SKILL.md .printing-press-patches/whole-subject-schedule-completeness.json
git commit -m "docs: record schedule completeness incident"
```

### Task 7: CLI Verification Gate

**Files:** Verify only; repair failures in the owning task.

- [ ] **Step 1: Format and inspect**

```bash
gofmt -w internal/laccd internal/store internal/cli tools/schedule-replay
git diff --check
git status --short
```

Expected: only planned files differ.

- [ ] **Step 2: Run full tests, lint, and builds**

```bash
go test ./... -count=1
golangci-lint run
go build -o bin/laccd-class-search-pp-cli ./cmd/laccd-class-search-pp-cli
go build -o bin/laccd-class-search-pp-mcp ./cmd/laccd-class-search-pp-mcp
```

Expected: all exit 0; evaluate every skip.

- [ ] **Step 3: Run deterministic replay**

Run Task 5's command and record stdout plus `git rev-parse HEAD` for the chatbot plan.

- [ ] **Step 4: Run fresh live acceptance**

Use the built binary with `--data-source live --no-cache --max-courses 0` for MULTIMD, CINEMA, and PHOTO at LAMC `2268`. Record UTC time, revision, raw JSON hashes, normalized class-number sets, and direct PeopleSoft spot checks. If live data changed, record the new oracle instead of forcing historical counts.
