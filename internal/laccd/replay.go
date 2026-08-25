// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
)

// FixtureReplay serves captured schedule-discovery JSON and catalog-scoped
// PeopleSoft HTML exclusively from a local fixture directory. It is used by
// deterministic replay tests and the offline replay command.
type FixtureReplay struct {
	server    *httptest.Server
	discovery CourseDiscovery
}

// StartFixtureReplay starts a local replay server backed by fixtures. The
// directory must contain discovery-multimd.json and sections-<catalog>.html
// files. Missing catalog HTML returns a local 404, making incomplete fixture
// captures fail the SearchScope replay instead of reaching the network.
func StartFixtureReplay(fixtures string) (*FixtureReplay, error) {
	fixtures = filepath.Clean(fixtures)
	discoveryJSON, err := os.ReadFile(filepath.Join(fixtures, "discovery-multimd.json"))
	if err != nil {
		return nil, fmt.Errorf("read discovery fixture: %w", err)
	}

	replay := &FixtureReplay{}
	replay.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/listing/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(discoveryJSON)
			return
		}
		if r.URL.Path != ComponentPath {
			http.NotFound(w, r)
			return
		}
		catalog := r.URL.Query().Get("catalogid")
		if catalog == "" { // PeopleSoft guest-session warm-up.
			_, _ = w.Write([]byte("warm"))
			return
		}
		if filepath.Base(catalog) != catalog {
			http.Error(w, "invalid catalog fixture", http.StatusBadRequest)
			return
		}
		html, err := os.ReadFile(filepath.Join(fixtures, "sections-"+catalog+".html"))
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "read catalog fixture", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	}))
	replay.discovery = NewScheduleAPIDiscovery(replay.server.URL, replay.server.Client(), cliutil.NewAdaptiveLimiter(0))
	return replay, nil
}

// URL is the local base URL for a Client that should replay PeopleSoft HTML.
func (r *FixtureReplay) URL() string { return r.server.URL }

// Discovery returns a local-only discovery adapter backed by the replay server.
func (r *FixtureReplay) Discovery() CourseDiscovery { return r.discovery }

// Close stops the local replay server.
func (r *FixtureReplay) Close() { r.server.Close() }
