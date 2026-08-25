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

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
)

func TestScheduleAPIDiscoveryCatalogsNormalizesAndSorts(t *testing.T) {
	body := `{"termname":"Fall 2026","subjects":[{"code":"MULTIMD","courses":[{"catalogNbr":" C1000 "},{"catalogNbr":"7"},{"catalogNbr":"007"},{"catalogNbr":"110"}]}]}`
	srv := jsonServer(t, http.StatusOK, body)
	d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))
	got, err := d.Catalogs(context.Background(), "LAMC", "2268", "multimd")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"007", "110", "C1000"}; !slices.Equal(got, want) {
		t.Fatalf("Catalogs=%v want %v", got, want)
	}
}

func TestNewAppliesCourseDiscoveryOption(t *testing.T) {
	discovery := courseDiscoveryStub{}
	client := New("https://example.test", 0, time.Second, WithCourseDiscovery(discovery))

	if client.discovery != discovery {
		t.Fatalf("Client discovery = %T, want supplied %T", client.discovery, discovery)
	}
}

func TestScheduleAPIDiscoveryCatalogsUsesListingPathAndSubjectQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/listing/LAMC/2268"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("subjectlist"), "ADM JUS"; got != want {
			t.Errorf("subjectlist = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"termname":"Fall 2026","subjects":[]}`))
	}))
	t.Cleanup(srv.Close)

	d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))
	if got, err := d.Catalogs(context.Background(), "lamc", "2268", "adm jus"); err != nil || len(got) != 0 {
		t.Fatalf("Catalogs = %v, %v; want empty, nil", got, err)
	}
}

func TestScheduleAPIDiscoveryCatalogsRejectsWrongTerm(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"termname":"Summer 2026","subjects":[]}`)
	d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))

	if _, err := d.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD"); err == nil {
		t.Fatal("Catalogs succeeded for a response with the wrong term")
	}
}

func TestScheduleAPIDiscoveryCatalogsRejectsUnsupportedTerm(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"termname":"Fall 2026","subjects":[]}`)
	d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))

	if _, err := d.Catalogs(context.Background(), "LAMC", "2264", "MULTIMD"); err == nil {
		t.Fatal("Catalogs succeeded for an unsupported term")
	}
}

func TestScheduleAPIDiscoveryCatalogsRejectsCourseBearingUnexpectedSubject(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{name: "blank", code: ""},
		{name: "unrelated", code: "MATH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"termname":"Fall 2026","subjects":[{"code":"` + tc.code + `","courses":[{"catalogNbr":"101"}]}]}`
			srv := jsonServer(t, http.StatusOK, body)
			d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))

			if _, err := d.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD"); err == nil {
				t.Fatal("Catalogs accepted a course-bearing unexpected subject")
			}
		})
	}
}

func TestScheduleAPIDiscoveryCatalogsAcceptsEmptyOfferings(t *testing.T) {
	bodies := []string{
		`{"termname":"Fall 2026","subjects":[]}`,
		`{"termname":"Fall 2026","subjects":[{"code":"MULTIMD","courses":[]}]}`,
	}
	for _, body := range bodies {
		srv := jsonServer(t, http.StatusOK, body)
		d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))
		got, err := d.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD")
		if err != nil || len(got) != 0 {
			t.Fatalf("Catalogs = %v, %v; want empty, nil", got, err)
		}
	}
}

func TestScheduleAPIDiscoveryCatalogsRejectsInvalidEmptyEnvelopeSchema(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing subjects", body: `{"termname":"Fall 2026"}`},
		{name: "null subjects", body: `{"termname":"Fall 2026","subjects":null}`},
		{name: "missing courses", body: `{"termname":"Fall 2026","subjects":[{"code":"MULTIMD"}]}`},
		{name: "null courses", body: `{"termname":"Fall 2026","subjects":[{"code":"MULTIMD","courses":null}]}`},
		{name: "unrelated empty subject", body: `{"termname":"Fall 2026","subjects":[{"code":"MATH","courses":[]}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := jsonServer(t, http.StatusOK, tt.body)
			d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))

			if got, err := d.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD"); err == nil {
				t.Fatalf("Catalogs = %v, nil; want schema error", got)
			}
		})
	}
}

func TestScheduleAPIDiscoveryCatalogsRejectsMalformedJSON(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"termname":`)
	d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))

	if _, err := d.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD"); err == nil {
		t.Fatal("Catalogs accepted malformed JSON")
	}
}

func TestScheduleAPIDiscoveryCatalogsReturnsHTTPErrors(t *testing.T) {
	srv := jsonServer(t, http.StatusServiceUnavailable, `temporarily unavailable`)
	d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))

	if _, err := d.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD"); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("Catalogs error = %v, want HTTP 503 error", err)
	}
}

func TestScheduleAPIDiscoveryCatalogsReturnsTypedRateLimitError(t *testing.T) {
	srv := jsonServer(t, http.StatusTooManyRequests, `slow down`)
	d := NewScheduleAPIDiscovery(srv.URL, srv.Client(), cliutil.NewAdaptiveLimiter(0))

	_, err := d.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD")
	var rateLimit *cliutil.RateLimitError
	if !errors.As(err, &rateLimit) {
		t.Fatalf("Catalogs error = %T %[1]v, want *cliutil.RateLimitError", err)
	}
}

func jsonServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type courseDiscoveryStub struct{}

func (courseDiscoveryStub) Catalogs(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
