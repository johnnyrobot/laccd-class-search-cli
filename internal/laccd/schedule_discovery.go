// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
)

// DefaultScheduleAPIBaseURL is the official LACCD public class-schedule API.
const DefaultScheduleAPIBaseURL = "https://services.laccd.edu/apps/api/classschedule"

// CourseDiscovery returns the catalog numbers offered by one college, term,
// and subject. It intentionally has no PeopleSoft dependency so the official
// schedule listing can establish the complete course fan-out independently.
type CourseDiscovery interface {
	Catalogs(context.Context, string, string, string) ([]string, error)
}

// ClientOption configures an optional Client dependency.
type ClientOption func(*Client)

// WithCourseDiscovery supplies the adapter used for whole-subject catalog
// discovery. It is applied by Client construction in the search-scope flow.
func WithCourseDiscovery(d CourseDiscovery) ClientOption {
	return func(c *Client) { c.discovery = d }
}

type scheduleAPIDiscovery struct {
	baseURL string
	http    *http.Client
	limiter *cliutil.AdaptiveLimiter
}

type scheduleAPIListing struct {
	TermName string                `json:"termname"`
	Subjects *[]scheduleAPISubject `json:"subjects"`
}

type scheduleAPISubject struct {
	Code    string               `json:"code"`
	Courses *[]scheduleAPICourse `json:"courses"`
}

type scheduleAPICourse struct {
	CatalogNbr string `json:"catalogNbr"`
}

// NewScheduleAPIDiscovery creates a CourseDiscovery backed by the official
// public schedule API. The base URL is injectable so callers can exercise the
// real HTTP boundary without making live requests.
func NewScheduleAPIDiscovery(baseURL string, client *http.Client, limiter *cliutil.AdaptiveLimiter) CourseDiscovery {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultScheduleAPIBaseURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &scheduleAPIDiscovery{baseURL: baseURL, http: client, limiter: limiter}
}

// Catalogs gets and validates the official schedule listing before returning
// normalized, deterministic catalog numbers for a subject.
func (d *scheduleAPIDiscovery) Catalogs(ctx context.Context, college, term, subject string) ([]string, error) {
	season, year, ok := termTokens(TermLabel(term))
	if !ok {
		return nil, fmt.Errorf("unsupported term %q for schedule discovery", term)
	}

	college = strings.ToUpper(strings.TrimSpace(college))
	term = strings.TrimSpace(term)
	subject = strings.ToUpper(strings.TrimSpace(subject))
	q := url.Values{}
	q.Set("subjectlist", subject)
	rawURL := d.baseURL + "/listing/" + url.PathEscape(college) + "/" + url.PathEscape(term) + "?" + q.Encode()

	d.limiter.Wait()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readBoundedBody(resp.Body, maxResponseBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("reading schedule discovery response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		d.limiter.OnRateLimit()
		return nil, &cliutil.RateLimitError{URL: rawURL, RetryAfter: cliutil.RetryAfter(resp), Body: snippet(string(body))}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, rawURL, snippet(string(body)))
	}
	d.limiter.OnSuccess()

	var listing scheduleAPIListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decoding schedule discovery response: %w", err)
	}
	if responseSeason, responseYear, valid := scheduleTermTokens(listing.TermName); !valid || responseSeason != season || responseYear != year {
		return nil, fmt.Errorf("schedule discovery response term %q does not match requested term %q", listing.TermName, term)
	}
	if listing.Subjects == nil {
		return nil, fmt.Errorf("schedule discovery response subjects must be present and non-null")
	}

	seen := make(map[string]bool)
	var catalogs []string
	foundSubject := false
	for _, entry := range *listing.Subjects {
		entryCode := strings.TrimSpace(entry.Code)
		if entry.Courses == nil {
			return nil, fmt.Errorf("schedule discovery response courses for subject %q must be present and non-null", entry.Code)
		}
		courses := *entry.Courses
		if !strings.EqualFold(entryCode, subject) {
			if len(courses) == 0 {
				continue
			}
			return nil, fmt.Errorf("schedule discovery response has courses for unexpected subject %q", entry.Code)
		}
		foundSubject = true
		for _, course := range courses {
			catalog, err := normalizeDiscoveredCatalog(course.CatalogNbr)
			if err != nil {
				return nil, err
			}
			if seen[catalog] {
				log.Printf("schedule discovery: duplicate normalized catalog %q ignored", catalog)
				continue
			}
			seen[catalog] = true
			catalogs = append(catalogs, catalog)
		}
	}
	if len(*listing.Subjects) > 0 && !foundSubject {
		return nil, fmt.Errorf("schedule discovery response does not contain requested subject %q", subject)
	}
	sort.Strings(catalogs)
	return catalogs, nil
}

func normalizeDiscoveredCatalog(catalog string) (string, error) {
	catalog = strings.TrimSpace(catalog)
	if catalog == "" {
		return "", fmt.Errorf("schedule discovery response contains an empty catalog number")
	}
	allDigits := true
	for _, r := range catalog {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return normalizeCatalog(catalog), nil
	}
	return strings.ToUpper(catalog), nil
}

func termTokens(label string) (season, year string, ok bool) {
	parts := strings.Fields(label)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.ToLower(parts[0]), parts[1], true
}

var (
	scheduleSeasonRE = regexp.MustCompile(`(?i)\b(spring|summer|fall)\b`)
	scheduleYearRE   = regexp.MustCompile(`\b\d{4}\b`)
)

func scheduleTermTokens(label string) (season, year string, ok bool) {
	seasons := scheduleSeasonRE.FindAllString(label, -1)
	years := scheduleYearRE.FindAllString(label, -1)
	if len(seasons) != 1 || len(years) != 1 {
		return "", "", false
	}
	return strings.ToLower(seasons[0]), years[0], true
}
