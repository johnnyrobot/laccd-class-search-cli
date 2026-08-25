// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/store"
	"github.com/spf13/cobra"
)

func TestSearchLegacyMirrorWholeSubjectReturnsConfigError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	seedScheduleMirror(t, dbPath, []laccd.Section{testSection("100", "legacy-100")}, nil)
	flags := &rootFlags{asJSON: true, dataSource: "local"}

	stdout, _, err := executeCompletenessCommand(t, newSearchCmd(flags),
		"--college", "LAMC", "--term", "2268", "--subject", "CINEMA", "--db", dbPath)

	if err == nil || ExitCode(err) != 10 {
		t.Fatalf("legacy whole-subject local error = %v (exit %d), want config exit 10", err, ExitCode(err))
	}
	if stdout != "" {
		t.Fatalf("legacy whole-subject local stdout = %q, want no result envelope", stdout)
	}
}

func TestSearchCompletenessAutoFallbackRequiresVersion2Mirror(t *testing.T) {
	liveErr := errors.New("live schedule unavailable")
	search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
		return laccd.ScopeResult{}, liveErr
	}

	t.Run("legacy mirror returns original live error", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "legacy.db")
		seedScheduleMirror(t, dbPath, []laccd.Section{testSection("100", "legacy-100")}, nil)
		flags := &rootFlags{asJSON: true, dataSource: "auto"}

		stdout, _, err := executeCompletenessCommand(t, newSearchCmdWithScopeSearch(flags, search),
			"--college", "LAMC", "--term", "2268", "--subject", "CINEMA", "--db", dbPath)

		if !errors.Is(err, liveErr) || ExitCode(err) != 5 {
			t.Fatalf("auto legacy error = %v (exit %d), want original live API error exit 5", err, ExitCode(err))
		}
		if stdout != "" {
			t.Fatalf("auto legacy stdout = %q, want no fallback envelope", stdout)
		}
	})

	t.Run("complete mirror is eligible", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "complete.db")
		marker := completeMarker("100", "110", "125")
		seedScheduleMirror(t, dbPath, []laccd.Section{testSection("100", "complete-100")}, &marker)
		flags := &rootFlags{asJSON: true, dataSource: "auto"}

		stdout, stderr, err := executeCompletenessCommand(t, newSearchCmdWithScopeSearch(flags, search),
			"--college", "LAMC", "--term", "2268", "--subject", "CINEMA", "--db", dbPath)
		if err != nil {
			t.Fatalf("auto complete fallback: %v", err)
		}
		var got searchResult
		unmarshalCompletenessJSON(t, stdout, &got)
		if got.Source != "local" || got.Count != 1 || got.ScannedCourses != 3 || got.CoursesTotal != 3 || got.Truncated {
			t.Fatalf("auto complete fallback = %+v, want local count=1 metadata=3/3 untruncated", got)
		}
		if !strings.Contains(stderr, "serving local mirror") {
			t.Fatalf("auto complete fallback stderr = %q, want local-fallback notice", stderr)
		}
	})
}

func TestSearchBoundedLocalUsesCompletenessCatalogs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "complete.db")
	marker := completeMarker("100", "110", "125")
	seedScheduleMirror(t, dbPath, []laccd.Section{
		testSection("100", "class-100"),
		testSection("125", "class-125"),
	}, &marker)
	flags := &rootFlags{asJSON: true, dataSource: "local"}

	stdout, _, err := executeCompletenessCommand(t, newSearchCmd(flags),
		"--college", "lamc", "--term", "2268", "--subject", "cinema", "--max-courses", "2", "--db", dbPath)
	if err != nil {
		t.Fatalf("bounded local search: %v", err)
	}
	var got searchResult
	unmarshalCompletenessJSON(t, stdout, &got)
	var envelope map[string]any
	unmarshalCompletenessJSON(t, stdout, &envelope)
	if got, want := sortedMapKeys(envelope), []string{"count", "courses_total", "note", "scanned_courses", "sections", "source", "truncated"}; !slices.Equal(got, want) {
		t.Fatalf("search JSON keys = %v, want existing public keys %v", got, want)
	}
	if got.Count != 1 || len(got.Sections) != 1 || got.Sections[0].ClassNbr != "class-100" {
		t.Fatalf("bounded local sections = %#v, want only catalog 100 row", got.Sections)
	}
	if got.Source != "local" || got.CoursesTotal != 3 || got.ScannedCourses != 2 || !got.Truncated {
		t.Fatalf("bounded local metadata = %+v, want local total=3 scanned=2 truncated", got)
	}
	if got.Note == "" {
		t.Fatal("bounded local result omitted truncation note")
	}
}

func TestSearchNegativeMaxCoursesIsUsageBeforeDataSourceSelection(t *testing.T) {
	tests := []struct {
		name       string
		dataSource string
		catalog    string
	}{
		{name: "local whole subject", dataSource: "local"},
		{name: "auto whole subject", dataSource: "auto"},
		{name: "live direct catalog", dataSource: "live", catalog: "100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
				calls++
				return laccd.ScopeResult{}, nil
			}
			dbPath := filepath.Join(t.TempDir(), "must-not-open.db")
			flags := &rootFlags{asJSON: true, dataSource: tc.dataSource}
			args := []string{"--college", "LAMC", "--term", "2268", "--subject", "CINEMA", "--max-courses", "-1", "--db", dbPath}
			if tc.catalog != "" {
				args = append(args, "--catalog", tc.catalog)
			}

			stdout, _, err := executeCompletenessCommand(t, newSearchCmdWithScopeSearch(flags, search), args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("negative max-courses error = %v (exit %d), want usage exit 2", err, ExitCode(err))
			}
			if calls != 0 {
				t.Fatalf("negative max-courses selected live search %d times, want 0", calls)
			}
			if stdout != "" {
				t.Fatalf("negative max-courses stdout = %q, want empty", stdout)
			}
		})
	}
}

func TestSyncNegativeMaxCoursesIsUsageBeforeFetch(t *testing.T) {
	calls := 0
	search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
		calls++
		return laccd.ScopeResult{}, nil
	}
	flags := &rootFlags{asJSON: true}
	stdout, _, err := executeCompletenessCommand(t, newSyncCmdWithScopeSearch(flags, search),
		"--college", "LAMC", "--term", "2268", "--subject", "CINEMA", "--catalog", "100", "--max-courses", "-1", "--db", filepath.Join(t.TempDir(), "must-not-open.db"))

	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("sync negative max-courses error = %v (exit %d), want usage exit 2", err, ExitCode(err))
	}
	if calls != 0 {
		t.Fatalf("sync negative max-courses fetched %d times, want 0", calls)
	}
	if stdout != "" {
		t.Fatalf("sync negative max-courses stdout = %q, want empty", stdout)
	}
}

func TestScheduleCompletenessUnsupportedTermIsUsageBeforeScopeSelection(t *testing.T) {
	for _, dataSource := range []string{"local", "auto", "live"} {
		t.Run("search "+dataSource, func(t *testing.T) {
			calls := 0
			search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
				calls++
				return laccd.ScopeResult{}, nil
			}
			flags := &rootFlags{asJSON: true, dataSource: dataSource}
			stdout, _, err := executeCompletenessCommand(t, newSearchCmdWithScopeSearch(flags, search),
				"--college", "LAMC", "--term", "2264", "--subject", "CINEMA", "--db", filepath.Join(t.TempDir(), "unused.db"))
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("unsupported search term error = %v (exit %d), want usage exit 2", err, ExitCode(err))
			}
			if stdout != "" {
				t.Fatalf("unsupported search term stdout = %q, want empty", stdout)
			}
			if calls != 0 {
				t.Fatalf("unsupported search term fetched %d times, want 0", calls)
			}
		})
	}

	t.Run("sync", func(t *testing.T) {
		calls := 0
		search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
			calls++
			return laccd.ScopeResult{}, nil
		}
		flags := &rootFlags{asJSON: true}
		stdout, _, err := executeCompletenessCommand(t, newSyncCmdWithScopeSearch(flags, search),
			"--college", "LAMC", "--term", "2264", "--subject", "CINEMA", "--db", filepath.Join(t.TempDir(), "unused.db"))
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("unsupported sync term error = %v (exit %d), want usage exit 2", err, ExitCode(err))
		}
		if stdout != "" {
			t.Fatalf("unsupported sync term stdout = %q, want empty", stdout)
		}
		if calls != 0 {
			t.Fatalf("unsupported sync term fetched %d times, want 0", calls)
		}
	})
}

func TestScheduleCompletenessDirectCatalogKeepsLegacyLocalBehavior(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	section := testSection("100", "legacy-100")
	section.Term = "2264"
	section.TermLabel = ""
	seedScheduleMirror(t, dbPath, []laccd.Section{section}, nil)
	flags := &rootFlags{asJSON: true, dataSource: "local"}

	stdout, _, err := executeCompletenessCommand(t, newSearchCmd(flags),
		"--college", "LAMC", "--term", "2264", "--subject", "CINEMA", "--catalog", "100", "--db", dbPath)
	if err != nil {
		t.Fatalf("legacy direct-catalog local search: %v", err)
	}
	var got searchResult
	unmarshalCompletenessJSON(t, stdout, &got)
	if got.Source != "local" || got.Count != 1 || got.Sections[0].ClassNbr != "legacy-100" {
		t.Fatalf("legacy direct-catalog local result = %+v", got)
	}
}

func TestSyncTruncatedScopeRollsBackKnownGoodMirror(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "complete.db")
	original := completeMarker("100")
	seedScheduleMirror(t, dbPath, []laccd.Section{testSection("100", "known-good")}, &original)
	search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
		return laccd.ScopeResult{
			Sections:       []laccd.Section{testSection("100", "partial-new")},
			Catalogs:       []string{"100"},
			CoursesScanned: 1,
			CoursesTotal:   2,
			Truncated:      true,
		}, nil
	}
	flags := &rootFlags{asJSON: true}

	stdout, _, err := executeCompletenessCommand(t, newSyncCmdWithScopeSearch(flags, search),
		"--college", "LAMC", "--term", "2268", "--subject", "CINEMA", "--max-courses", "1", "--db", dbPath)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("truncated sync error = %v (exit %d), want usage exit 2", err, ExitCode(err))
	}
	if stdout != "" {
		t.Fatalf("truncated sync stdout = %q, want no success envelope", stdout)
	}

	st := openCompletenessStore(t, dbPath)
	defer st.Close()
	rows, err := st.SectionsByScope("LAMC", "2268", "CINEMA", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sectionClassNumbers(t, rows); !slices.Equal(got, []string{"known-good"}) {
		t.Fatalf("rows after truncated sync = %v, want known-good mirror unchanged", got)
	}
	marker, ok, err := st.GetScopeCompleteness("sections:LAMC:2268:CINEMA")
	if err != nil || !ok || !slices.Equal(marker.Catalogs, original.Catalogs) || marker.SyncedAt != original.SyncedAt {
		t.Fatalf("marker after truncated sync = %#v, ok=%v, err=%v; want original %#v", marker, ok, err, original)
	}
}

func TestSyncCompletenessPersistsVersion2MarkerAtomically(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "complete.db")
	search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
		return laccd.ScopeResult{
			Sections:       []laccd.Section{testSection("100", "class-100"), testSection("125", "class-125")},
			Catalogs:       []string{"100", "110", "125"},
			CoursesScanned: 3,
			CoursesTotal:   3,
		}, nil
	}
	flags := &rootFlags{asJSON: true}

	stdout, _, err := executeCompletenessCommand(t, newSyncCmdWithScopeSearch(flags, search),
		"--college", "lamc", "--term", "2268", "--subject", "cinema", "--max-courses", "0", "--db", dbPath)
	if err != nil {
		t.Fatalf("complete sync: %v", err)
	}
	var envelope map[string]any
	unmarshalCompletenessJSON(t, stdout, &envelope)
	if got, want := sortedMapKeys(envelope), []string{"college", "courses_scanned", "courses_total", "subject", "synced", "term", "truncated"}; !slices.Equal(got, want) {
		t.Fatalf("sync JSON keys = %v, want existing public keys %v", got, want)
	}

	st := openCompletenessStore(t, dbPath)
	defer st.Close()
	marker, ok, err := st.GetScopeCompleteness("sections:LAMC:2268:CINEMA")
	if err != nil || !ok {
		t.Fatalf("complete sync marker = %#v, ok=%v, err=%v", marker, ok, err)
	}
	if marker.Version != 2 || marker.DiscoverySource != "laccd-schedule-api" || !slices.Equal(marker.Catalogs, []string{"100", "110", "125"}) || marker.CoursesTotal != 3 || marker.CoursesScanned != 3 || marker.Truncated || marker.SyncedAt.IsZero() {
		t.Fatalf("complete sync marker = %#v", marker)
	}
	rows, err := st.SectionsByScope("LAMC", "2268", "CINEMA", "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("complete sync rows = %d, err=%v, want 2", len(rows), err)
	}
	snapshots, err := st.SnapshotsFiltered("LAMC", "2268", "CINEMA")
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("complete sync snapshots = %d, err=%v, want 2", len(snapshots), err)
	}
}

func TestSyncCompletenessPersistenceFailureEmitsNoSuccess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "failure.db")
	st := openCompletenessStore(t, dbPath)
	if _, err := st.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_complete_marker
		BEFORE INSERT ON sync_state
		WHEN NEW.resource_type = 'sections:LAMC:2268:CINEMA'
		BEGIN SELECT RAISE(ABORT, 'forced marker failure'); END`); err != nil {
		t.Fatalf("create marker failure trigger: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	search := func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error) {
		return laccd.ScopeResult{
			Sections:       []laccd.Section{testSection("100", "must-rollback")},
			Catalogs:       []string{"100"},
			CoursesScanned: 1,
			CoursesTotal:   1,
		}, nil
	}
	flags := &rootFlags{asJSON: true}
	stdout, _, err := executeCompletenessCommand(t, newSyncCmdWithScopeSearch(flags, search),
		"--college", "LAMC", "--term", "2268", "--subject", "CINEMA", "--max-courses", "0", "--db", dbPath)
	if err == nil {
		t.Fatal("complete sync succeeded despite marker transaction failure")
	}
	if stdout != "" {
		t.Fatalf("failed complete sync stdout = %q, want no success envelope", stdout)
	}

	st = openCompletenessStore(t, dbPath)
	defer st.Close()
	for _, table := range []string{"resources", "sections", "seat_snapshots"} {
		var count int
		if err := st.DB().QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after failed complete sync = %d, want 0", table, count)
		}
	}
}

func executeCompletenessCommand(t *testing.T, cmd *cobra.Command, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func testSection(catalog, classNbr string) laccd.Section {
	return laccd.Section{
		ID: classNbr, ClassNbr: classNbr, College: "LAMC", CollegeName: "Los Angeles Mission College",
		Term: "2268", TermLabel: "Fall 2026", Subject: "CINEMA", Catalog: catalog,
		Course: "CINEMA " + catalog, Title: "Test course", Section: "001", Status: "Open",
	}
}

func completeMarker(catalogs ...string) store.ScopeCompleteness {
	return store.ScopeCompleteness{
		Version: 2, DiscoverySource: "laccd-schedule-api", Catalogs: slices.Clone(catalogs),
		CoursesTotal: len(catalogs), CoursesScanned: len(catalogs),
		SyncedAt: time.Date(2026, time.August, 25, 20, 30, 0, 0, time.UTC),
	}
}

func seedScheduleMirror(t *testing.T, dbPath string, sections []laccd.Section, marker *store.ScopeCompleteness) {
	t.Helper()
	st := openCompletenessStore(t, dbPath)
	defer st.Close()
	items := marshalTestSections(t, sections)
	if marker != nil {
		if err := st.SaveCompleteScheduleScope("sections:LAMC:2268:CINEMA", items, nil, *marker); err != nil {
			t.Fatalf("seed complete mirror: %v", err)
		}
		return
	}
	if _, _, err := st.UpsertBatch("sections", items); err != nil {
		t.Fatalf("seed legacy mirror: %v", err)
	}
}

func marshalTestSections(t *testing.T, sections []laccd.Section) []json.RawMessage {
	t.Helper()
	items := make([]json.RawMessage, 0, len(sections))
	for _, section := range sections {
		raw, err := json.Marshal(section)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, raw)
	}
	return items
}

func openCompletenessStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func sectionClassNumbers(t *testing.T, rows []json.RawMessage) []string {
	t.Helper()
	classNbrs := make([]string, 0, len(rows))
	for _, row := range rows {
		var section laccd.Section
		if err := json.Unmarshal(row, &section); err != nil {
			t.Fatal(err)
		}
		classNbrs = append(classNbrs, section.ClassNbr)
	}
	sort.Strings(classNbrs)
	return classNbrs
}

func unmarshalCompletenessJSON(t *testing.T, raw string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("decode JSON %q: %v", raw, err)
	}
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
