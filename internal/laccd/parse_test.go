// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

func TestParseSectionsSingleCourse(t *testing.T) {
	secs := ParseSections(readFixture(t, "sections-math261.html"), "LAPC", "2268", "MATH")
	if len(secs) != 17 {
		t.Fatalf("expected 17 sections, got %d", len(secs))
	}
	byNbr := map[string]Section{}
	for _, s := range secs {
		byNbr[s.ClassNbr] = s
	}
	got, ok := byNbr["13079"]
	if !ok {
		t.Fatalf("section 13079 not parsed; got class numbers %v", classNbrs(secs))
	}
	checks := map[string]string{
		"college":      got.College,
		"college_name": got.CollegeName,
		"term":         got.Term,
		"subject":      got.Subject,
		"catalog":      got.Catalog,
		"course":       got.Course,
		"title":        got.Title,
		"section":      got.Section,
		"component":    got.Component,
		"days_times":   got.DaysTimes,
		"instructor":   got.Instructor,
		"start_date":   got.StartDate,
		"end_date":     got.EndDate,
		"status":       got.Status,
	}
	want := map[string]string{
		"college":      "LAPC",
		"college_name": "Pierce College",
		"term":         "2268",
		"subject":      "MATH",
		"catalog":      "261",
		"course":       "MATH 261",
		"title":        "Calculus I",
		"section":      "P01",
		"component":    "LEC",
		"days_times":   "MoTuWeTh 8:00AM - 9:10AM",
		"instructor":   "Jacob Veiga",
		"start_date":   "08/31/2026",
		"end_date":     "12/20/2026",
		"status":       "Wait List",
	}
	for k, w := range want {
		if checks[k] != w {
			t.Errorf("section 13079 %s = %q, want %q", k, checks[k], w)
		}
	}
	// Every section must carry a non-empty class number and a status.
	for _, s := range secs {
		if s.ClassNbr == "" {
			t.Errorf("section with empty class_nbr: %+v", s)
		}
		if s.Status == "" {
			t.Errorf("section %s has empty status", s.ClassNbr)
		}
		if s.ID != s.ClassNbr {
			t.Errorf("section %s ID (%s) must mirror ClassNbr for store keying", s.ClassNbr, s.ID)
		}
	}
}

func classNbrs(secs []Section) []string {
	out := make([]string, len(secs))
	for i, s := range secs {
		out[i] = s.ClassNbr
	}
	return out
}

func TestParseSectionsMultiCourseCatalogGrouping(t *testing.T) {
	// The whole-subject (no-catalog) page renders a collapsed sample of sections
	// across many courses. This exercises the linear course-marker -> section
	// assignment across course-group boundaries; it does not assert completeness
	// (that is SearchScope's per-course fan-out, which needs live requests).
	html := readFixture(t, "sections-allmath.html")
	secs := ParseSections(html, "LAPC", "2268", "MATH")
	if len(secs) < 10 {
		t.Fatalf("expected sections across courses, got %d", len(secs))
	}
	// Every section must carry that course's catalog/title with no
	// cross-contamination between course groups.
	titleByCatalog := map[string]string{}
	for _, s := range secs {
		if s.Catalog == "" {
			t.Errorf("section %s has empty catalog", s.ClassNbr)
			continue
		}
		if s.Title == "" {
			t.Errorf("section %s (catalog %s) has empty title", s.ClassNbr, s.Catalog)
		}
		if prev, ok := titleByCatalog[s.Catalog]; ok && prev != s.Title {
			t.Errorf("catalog %s has inconsistent titles %q vs %q", s.Catalog, prev, s.Title)
		}
		titleByCatalog[s.Catalog] = s.Title
	}
	if len(titleByCatalog) < 3 {
		t.Errorf("expected sections from >=3 distinct courses, got %d", len(titleByCatalog))
	}
	// The course list (used by SearchScope's fan-out) must enumerate all 9
	// offered MATH courses from this page.
	catalogs := ParseCourseCatalogs(html, "MATH")
	if len(catalogs) != 9 {
		t.Errorf("ParseCourseCatalogs = %d courses, want 9 (%v)", len(catalogs), catalogs)
	}
}

func TestParseDetailSeats(t *testing.T) {
	d, ok := ParseDetail(readFixture(t, "detail-math261.html"), "13079")
	if !ok {
		t.Fatal("ParseDetail reported no fields found")
	}
	if d.EnrollCap != 35 {
		t.Errorf("EnrollCap = %d, want 35", d.EnrollCap)
	}
	if d.Enrolled != 19 {
		t.Errorf("Enrolled = %d, want 19", d.Enrolled)
	}
	if d.AvailableSeats != 16 {
		t.Errorf("AvailableSeats = %d, want 16", d.AvailableSeats)
	}
	if d.Status != "Open" {
		t.Errorf("Status = %q, want Open", d.Status)
	}
	if d.Units == "" {
		t.Error("Units should be populated")
	}
}

func TestParseTerms(t *testing.T) {
	terms := ParseTerms(readFixture(t, "entry-form.html"))
	if len(terms) == 0 {
		t.Fatal("no terms parsed")
	}
	codes := map[string]string{}
	for _, term := range terms {
		codes[term.Code] = term.Label
	}
	if _, ok := codes["2268"]; !ok {
		t.Errorf("term 2268 (Fall 2026) not parsed; got %v", codes)
	}
}

func TestTermLabel(t *testing.T) {
	cases := map[string]string{
		"2268": "Fall 2026",
		"2266": "Summer 2026",
		"2262": "Spring 2026",
		"2264": "", // non-standard intersession digit
		"abcd": "",
	}
	for code, want := range cases {
		if got := TermLabel(code); got != want {
			t.Errorf("TermLabel(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestParseSubjects(t *testing.T) {
	subs := ParseSubjects(readFixture(t, "subject-lookup.html"))
	if len(subs) < 10 {
		t.Fatalf("expected many subjects, got %d", len(subs))
	}
	byCode := map[string]Subject{}
	for _, s := range subs {
		byCode[s.Code] = s
	}
	// A real multi-word code must be preserved verbatim (not space-stripped).
	if _, ok := byCode["ADM JUS"]; !ok {
		t.Errorf("multi-word subject code 'ADM JUS' not preserved; codes %v", codes(subs))
	}
	if s, ok := byCode["ACCTG"]; !ok || s.Name == "" {
		t.Errorf("subject ACCTG missing or unnamed: %+v", s)
	}
	// CCN subjects must be flagged.
	if s, ok := byCode["ANTH"]; ok && !s.CCN {
		t.Errorf("ANTH should be flagged CCN (name %q)", s.Name)
	}
}

func codes(subs []Subject) []string {
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.Code
	}
	return out
}

func TestMatchesMode(t *testing.T) {
	if !MatchesMode("online", "Online (asynchronous)") {
		t.Error("online filter should match async online mode")
	}
	if MatchesMode("hybrid", "In Person") {
		t.Error("hybrid filter should not match in-person mode")
	}
	if !MatchesMode("", "anything") {
		t.Error("empty filter should match everything")
	}
}
