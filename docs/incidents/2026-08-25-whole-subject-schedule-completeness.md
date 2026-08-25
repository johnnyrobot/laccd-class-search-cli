# Whole-subject schedule completeness incident

Date: 2026-08-25
Component: `laccd-class-search-pp-cli` whole-subject `search`/`sync`
Status: fixed in the schedule-completeness change set

## Summary

The generated whole-subject path treated a collapsed PeopleSoft result page as
the course list. That page is a sample, not a complete subject schedule. A
whole-subject request could therefore omit offered courses and sections, and a
local mirror could preserve an incomplete scope for later offline reads.

The motivating historical case was LAMC Fall 2026 (`LAMC`, term `2268`,
subject `CINEMA`): the collapsed sample exposed CINEMA 003 and 107 while the
subject also offered CINEMA 125. The old path did not reliably return the
CINEMA 125 sections, including class numbers `17490` and `17491`.

## Reproduction and impact

The affected shape was a whole-subject request with no `catalogid`, equivalent
to:

```bash
laccd-class-search-pp-cli search --college LAMC --term 2268 --subject CINEMA --max-courses 0 --json
```

In the old generated flow, the no-catalog PeopleSoft HTML was parsed as if it
represented every course and section. A collapsed page could omit courses,
and rows from a different term could be mixed into the result. Repeating that
flow through `sync` could leave a mirror that looked usable but was not a
complete whole-subject snapshot. This was an incomplete-read/data-quality
incident, not a remote write or enrollment incident; the CLI remains
read-only.

## Root cause

PeopleSoft's deep-link GET has two materially different behaviors:

* `catalogid=<course>` returns the sections for one course.
* Omitting `catalogid` returns a collapsed whole-subject sample, which is not
  an exhaustive catalog or section listing.

The generated client assumed the second response was authoritative and built
its fan-out from that incomplete sample. The assumption also allowed a
mixed-term sample row to enter whole-subject output.

## Fixed flow

For a whole-subject request, `SearchScope` now:

1. Calls the official LACCD schedule discovery endpoint:
   `https://services.laccd.edu/apps/api/classschedule/listing/{college}/{term}?subjectlist={subject}`.
2. Validates the discovery response term and subject, normalizes catalog
   values, removes duplicates, and sorts them deterministically.
3. Applies `--max-courses`: a positive value selects the first N catalogs;
   `0` is unbounded. Negative values are usage errors.
4. Performs exactly one catalog-scoped PeopleSoft request for each selected
   catalog. It never requests or merges the collapsed no-catalog page.
5. Removes duplicate class numbers across catalog responses, sorts the final
   sections, and reports `courses_scanned`, `courses_total`, and `truncated`.

Discovery, catalog fetch, and over-cap failures abort the whole live scope; no
partial whole-subject result is treated as complete. A bounded `sync` refuses
to persist the scope as complete and tells the caller to raise the bound or
use `--max-courses 0`.

An explicit `--catalog` request bypasses discovery and retains the direct
catalog-scoped PeopleSoft request (`CoursesScanned=1`, `CoursesTotal=1`). This
degraded path remains useful if discovery is unavailable and preserves direct
catalog access to legacy mirror rows, but it makes no claim about whole-subject
completeness.

## Mirror completeness marker and compatibility

Whole-subject `sync` writes the marker on the existing `sync_state` scope row
in the same transaction as section rows and grid-level seat snapshots:

```text
completeness_version = 2
discovery_source     = laccd-schedule-api
catalogs_json        = ordered normalized catalogs, including zero-row catalogs
courses_total        = discovered catalog count
courses_scanned      = selected catalog count
truncated            = false
synced_at            = sync timestamp
```

Only a valid version-2 marker with a non-null catalog array, equal total and
scanned counts, `truncated=false`, and a sync timestamp authorizes a
whole-subject `--data-source local` or `auto` read. Marker writes and all scope
rows are atomic, so a failed or truncated sync cannot replace a known-good
complete scope with partial data.

`discovery_source=laccd-schedule-api` is recorded for provenance. The source
value is not an additional exact-value validity gate; marker validity is
determined by the version, valid ordered catalog array, counts, truncation flag,
and timestamp above.

This migration is additive: existing section rows and the section-row schema
are unchanged. Legacy mirrors remain on disk and remain usable for explicit
catalog-scoped reads. No migration infers completeness from old rows, because
missing catalogs cannot be reconstructed. A whole-subject local read against a
legacy or invalid marker returns configuration error 10 and instructs the
caller to resync with `--max-courses 0`; an `auto` fallback requires the same
valid marker or returns the original live error.

## Evidence boundary

The deterministic regression evidence is local and historical. The replay
manifest under `internal/laccd/testdata/completeness/` pins SHA-256 hashes for
one captured discovery JSON response and catalog-scoped HTML fixtures. The
replay command serves only those verified bytes through a local test server;
it makes no live network request. Its MULTIMD fixture oracle is a regression
fixture, not a claim about the current production schedule.

Any current production count or availability statement requires a separately
timestamped live run against the public LACCD endpoints and, where relevant,
independent direct PeopleSoft catalog probes. This incident record does not
represent the historical fixtures as live evidence.

## Verification

Focused deterministic regressions:

```bash
go test ./internal/laccd -run 'TestSearchScope|TestCompletenessReplayManifest|TestFixtureReplayRejectsMismatchedScope' -count=1
go test ./internal/store -run 'Test.*ScopeCompleteness|TestMigrate_AddsColumnsOnUpgrade_SyncState' -count=1
go test ./internal/cli -run 'Test.*(Completeness|Truncated|LegacyMirror|BoundedLocal|NegativeMaxCourses)' -count=1
go test ./tools/schedule-replay -count=1
go run ./tools/schedule-replay --fixtures internal/laccd/testdata/completeness --college LAMC --term 2268 --subject MULTIMD
```

Repository regression and build checks:

```bash
go test ./... -count=1
go vet ./...
make build-all
git diff --check
```

The replay command's expected oracle is catalogs `100`, `110`, and `125`, with
class numbers `17300`, `17310`, `17490`, and `17491`, plus four verified fixture
hashes. The exact hashes are emitted by the command and remain authoritative
for the captured fixture set.
