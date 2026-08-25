// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSearchScopeUsesDiscoveryInsteadOfCollapsedSample(t *testing.T) {
	discovery := &fakeDiscovery{catalogs: []string{"003", "107", "125"}}
	server := scopeServer(t, map[string]string{
		"003": readFixture(t, "completeness/sections-100.html"),
		"107": readFixture(t, "completeness/sections-110.html"),
		"125": readFixture(t, "completeness/sections-125.html"),
	})
	client := New(server.server.URL, 0, time.Second, WithCourseDiscovery(discovery))

	got, err := client.SearchScope(context.Background(), "LAMC", "2268", "CINEMA", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"17490", "17491"}; !slices.Equal(onlyCatalogClassNbrs(got.Sections, "125"), want) {
		t.Fatalf("CINEMA 125 class numbers = %v, want %v", onlyCatalogClassNbrs(got.Sections, "125"), want)
	}
	if slices.Contains(classNbrs(got.Sections), "SUMMER-ROW") {
		t.Fatalf("collapsed sample row leaked into sections: %v", classNbrs(got.Sections))
	}
	if got.CoursesScanned != 3 || got.CoursesTotal != 3 || got.Truncated {
		t.Fatalf("bad metadata: %+v", got)
	}
	if want := []string{"003", "107", "125"}; !slices.Equal(got.Catalogs, want) {
		t.Fatalf("Catalogs = %v, want %v", got.Catalogs, want)
	}
	if discovery.calls != 1 {
		t.Fatalf("discovery calls = %d, want 1", discovery.calls)
	}
	if server.collapsedCalls != 0 {
		t.Fatalf("collapsed PeopleSoft calls = %d, want 0", server.collapsedCalls)
	}
}

func TestNewDefaultsToScheduleAPIDiscovery(t *testing.T) {
	client := New("https://example.test", 0, time.Second)
	if _, ok := client.discovery.(*scheduleAPIDiscovery); !ok {
		t.Fatalf("default discovery = %T, want *scheduleAPIDiscovery", client.discovery)
	}
}

func TestSearchScopePositiveBoundTruncatesDeterministically(t *testing.T) {
	discovery := &fakeDiscovery{catalogs: []string{"003", "107", "125"}}
	server := scopeServer(t, map[string]string{
		"003": readFixture(t, "completeness/sections-100.html"),
		"107": readFixture(t, "completeness/sections-110.html"),
		"125": readFixture(t, "completeness/sections-125.html"),
	})
	client := New(server.server.URL, 0, time.Second, WithCourseDiscovery(discovery))

	got, err := client.SearchScope(context.Background(), "LAMC", "2268", "CINEMA", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.CoursesScanned != 2 || got.CoursesTotal != 3 || !got.Truncated {
		t.Fatalf("bad metadata: %+v", got)
	}
	if want := []string{"003", "107"}; !slices.Equal(got.Catalogs, want) {
		t.Fatalf("Catalogs = %v, want %v", got.Catalogs, want)
	}
	if want := []string{"17300", "17310"}; !slices.Equal(classNbrs(got.Sections), want) {
		t.Fatalf("section order = %v, want %v", classNbrs(got.Sections), want)
	}
	if gotCalls := server.catalogCalls(); !slices.Equal(gotCalls, []string{"003", "107"}) {
		t.Fatalf("catalog calls = %v, want [003 107]", gotCalls)
	}
}

func TestSearchScopeDeduplicatesClassNumbersAcrossCatalogs(t *testing.T) {
	discovery := &fakeDiscovery{catalogs: []string{"003", "107", "125"}}
	server := scopeServer(t, map[string]string{
		"003": readFixture(t, "completeness/sections-100.html"),
		"107": readFixture(t, "completeness/sections-110.html"),
		"125": readFixture(t, "completeness/sections-125.html"),
	})
	client := New(server.server.URL, 0, time.Second, WithCourseDiscovery(discovery))

	got, err := client.SearchScope(context.Background(), "LAMC", "2268", "CINEMA", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if count := countClassNbr(got.Sections, "17310"); count != 1 {
		t.Fatalf("class number 17310 emitted %d times, want 1: %v", count, classNbrs(got.Sections))
	}
}

func TestSearchScopeRejectsNegativeBounds(t *testing.T) {
	discovery := &fakeDiscovery{catalogs: []string{"003"}}
	server := scopeServer(t, map[string]string{"003": readFixture(t, "completeness/sections-100.html")})
	client := New(server.server.URL, 0, time.Second, WithCourseDiscovery(discovery))

	_, err := client.SearchScope(context.Background(), "LAMC", "2268", "CINEMA", "", -1)
	if err == nil || !strings.Contains(err.Error(), "maxCourses") {
		t.Fatalf("SearchScope negative bound error = %v, want maxCourses error", err)
	}
	if discovery.calls != 0 || len(server.catalogCalls()) != 0 {
		t.Fatalf("negative bound made calls: discovery=%d catalogs=%v", discovery.calls, server.catalogCalls())
	}
}

func TestSearchScopeAbortsOnCatalogFailure(t *testing.T) {
	cases := []struct {
		name        string
		catalogs    map[string]string
		wantOverCap bool
	}{
		{
			name: "http error",
			catalogs: map[string]string{
				"003": readFixture(t, "completeness/sections-100.html"),
				"107": "HTTP 503",
				"125": readFixture(t, "completeness/sections-125.html"),
			},
		},
		{
			name: "over cap",
			catalogs: map[string]string{
				"003": readFixture(t, "completeness/sections-100.html"),
				"107": "This search will return over 300 classes",
				"125": readFixture(t, "completeness/sections-125.html"),
			},
			wantOverCap: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			discovery := &fakeDiscovery{catalogs: []string{"003", "107", "125"}}
			server := scopeServer(t, tc.catalogs)
			client := New(server.server.URL, 0, time.Second, WithCourseDiscovery(discovery))

			_, err := client.SearchScope(context.Background(), "LAMC", "2268", "CINEMA", "", 0)
			if err == nil {
				t.Fatal("SearchScope succeeded after a catalog failure")
			}
			if tc.wantOverCap && !errors.Is(err, ErrOverCap) {
				t.Fatalf("SearchScope error = %v, want ErrOverCap", err)
			}
			if gotCalls := server.catalogCalls(); !slices.Equal(gotCalls, []string{"003", "107"}) {
				t.Fatalf("catalog calls = %v, want abort after [003 107]", gotCalls)
			}
		})
	}
}

func TestSearchScopeReturnsDiscoveryFailure(t *testing.T) {
	discovery := &fakeDiscovery{err: errors.New("official listing unavailable")}
	server := scopeServer(t, nil)
	client := New(server.server.URL, 0, time.Second, WithCourseDiscovery(discovery))

	_, err := client.SearchScope(context.Background(), "LAMC", "2268", "CINEMA", "", 0)
	if !errors.Is(err, discovery.err) {
		t.Fatalf("SearchScope error = %v, want discovery error", err)
	}
	if len(server.catalogCalls()) != 0 || server.collapsedCalls != 0 {
		t.Fatalf("discovery failure made PeopleSoft calls: catalogs=%v collapsed=%d", server.catalogCalls(), server.collapsedCalls)
	}
}

func TestSearchScopeDirectCatalogDoesNotCallDiscovery(t *testing.T) {
	discovery := &fakeDiscovery{err: errors.New("direct catalog must not discover")}
	server := scopeServer(t, map[string]string{"125": readFixture(t, "completeness/sections-125.html")})
	client := New(server.server.URL, 0, time.Second, WithCourseDiscovery(discovery))

	got, err := client.SearchScope(context.Background(), "LAMC", "2268", "CINEMA", "125", 0)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.calls != 0 {
		t.Fatalf("discovery calls = %d, want 0", discovery.calls)
	}
	if got.CoursesScanned != 1 || got.CoursesTotal != 1 || got.Truncated {
		t.Fatalf("bad metadata: %+v", got)
	}
	if want := []string{"125"}; !slices.Equal(got.Catalogs, want) {
		t.Fatalf("Catalogs = %v, want %v", got.Catalogs, want)
	}
}

type fakeDiscovery struct {
	catalogs []string
	err      error
	calls    int
}

func (d *fakeDiscovery) Catalogs(context.Context, string, string, string) ([]string, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return slices.Clone(d.catalogs), nil
}

type scopedTestServer struct {
	server         *httptest.Server
	catalogs       map[string]string
	calls          []string
	collapsedCalls int
}

func scopeServer(t *testing.T, catalogs map[string]string) *scopedTestServer {
	t.Helper()
	s := &scopedTestServer{catalogs: catalogs}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery == "" { // Client warm-up request.
			_, _ = w.Write([]byte("warm"))
			return
		}
		catalog := r.URL.Query().Get("catalogid")
		if catalog == "" {
			s.collapsedCalls++
			_, _ = w.Write([]byte(collapsedSampleFixture))
			return
		}
		s.calls = append(s.calls, catalog)
		body, ok := s.catalogs[catalog]
		if !ok {
			http.Error(w, "unknown catalog "+catalog, http.StatusNotFound)
			return
		}
		if body == "HTTP 503" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *scopedTestServer) catalogCalls() []string { return slices.Clone(s.calls) }

func onlyCatalogClassNbrs(sections []Section, catalog string) []string {
	var out []string
	for _, section := range sections {
		if section.Catalog == catalog {
			out = append(out, section.ClassNbr)
		}
	}
	return out
}

func countClassNbr(sections []Section, classNbr string) int {
	count := 0
	for _, section := range sections {
		if section.ClassNbr == classNbr {
			count++
		}
	}
	return count
}

const collapsedSampleFixture = `
<a title='Collapse section CINEMA 003 - Film Appreciation'></a>
<img src='PS_CS_STATUS_OPEN_ICN'><a id='MTG_CLASS_NBR$0'>17300</a><span id='MTG_CLASSNAME$0'>001-LEC</span>
<a title='Collapse section CINEMA 107 - Film History'></a>
<img src='PS_CS_STATUS_OPEN_ICN'><a id='MTG_CLASS_NBR$1'>17310</a><span id='MTG_CLASSNAME$1'>001-LEC</span>
<a title='Collapse section CINEMA 125 - Summer Film'></a>
<img src='PS_CS_STATUS_OPEN_ICN'><a id='MTG_CLASS_NBR$2'>SUMMER-ROW</a><span id='MTG_CLASSNAME$2'>001-LEC</span>
`
