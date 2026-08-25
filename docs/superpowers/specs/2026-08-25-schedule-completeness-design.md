# LACCD Schedule Completeness: CLI and Chatbot Design

**Date:** 2026-08-25  
**Status:** Approved design  
**Repositories:** `laccd-class-search-cli`, `laccd_chatbot`

## Summary

Whole-subject searches in `laccd-class-search-cli` can return an incomplete set
of courses while claiming the result is complete. The client currently derives
the course-number fan-out from a collapsed PeopleSoft sample page. That page
does not enumerate every offered course and can include rows from another active
term. For LAMC Fall 2026, a whole-subject CINEMA search omitted CINEMA 125 and a
MULTIMD search found only 2 of 10 offered course numbers.

The sibling `laccd_chatbot` has a separate completeness defect. It does not call
the CLI; it reads the official LACCD class-schedule JSON service directly. That
service returned all 10 LAMC Fall 2026 MULTIMD courses, but the chatbot's
`format_sections()` function ignored nested `relsections`, dropping 12 linked
LAB components. Voice output also limits detailed rendering without first
making the complete course coverage explicit.

The two fixes will share a correctness contract but no source-code dependency:

- The CLI will use the official LACCD schedule service to discover every course
  number in a campus/term/subject scope, then keep its existing catalog-scoped
  PeopleSoft requests for section parsing and status behavior.
- The chatbot will flatten primary sections and related components from its
  existing JSON response, preserving all class numbers while keeping voice
  responses concise and explicit about omitted detail.

## Evidence and Reproduction

### CLI defect

The current whole-subject path is:

1. Request the PeopleSoft result page without `catalogid`.
2. Parse course markers rendered in that collapsed sample.
3. Request each discovered catalog number.
4. Merge the collapsed sample rows into the final result.

The following differential check is red on version `1.0.0+dirty` built from
commit `78dbc98`:

```bash
laccd-class-search-pp-cli search \
  --college LAMC --term 2268 --subject CINEMA \
  --data-source live --no-cache --json

laccd-class-search-pp-cli search \
  --college LAMC --term 2268 --subject CINEMA --catalog 125 \
  --data-source live --no-cache --json
```

The first command omits class numbers `17490` and `17491`; the direct catalog
query returns both. The same failure hides MULTIMD 100, 220, 245, 300, 340,
500, 605, and 620, as well as PHOTO 015 and 121.

The collapsed Fall response also contains Summer rows. The current merge step
therefore pollutes the Fall result even when every catalog-scoped request is
term-correct.

### Chatbot defect

The official service endpoint:

```text
GET https://services.laccd.edu/apps/api/classschedule/listing/LAMC/2268?subjectlist=MULTIMD
```

returns 10 courses, 14 primary sections, and 12 nested related sections. The
chatbot's `backend/app/services/live_schedule.py::format_sections()` iterates
only each course's primary `sections`. The sequencing subsystem already walks
`relsections`, so two production paths interpret the same upstream schema
differently.

The text-chat formatter renders every normalized item it receives. The voice
formatter renders at most eight details and reports a remaining count, but it
does not first state the full distinct-course coverage. These are separate
issues: the missing labs are a data-normalization bug; the voice limit is a
presentation policy that must not imply incomplete discovery.

## Goals

1. A CLI whole-subject search discovers all scheduled catalog numbers for the
   requested campus, term, and subject before applying an explicit
   `--max-courses` bound.
2. The CLI never merges rows from the collapsed no-catalog PeopleSoft sample
   into an otherwise catalog-scoped result.
3. Direct `--catalog` searches retain their current behavior and output shape.
4. Completeness failures are explicit; no live path silently substitutes a
   partial sample and labels it complete.
5. Chatbot schedule normalization includes primary and related sections exactly
   once, preserving lecture/lab class numbers and meeting data.
6. Text and mobile consumers receive the complete normalized list.
7. Voice consumers receive complete counts and course coverage, with a clearly
   labeled cap on detailed section narration.
8. Existing uncommitted work in both checkouts remains untouched.

## Non-goals

- Registering, adding, dropping, or waitlisting a class.
- Changing seat-watch or section-detail semantics.
- Replacing the CLI's PeopleSoft detail/status path with the JSON schedule API.
- Making the chatbot invoke the CLI or creating a cross-language runtime
  dependency between the repositories.
- Deploying the chatbot as part of local implementation. Deployment requires a
  separately authorized task after revision-specific verification.
- Repairing unrelated subject-lookup pagination or CCN behavior.

## Decision

### Alternatives considered

1. **Official schedule discovery plus existing PeopleSoft fan-out — selected.**
   One JSON request supplies the complete scheduled catalog-number set. The
   current PeopleSoft catalog requests preserve full titles, normalized status,
   local-mirror behavior, and section-detail compatibility.
2. **Use the JSON schedule service for all CLI section data.** This is faster,
   but it changes titles, status spelling, meeting aggregation, and provenance
   in a shared output contract.
3. **Drive PeopleSoft's stateful expand/pagination controls.** This keeps one
   upstream, but relies on fragile ICAction state and browser-form internals for
   information already published by LACCD's schedule service.

The selected approach minimizes contract change while removing the false
completeness assumption.

## CLI Architecture

### Discovery boundary

Add a small internal course-discovery boundary in `internal/laccd`:

```go
type CourseDiscovery interface {
    Catalogs(ctx context.Context, college, term, subject string) ([]string, error)
}
```

The production implementation calls:

```text
https://services.laccd.edu/apps/api/classschedule/listing/{college}/{term}?subjectlist={subject}
```

It validates that:

- the response is valid JSON;
- `termname` contains exactly the same recognized season token and four-digit
  year as the existing `TermLabel()` contract for the requested term, regardless
  of whether the upstream string orders them as `Fall 2026` or `2026 Fall`;
- an unsupported term code that `TermLabel()` cannot interpret is rejected with
  a usage error before discovery rather than weakly validated;
- every subject entry containing courses has a non-empty code that matches the
  requested subject case-insensitively; blank or unrelated course-bearing
  entries are an API error rather than being filtered silently;
- every catalog number is non-empty;
- catalog normalization occurs before deduplication in this exact order: trim;
  reject an empty result; if every remaining rune is numeric, apply the existing
  zero-padding rule; otherwise uppercase the trimmed value; normalized
  collisions retain the first occurrence and log a warning;
- normalized duplicate catalog numbers are removed, and the normalized value
  is the value passed to PeopleSoft;
- results use deterministic catalog ordering: normalize pure numeric catalogs
  through the existing zero-padding rule, uppercase alphanumeric catalogs, then
  sort by that normalized string.

The subject-filtered endpoint may represent a valid zero-offering result as an
empty `subjects` array or as the requested subject with an empty `courses`
array. Either form is accepted only after term validation succeeds. A response
containing courses under an unrelated subject is rejected.

The discovery base URL must be injectable for offline HTTP tests. Production
uses the fixed official LACCD service URL. The existing PeopleSoft `base_url`
configuration continues to affect only PeopleSoft requests; it must not
silently redirect discovery to an HTML mock or third-party host.

### Whole-subject search flow

For a search without `--catalog`:

1. Discover the complete catalog-number list through `CourseDiscovery`.
2. Set `CoursesTotal` to the discovered count.
3. Apply `maxCourses` deterministically. A positive value bounds the fan-out;
   zero means no explicit bound.
4. Call the existing catalog-scoped `SearchSections` path for each selected
   course.
5. Deduplicate by class number and sort through the existing section sorter.
6. Do not parse or merge the collapsed no-catalog PeopleSoft page.
7. Set `CoursesScanned` and `Truncated` from the actual discovered set.

The command-level JSON keys remain unchanged:

```text
sections, count, source, scanned_courses, courses_total, truncated, note
```

The existing `search --max-courses 30` default and `sync --max-courses 50`
default remain for compatibility. If a subject exceeds the bound, the command
must set `truncated: true` and retain its explicit note. Callers that require
unbounded fan-out can pass `--max-courses 0`. Negative values are usage errors.

`CoursesScanned` means the number of catalog-scoped PeopleSoft requests that
completed successfully. A successful bounded result therefore has
`CoursesScanned == min(CoursesTotal, maxCourses)`; an unbounded successful
result has `CoursesScanned == CoursesTotal`. Failed live results emit no search
envelope, so a partial attempted count is never presented as usable output.

### Direct-catalog flow

When `--catalog` is supplied, the CLI must not call `CourseDiscovery`. It keeps
the existing one-course PeopleSoft request, `CoursesScanned=1`, and
`CoursesTotal=1`. This preserves a useful degraded path when the discovery
service is unavailable.

### Error and fallback behavior

- A malformed, term-mismatched, or unavailable discovery response is an API
  error, never an empty complete result.
- Any selected catalog request failure aborts the entire live whole-subject
  operation. The CLI must not skip that catalog, label the remainder complete,
  or persist the partial set.
- `--data-source live` exits with the existing API-error code.
- `--data-source auto` applies the same fallback rule to discovery failures and
  later catalog-request failures. It serves a locally complete mirror through
  the current fallback path and labels the result `source: local`; if no
  complete mirror is available, it returns the original live API error.
- A bounded local or `auto` fallback applies the same normalized catalog sort
  and first-N selection as live search. It reports the marker's complete
  `CoursesTotal`, the number selected as `CoursesScanned`, and `truncated=true`
  when the requested bound is smaller than the complete mirror.
- `sync` fails without replacing a known-good mirror with a partial scope. A
  bounded sync that would set `Truncated=true` is not persisted and returns a
  usage error instructing the caller to pass `--max-courses 0` or a sufficient
  higher bound.
- A discovery response containing zero scheduled courses is a successful empty
  result only when its term and subject envelope is valid.
- `ErrOverCap` from any selected catalog follows the same abort/no-persist rule
  as every other catalog failure. A direct-catalog call returns that error; a
  whole-subject call does not skip the affected course.
- Discovery uses the command's existing timeout and adaptive rate limiter, URL
  construction through `net/url`, an 8 MiB response bound, and JSON content
  validation. HTTP 429 remains a typed rate-limit error. No new retry loop is
  added in this change.

### Generated-tree preservation

The implementation is an intentional local change to a Printing Press output
tree. Add a `.printing-press-patches/` sidecar describing:

- the false assumption in generated whole-subject fan-out;
- the discovery interface and official endpoint;
- the rule forbidding collapsed-sample merging;
- the regression tests that must survive regeneration.

### Mirror completeness marker

Apply an additive SQLite schema migration to `sync_state`; stored section rows
and the `sections` table remain unchanged. Add nullable completeness columns and
write them on the existing scope-specific `resource_type` row for each
`(college, term, subject)` scope:

```text
completeness_version = 2
discovery_source = laccd-schedule-api
catalogs_json = [ordered normalized catalog values]
courses_total = N
courses_scanned = N
truncated = false
synced_at = timestamp
```

Only a marker with the current completeness version, equal total/scanned
counts, and `truncated=false` qualifies a whole-subject mirror for `auto` or
`local` complete-scope reads. Legacy mirrors without the marker remain on disk
and remain usable for explicit catalog-scoped reads, but a whole-subject read
must refuse to label them complete and print a resync hint. No migration infers
completeness from old rows because missing catalogs cannot be reconstructed from
those rows. An explicit whole-subject `--data-source local` read against an
absent or incomplete marker returns the existing configuration-error exit code
`10`; an `auto` fallback that encounters the same marker returns the original
live error instead.

`catalogs_json` is the authoritative normalized order captured at sync time,
including discovered catalogs that returned zero sections. Bounded local and
`auto` reads select the first N entries from this list before filtering stored
rows, so their catalog coverage and truncation metadata match the equivalent
live bound even when a selected course has no section row.

## Chatbot Architecture

### Shared section traversal

Introduce one reusable iterator in the backend services layer that yields each
course's primary sections and recursively yields related sections. It accepts a
schedule listing and yields `(course, section, primary_class_number)` tuples.

The iterator must:

- preserve upstream order: primary section followed by its related components;
- recurse defensively even though current related components have empty nested
  lists;
- carry the top-level primary class number through every descendant so
  `related_to` never changes meaning at deeper nesting levels;
- normalize a valid `NUMBER (COMPONENT)` value by extracting `NUMBER`; for any
  non-empty value that does not match that shape, preserve the entire trimmed
  raw value as the class number and leave `component` empty;
- retain the first record and log a warning when a normalized class number is
  duplicated within one course, including a primary/related collision;
- if a primary class number is completely blank, skip that primary and its
  entire related subtree with one warning because descendants cannot receive a
  valid primary ancestor; if only a related class number is blank, skip that
  related node with a warning but continue traversing any non-blank descendants
  using the original top-level primary ancestor;
- deduplicate within one course by normalized class number;
- avoid merging the same class number across different courses;
- tolerate absent or null `subjects`, `courses`, `sections`, and `relsections`.

Both `live_schedule.format_sections()` and the sequencing sweep must use this
shared traversal so the two paths cannot drift again.

### Normalized output

Every primary or related component produces the existing schedule keys:

```text
course_code, course_title, class_number, section, days, time, location,
instructor, dates, class_type
```

Add two backward-compatible keys:

- `component`: the parenthetical component parsed from `classNbr`, such as
  `LEC` or `LAB`, or an empty string when absent;
- `related_to`: the parent primary class number for a related component, or an
  empty string for a primary section.

Course-number and keyword filters apply at the course level before any primary
or related sections are emitted.

### Consumer behavior

- Text chat and the authenticated mobile schedule endpoint receive the complete
  normalized component list and accurate `total_sections`.
- Cross-campus totals count all components consistently.
- ZTC/LCT filtering applies independently to each component's `class_type`.
- Enrollment-planner consumers continue to receive one candidate per class
  number; related labs remain distinct candidates rather than being folded into
  their lectures.
- Voice output states the number of distinct courses and total components. It
  names every distinct course when there are 12 or fewer; above that, it names
  the first 12 and explicitly states how many course codes remain. Detailed
  section narration remains capped at eight and explicitly labels the number of
  additional components available through text or the official class search.
  Course disclosure follows first occurrence in the normalized section order.

## Compatibility Contract

| Surface | Compatibility requirement |
|---|---|
| CLI direct `--catalog` | No behavioral or JSON-shape change |
| CLI whole-subject JSON | Existing keys retained; course totals become accurate |
| CLI local mirror | Additive `sync_state` migration; section-row schema unchanged; only versioned-complete scopes satisfy whole-subject reads |
| CLI exit codes | Existing `0/2/3/5/7/10` meanings retained |
| Chatbot schedule dictionaries | Existing keys retained; two additive keys |
| Text/mobile schedule output | More complete valid rows; a blank identifier is skipped and exact duplicates collapse to the first row |
| Voice output | Same eight-detail limit; complete counts/course disclosure added |
| Sequencing cache | Same file schema; traversal implementation is shared |

## Test Strategy

### CLI tests

1. Add an HTTP fixture where the collapsed PeopleSoft page exposes CINEMA 003
   and 107 while discovery exposes 003, 107, and 125. Assert whole-subject
   output includes the direct 125 sections.
2. Add a MULTIMD fixture with 10 discovered catalogs and assert all catalog
   requests are made and all class numbers survive deduplication.
3. Include a cross-term row only in the collapsed page and assert it never
   reaches the result.
4. Assert `CoursesTotal`, `CoursesScanned`, `Truncated`, and `Note` under bounded
   and unbounded fan-out.
5. Assert a direct-catalog search performs no discovery request.
6. Assert discovery transport, JSON, and term-validation failures return an
   error instead of partial sections.
7. Assert an `auto` command can fall back to a populated local mirror without
   overwriting it, and that a missing mirror returns the original live error.
8. Assert one failing catalog request aborts the scope, emits no partial search
   envelope, and persists no partial rows.
9. Assert unrelated subject entries, malformed JSON, wrong term labels,
   negative `max-courses`, and duplicate discovery catalogs follow the defined
   validation rules.
10. Assert truncated syncs do not persist and legacy/incomplete mirror markers
    cannot satisfy a whole-subject local or `auto` read.
11. Assert `catalogs_json` retains zero-section catalogs and bounded local
    fallback selects the same first-N catalogs and metadata as live search.
12. Run `go test ./...` and the repository lint command.

### Chatbot tests

1. Reuse `tests/fixtures/lamc_2258_sample.json` to assert the shared iterator
   yields all 14 fixture components, including its eight related LAB rows.
2. Add a compact MULTIMD fixture proving `format_sections()` emits a lecture and
   linked lab with unique class numbers and correct `component`/`related_to`.
3. Assert course and keyword filtering retains related components for matched
   courses and excludes unmatched courses.
4. Assert duplicate related class numbers are emitted once per course, with the
   first row retained and a warning emitted.
5. Assert blank, malformed, and nested related class numbers follow the
   normalization and top-primary ancestry rules.
6. Assert text/mobile totals include related components.
7. Assert ZTC/LCT filtering uses each related component's own tags.
8. Assert voice output reports complete distinct-course and component counts,
   names all courses in a 10-course response, and labels its eight-detail cap.
9. Audit strict-schema consumers, including the mobile client, and add a
   contract test proving the two additive keys do not break deserialization.
10. Run the targeted schedule tests, then the repository's full backend test
   suite and configured lint/type checks.

### Deterministic cross-repository replay

Cross-repository set equality is proven against captured bytes, not against
separate live requests. The verification harness will:

1. Capture and hash the raw official schedule JSON for each acceptance subject.
2. Recursively extract its canonical catalog and class-number oracle.
3. Capture and hash the catalog-scoped PeopleSoft HTML for every oracle catalog.
4. Serve the captured JSON and HTML through local replay endpoints.
5. Point the CLI's injectable discovery URL and existing PeopleSoft `base_url`
   at those endpoints, then record its normalized class-number set.
6. Run chatbot normalization directly over the identical captured JSON bytes
   and record its normalized class-number set.
7. Require both application sets to equal the oracle and include fixture hashes
   in the verification report.

This replay is deterministic regression/revision evidence. It is followed by a
separate live smoke check, which demonstrates current upstream compatibility
but does not claim that independently issued requests consumed identical bytes.

### Live acceptance checks

Live checks are current-state evidence, not deterministic replay tests. Run the CLI
with `--no-cache`; run chatbot checks in a fresh process with its in-memory
listing cache cleared. Record exact commands, UTC timestamps, repository
revisions, raw-response SHA-256 hashes, and normalized class-number sets in a
verification report. Against LAMC Fall 2026 (`2268`):

- MULTIMD contains 10 course codes and 26 class components.
- CINEMA contains CINEMA 125 class numbers `17490` and `17491`.
- PHOTO contains PHOTO 015 and PHOTO 121.
- No returned component has Summer 2026 dates.
- A separately written recursive extractor over each newly captured raw
  schedule JSON response is the current-state oracle. Each application's live
  result is compared with the closest timestamped oracle, with timing skew
  disclosed rather than represented as byte-identical input.
- Direct PeopleSoft catalog probes independently confirm the previously omitted
  CINEMA 125, representative omitted MULTIMD catalogs, and PHOTO 015/121.

If the live schedule changes before verification, record the new snapshot and
update the live expectations. The deterministic replay retains the original
regression evidence; the live report uses current counts. Agreement between the
two applications alone is insufficient because both depend on the official
schedule service for course discovery.

## Documentation Deliverables

### CLI repository

- Add an incident document describing impact, reproduction, root cause, fix,
  compatibility, and verification.
- Update README whole-subject search claims and failure behavior.
- Update HANDOFF request-shape and course-discovery documentation.
- Add the Printing Press patch sidecar.

### Chatbot repository

- Update `docs/classscheduleapi.md` with the primary/related section schema and
  completeness rules.
- Document the voice presentation cap separately from data completeness.
- Record the exact tests and live comparison in the implementation handoff or
  pull-request description.

## Implementation Isolation

Both current checkouts contain user-owned changes:

- `laccd-class-search-cli` has modified CCN/version files and untracked patch
  sidecars/tests.
- `laccd_chatbot` has untracked `dogfood-output/` and `jina.txt`.

Implementation will use separate git worktrees and feature branches for each
repository. No existing modified or untracked file will be staged, overwritten,
or deleted. Cross-repository validation will invoke the built CLI and chatbot
tests from their isolated paths.

## Rollout and Deployment Gates

1. Commit and review the CLI fix independently.
2. Build the CLI and run offline tests.
3. Capture the replay fixtures, run CLI-only replay against them, then capture a
   fresh live three-subject snapshot and compare the CLI against the nearest
   timestamped oracle plus direct PeopleSoft spot checks.
4. Commit and review the chatbot fix independently.
5. Run targeted and full chatbot tests.
6. Run the full deterministic cross-repository replay against the committed
   chatbot and CLI revisions, compare both normalized sets with the oracle, and
   then perform a separately labeled fresh-process live smoke check.
7. Provide the user with exact repository revisions and unresolved caveats.
8. Only then instruct the existing `laccd_chatbot` task to deploy the verified
   chatbot revision. Deployment authorization must explicitly name the target
   environment and revision. The deploying task must record its standard health
   command plus a production LAMC schedule-query readback showing the expected
   course/component disclosure; a local pass is not deployment evidence.

The CLI can be released separately. Installing a local binary does not publish
a GitHub release or update downstream consumers; those actions require explicit
release authorization after the fix revision is known.

## Acceptance Criteria

The work is complete when:

- the CLI regression catches the old omission and passes with the new discovery
  path;
- whole-subject CLI output is complete for the discovered scope or explicitly
  marked truncated;
- mixed-term sample rows cannot enter catalog-scoped results;
- chatbot normalization includes linked components once;
- CLI and chatbot live class-number sets each agree with the captured raw
  schedule oracle for the acceptance subjects;
- all required repository tests and checks pass;
- documentation and Printing Press preservation notes are committed;
- existing user-owned checkout changes remain untouched;
- no deployment or release is represented as complete without revision-specific
  production evidence.
