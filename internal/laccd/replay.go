// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
)

// ReplayManifest binds a captured fixture set to one normalized search scope
// and records the fixture bytes permitted during replay.
type ReplayManifest struct {
	College       string            `json:"college"`
	Term          string            `json:"term"`
	Subject       string            `json:"subject"`
	FixtureHashes map[string]string `json:"fixture_hashes"`
	Catalogs      []string          `json:"catalogs"`
	ClassNumbers  []string          `json:"class_numbers"`
}

// MatchesScope reports whether the normalized input is the captured scope.
func (m ReplayManifest) MatchesScope(college, term, subject string) bool {
	return normalizeReplayCollege(college) == m.College &&
		normalizeReplayTerm(term) == m.Term &&
		normalizeReplaySubject(subject) == m.Subject
}

// FixtureReplay serves captured schedule-discovery JSON and catalog-scoped
// PeopleSoft HTML exclusively from a local fixture directory. It is used by
// deterministic replay tests and the offline replay command.
type FixtureReplay struct {
	server        *httptest.Server
	discovery     CourseDiscovery
	manifest      ReplayManifest
	fixtureHashes map[string]string
	fixtureBytes  map[string][]byte
}

// StartFixtureReplay starts a local replay server backed by fixtures. The
// directory must contain a replay manifest, a verified discovery JSON fixture,
// and a verified catalog HTML fixture for every allowlisted catalog. The server
// holds immutable in-memory copies of only those verified bytes, so a discovered
// catalog cannot cause an unhashed file to be served or parsed.
func StartFixtureReplay(fixtures string) (*FixtureReplay, error) {
	fixtures = filepath.Clean(fixtures)
	manifest, fixtureBytes, fixtureHashes, err := loadReplayFixtures(fixtures)
	if err != nil {
		return nil, err
	}

	replay := &FixtureReplay{
		manifest:      manifest,
		fixtureHashes: fixtureHashes,
		fixtureBytes:  fixtureBytes,
	}
	replay.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/listing/") {
			if r.URL.Path != "/listing/"+replay.manifest.College+"/"+replay.manifest.Term ||
				normalizeReplaySubject(r.URL.Query().Get("subjectlist")) != replay.manifest.Subject {
				http.Error(w, "replay scope mismatch", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(replay.fixtureBytes["discovery-multimd.json"])
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
		if !replay.manifest.MatchesScope(r.URL.Query().Get("Campus"), r.URL.Query().Get("strm"), r.URL.Query().Get("subj")) {
			http.Error(w, "replay scope mismatch", http.StatusBadRequest)
			return
		}
		if filepath.Base(catalog) != catalog {
			http.Error(w, "invalid catalog fixture", http.StatusBadRequest)
			return
		}
		html, ok := replay.fixtureBytes["sections-"+catalog+".html"]
		if !ok {
			http.Error(w, "catalog is not in replay manifest", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	}))
	replay.discovery = NewScheduleAPIDiscovery(replay.server.URL, replay.server.Client(), cliutil.NewAdaptiveLimiter(0))
	return replay, nil
}

func loadReplayFixtures(fixtures string) (ReplayManifest, map[string][]byte, map[string]string, error) {
	body, err := os.ReadFile(filepath.Join(fixtures, "replay-manifest.json"))
	if err != nil {
		return ReplayManifest{}, nil, nil, fmt.Errorf("read replay manifest: %w", err)
	}
	var manifest ReplayManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return ReplayManifest{}, nil, nil, fmt.Errorf("decode replay manifest: %w", err)
	}
	manifest.College = normalizeReplayCollege(manifest.College)
	manifest.Term = normalizeReplayTerm(manifest.Term)
	manifest.Subject = normalizeReplaySubject(manifest.Subject)
	if manifest.College == "" || manifest.Term == "" || manifest.Subject == "" {
		return ReplayManifest{}, nil, nil, errors.New("replay manifest must include college, term, and subject")
	}
	if len(manifest.FixtureHashes) == 0 || len(manifest.Catalogs) == 0 || len(manifest.ClassNumbers) == 0 {
		return ReplayManifest{}, nil, nil, errors.New("replay manifest must include fixture hashes, catalogs, and class numbers")
	}
	if _, ok := manifest.FixtureHashes["discovery-multimd.json"]; !ok {
		return ReplayManifest{}, nil, nil, errors.New("replay manifest is missing discovery-multimd.json hash")
	}

	allowlistedCatalogs := make(map[string]bool, len(manifest.Catalogs))
	for i, catalog := range manifest.Catalogs {
		normalized, err := normalizeDiscoveredCatalog(catalog)
		if err != nil {
			return ReplayManifest{}, nil, nil, fmt.Errorf("normalize replay manifest catalog: %w", err)
		}
		if allowlistedCatalogs[normalized] {
			return ReplayManifest{}, nil, nil, fmt.Errorf("replay manifest has duplicate catalog %q", normalized)
		}
		allowlistedCatalogs[normalized] = true
		manifest.Catalogs[i] = normalized
		if _, ok := manifest.FixtureHashes["sections-"+normalized+".html"]; !ok {
			return ReplayManifest{}, nil, nil, fmt.Errorf("replay manifest is missing sections-%s.html hash", normalized)
		}
	}

	fixtureBytes := make(map[string][]byte, len(manifest.FixtureHashes))
	fixtureHashes := make(map[string]string, len(manifest.FixtureHashes))
	for name, want := range manifest.FixtureHashes {
		if filepath.Base(name) != name {
			return ReplayManifest{}, nil, nil, fmt.Errorf("invalid fixture name %q", name)
		}
		data, err := os.ReadFile(filepath.Join(fixtures, name))
		if err != nil {
			return ReplayManifest{}, nil, nil, fmt.Errorf("read fixture %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, want) {
			return ReplayManifest{}, nil, nil, fmt.Errorf("fixture %s SHA-256 = %s, want %s", name, got, want)
		}
		fixtureBytes[name] = bytesClone(data)
		fixtureHashes[name] = got
	}
	if err := verifyReplayDiscovery(manifest, allowlistedCatalogs, fixtureBytes["discovery-multimd.json"]); err != nil {
		return ReplayManifest{}, nil, nil, err
	}
	return manifest, fixtureBytes, fixtureHashes, nil
}

func verifyReplayDiscovery(manifest ReplayManifest, allowlistedCatalogs map[string]bool, data []byte) error {
	var listing scheduleAPIListing
	if err := json.Unmarshal(data, &listing); err != nil {
		return fmt.Errorf("decode replay discovery fixture: %w", err)
	}
	season, year, ok := termTokens(TermLabel(manifest.Term))
	if !ok {
		return fmt.Errorf("unsupported replay manifest term %q", manifest.Term)
	}
	if responseSeason, responseYear, valid := scheduleTermTokens(listing.TermName); !valid || responseSeason != season || responseYear != year {
		return fmt.Errorf("replay discovery term %q does not match manifest term %q", listing.TermName, manifest.Term)
	}
	for _, entry := range listing.Subjects {
		if len(entry.Courses) == 0 {
			continue
		}
		if normalizeReplaySubject(entry.Code) != manifest.Subject {
			return fmt.Errorf("replay discovery has courses for unexpected subject %q", entry.Code)
		}
		for _, course := range entry.Courses {
			catalog, err := normalizeDiscoveredCatalog(course.CatalogNbr)
			if err != nil {
				return fmt.Errorf("normalize replay discovery catalog: %w", err)
			}
			if !allowlistedCatalogs[catalog] {
				return fmt.Errorf("discovered catalog %q is not in the replay manifest", catalog)
			}
		}
	}
	return nil
}

func normalizeReplayCollege(college string) string {
	return strings.ToUpper(strings.TrimSpace(college))
}

func normalizeReplayTerm(term string) string { return strings.TrimSpace(term) }

func normalizeReplaySubject(subject string) string {
	return strings.ToUpper(strings.TrimSpace(subject))
}

func bytesClone(data []byte) []byte { return append([]byte(nil), data...) }

// URL is the local base URL for a Client that should replay PeopleSoft HTML.
func (r *FixtureReplay) URL() string { return r.server.URL }

// Discovery returns a local-only discovery adapter backed by the replay server.
func (r *FixtureReplay) Discovery() CourseDiscovery { return r.discovery }

// Manifest returns a copy of the verified captured scope and oracle metadata.
func (r *FixtureReplay) Manifest() ReplayManifest {
	manifest := r.manifest
	manifest.Catalogs = slices.Clone(r.manifest.Catalogs)
	manifest.ClassNumbers = slices.Clone(r.manifest.ClassNumbers)
	manifest.FixtureHashes = cloneStringMap(r.manifest.FixtureHashes)
	return manifest
}

// FixtureHashes returns the verified SHA-256 hashes used by this replay.
func (r *FixtureReplay) FixtureHashes() map[string]string { return cloneStringMap(r.fixtureHashes) }

// Close stops the local replay server.
func (r *FixtureReplay) Close() { r.server.Close() }

func cloneStringMap(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
