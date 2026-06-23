// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"laccd-class-search-pp-cli/internal/cliutil"
)

// Client talks to the LACCD PeopleSoft guest class-search portal over plain
// HTTP. A cookie-jar warm-up GET establishes the guest session; after that the
// list path is a deep-link GET and section detail is a stateful ICAction POST.
type Client struct {
	BaseURL string
	http    *http.Client
	limiter *cliutil.AdaptiveLimiter

	warmMu sync.Mutex
	warmed bool
}

// ErrOverCap signals a results page returned the PeopleSoft "over N classes"
// interstitial instead of rows. Narrow the search (add --catalog) to avoid it.
var ErrOverCap = fmt.Errorf("search matched too many classes to render; narrow it with --catalog")

// New builds a Client. baseURL comes from config (the verify pipeline points it
// at a mock server). ratePerSec <= 0 disables pacing. timeout bounds each HTTP
// call; the per-command --timeout is also applied via boundCtx at the call site.
func New(baseURL string, ratePerSec float64, timeout time.Duration) *Client {
	jar, _ := cookiejar.New(nil)
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		BaseURL: base,
		http:    &http.Client{Jar: jar, Timeout: timeout},
		limiter: cliutil.NewAdaptiveLimiter(ratePerSec),
	}
}

func (c *Client) componentURL() string { return c.BaseURL + ComponentPath }

// warmup performs a one-time GET to establish PeopleSoft session cookies. A
// fresh request without these cookies is 302-redirected to a cookie-check page.
func (c *Client) warmup(ctx context.Context) error {
	c.warmMu.Lock()
	defer c.warmMu.Unlock()
	if c.warmed {
		return nil
	}
	if _, err := c.do(ctx, http.MethodGet, c.componentURL(), nil); err != nil {
		return fmt.Errorf("establishing guest session: %w", err)
	}
	c.warmed = true
	return nil
}

// do issues one request through the limiter, applying the browser headers the
// portal expects and mapping 429 to a typed RateLimitError so throttling is
// never confused with an empty result.
func (c *Client) do(ctx context.Context, method, rawURL string, body url.Values) (string, error) {
	c.limiter.Wait()
	var reqBody io.Reader
	if body != nil {
		reqBody = strings.NewReader(body.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reqBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", c.componentURL())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		c.limiter.OnRateLimit()
		return "", &cliutil.RateLimitError{URL: rawURL, RetryAfter: cliutil.RetryAfter(resp), Body: snippet(string(data))}
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, rawURL, snippet(string(data)))
	}
	c.limiter.OnSuccess()
	return string(data), nil
}

func snippet(s string) string {
	s = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// resultsPage fetches the deep-link results page for a search scope.
func (c *Client) resultsPage(ctx context.Context, college, term, subject, catalog string) (string, error) {
	if err := c.warmup(ctx); err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("Campus", strings.ToUpper(strings.TrimSpace(college)))
	q.Set("strm", strings.TrimSpace(term))
	q.Set("subj", strings.ToUpper(strings.TrimSpace(subject)))
	if cat := normalizeCatalog(catalog); cat != "" {
		q.Set("catalogid", cat)
	}
	// pp:client-call deep-link results page
	return c.do(ctx, http.MethodGet, c.componentURL()+"?"+q.Encode(), nil)
}

// normalizeCatalog left-pads a short numeric catalog number to 3 digits, the
// width the portal's catalogid parameter expects (035 for KIN 35). Four-digit
// CCN numbers and alphanumeric numbers pass through unchanged.
func normalizeCatalog(catalog string) string {
	catalog = strings.TrimSpace(catalog)
	if catalog == "" {
		return ""
	}
	allDigits := true
	for _, r := range catalog {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		for len(catalog) < 3 {
			catalog = "0" + catalog
		}
	}
	return catalog
}

// SearchSections fetches and parses the sections for a (college, term, subject)
// scope, optionally narrowed to one course by catalog.
func (c *Client) SearchSections(ctx context.Context, college, term, subject, catalog string) ([]Section, error) {
	html, err := c.resultsPage(ctx, college, term, subject, catalog)
	if err != nil {
		return nil, err
	}
	secs := ParseSections(html, college, term, subject)
	if len(secs) == 0 && IsOverCap(html) {
		return nil, ErrOverCap
	}
	SortSections(secs)
	return secs, nil
}

// ScopeResult carries the sections for a search plus how the scan was bounded,
// so callers can report partial coverage honestly (scan-and-filter contract).
type ScopeResult struct {
	Sections       []Section
	CoursesScanned int
	CoursesTotal   int
	Truncated      bool
}

// SearchScope returns the sections for a search scope. With a catalog it is a
// single complete request for one course. Without a catalog it fetches the
// course list from the collapsed whole-subject page, then fans out one
// catalog-scoped request per course (up to maxCourses) so the result is the
// complete set of sections — the collapsed page alone only renders a sample.
func (c *Client) SearchScope(ctx context.Context, college, term, subject, catalog string, maxCourses int) (ScopeResult, error) {
	if strings.TrimSpace(catalog) != "" {
		secs, err := c.SearchSections(ctx, college, term, subject, catalog)
		if err != nil {
			return ScopeResult{}, err
		}
		return ScopeResult{Sections: secs, CoursesScanned: 1, CoursesTotal: 1}, nil
	}

	html, err := c.resultsPage(ctx, college, term, subject, "")
	if err != nil {
		return ScopeResult{}, err
	}
	catalogs := ParseCourseCatalogs(html, subject)
	res := ScopeResult{CoursesTotal: len(catalogs)}
	seen := map[string]bool{}
	add := func(secs []Section) {
		for _, s := range secs {
			if seen[s.ClassNbr] {
				continue
			}
			seen[s.ClassNbr] = true
			res.Sections = append(res.Sections, s)
		}
	}
	if maxCourses <= 0 {
		maxCourses = len(catalogs)
	}
	// Fan out per course FIRST: these catalog-scoped pages carry the authoritative
	// catalog number for every section. The collapsed whole-subject page only
	// renders a sample and can mis-attribute catalog for rows above the first
	// course marker, so it is added last and only fills in sections from courses
	// the fan-out skipped (e.g. under a curtailed maxCourses).
	for i, cat := range catalogs {
		if i >= maxCourses {
			res.Truncated = true
			break
		}
		secs, err := c.SearchSections(ctx, college, term, subject, cat)
		if err != nil {
			if err == ErrOverCap {
				continue
			}
			return res, err
		}
		add(secs)
		res.CoursesScanned++
	}
	add(ParseSections(html, college, term, subject))
	SortSections(res.Sections)
	return res, nil
}

// FetchDetail loads the results page for the section's scope, finds the section
// by class number, and POSTs its ICAction link to retrieve seat counts.
func (c *Client) FetchDetail(ctx context.Context, college, term, subject, catalog, classNbr string) (*SectionDetail, error) {
	html, err := c.resultsPage(ctx, college, term, subject, catalog)
	if err != nil {
		return nil, err
	}
	field, ok := ClassNbrFieldIndex(html, classNbr)
	if !ok {
		// The catalog may be missing or stale (e.g. resolved from a mirror that
		// pre-dates a catalog fix). Locate the section's real catalog by scanning
		// the subject, then retry against the correct course page.
		if foundCatalog, found := c.catalogForClass(ctx, college, term, subject, classNbr); found && foundCatalog != catalog {
			html, err = c.resultsPage(ctx, college, term, subject, foundCatalog)
			if err != nil {
				return nil, err
			}
			field, ok = ClassNbrFieldIndex(html, classNbr)
		}
	}
	if !ok {
		if IsOverCap(html) {
			return nil, ErrOverCap
		}
		return nil, fmt.Errorf("class %s not found in %s %s %s sections", classNbr, college, subject, term)
	}
	form := parseFormFields(html)
	form.Set("ICAction", field)
	form.Set("ICElementNum", "0")
	form.Set("ICType", "Panel")
	form.Set("ICResubmit", "0")
	form.Del("ICAJAX") // full-page response is simplest to parse
	detailHTML, err := c.do(ctx, http.MethodPost, c.componentURL(), form)
	if err != nil {
		return nil, err
	}
	detail, found := ParseDetail(detailHTML, classNbr)
	if !found {
		return nil, fmt.Errorf("could not read seat detail for class %s (the portal session may have expired; retry)", classNbr)
	}
	return &detail, nil
}

// catalogForClass scans a subject's courses for a class number and returns the
// catalog of the course that contains it, so FetchDetail can recover from a
// missing or stale catalog. Bounded by the same per-course fan-out as a normal
// whole-subject search.
func (c *Client) catalogForClass(ctx context.Context, college, term, subject, classNbr string) (string, bool) {
	scope, err := c.SearchScope(ctx, college, term, subject, "", 0)
	if err != nil {
		return "", false
	}
	for _, s := range scope.Sections {
		if s.ClassNbr == classNbr && s.Catalog != "" {
			return s.Catalog, true
		}
	}
	return "", false
}

// ListTerms fetches the entry form and parses its active-term dropdown.
func (c *Client) ListTerms(ctx context.Context) ([]Term, error) {
	if err := c.warmup(ctx); err != nil {
		return nil, err
	}
	// pp:client-call entry form for term dropdown
	html, err := c.do(ctx, http.MethodGet, c.componentURL(), nil)
	if err != nil {
		return nil, err
	}
	return ParseTerms(html), nil
}

// ListSubjects opens the subject lookup for a college+term and returns the
// available subjects. It sets the campus and term on the entry form then POSTs
// the subject-search button, threading the ICBcDomData=UnknownValue value that
// PeopleSoft requires for the action to execute.
func (c *Client) ListSubjects(ctx context.Context, college, term string) ([]Subject, error) {
	if err := c.warmup(ctx); err != nil {
		return nil, err
	}
	html, err := c.do(ctx, http.MethodGet, c.componentURL(), nil)
	if err != nil {
		return nil, err
	}
	form := parseFormFields(html)
	setFieldByPrefix(form, `SSR_CLSRCH_WRK_CAMPUS\$\d+`, strings.ToUpper(strings.TrimSpace(college)))
	setFieldByPrefix(form, `CLASS_SRCH_WRK2_STRM\$\d+\$`, strings.TrimSpace(term))
	form.Set("ICAction", "CLASS_SRCH_WRK2_SSR_PB_SUBJ_SRCH$0")
	form.Set("ICBcDomData", "UnknownValue")
	form.Set("ICElementNum", "0")
	form.Set("ICType", "Panel")
	form.Set("ICResubmit", "0")
	form.Del("ICAJAX")
	// pp:client-call subject lookup POST
	resp, err := c.do(ctx, http.MethodPost, c.componentURL(), form)
	if err != nil {
		return nil, err
	}
	return ParseSubjects(resp), nil
}

// setFieldByPrefix overrides the first form field whose name matches the given
// regex (used to set the dynamically-suffixed CAMPUS/STRM fields).
func setFieldByPrefix(form url.Values, pattern, value string) {
	re := regexp.MustCompile(`^` + pattern + `$`)
	for k := range form {
		if re.MatchString(k) {
			form.Set(k, value)
			return
		}
	}
}

// formFieldRE matches every input on a page (any type). Buttons are excluded by
// the caller; selects are read separately.
var (
	inputTagRE  = regexp.MustCompile(`(?s)<input\b[^>]*>`)
	selectTagRE = regexp.MustCompile(`(?s)<select\b[^>]*name='([^']*)'[^>]*>(.*?)</select>`)
	attrNameRE  = regexp.MustCompile(`\bname='([^']*)'`)
	attrValueRE = regexp.MustCompile(`\bvalue='([^']*)'`)
	attrTypeRE  = regexp.MustCompile(`\btype='([^']*)'`)
	// PeopleSoft double-quotes option values even though form inputs are
	// single-quoted, so option matching is quote-agnostic.
	selectedOptRE = regexp.MustCompile(`(?s)<option[^>]*value=["']([^"']*)["'][^>]*\bselected`)
	firstOptRE    = regexp.MustCompile(`<option[^>]*value=["']([^"']*)["']`)
)

// parseFormFields collects all submittable form values (inputs + selects) from
// a page so a detail POST replays the full PeopleSoft form state. Buttons are
// skipped because PeopleSoft submits the clicked control via ICAction, not as a
// form value.
func parseFormFields(html string) url.Values {
	out := url.Values{}
	for _, tag := range inputTagRE.FindAllString(html, -1) {
		nm := attrNameRE.FindStringSubmatch(tag)
		if nm == nil {
			continue
		}
		typ := "text"
		if tm := attrTypeRE.FindStringSubmatch(tag); tm != nil {
			typ = strings.ToLower(tm[1])
		}
		if typ == "button" || typ == "submit" || typ == "image" {
			continue
		}
		val := ""
		if vm := attrValueRE.FindStringSubmatch(tag); vm != nil {
			val = vm[1]
		}
		if (typ == "checkbox" || typ == "radio") && !strings.Contains(strings.ToLower(tag), "checked") {
			continue
		}
		out.Set(nm[1], val)
	}
	for _, m := range selectTagRE.FindAllStringSubmatch(html, -1) {
		name, body := m[1], m[2]
		if sel := selectedOptRE.FindStringSubmatch(body); sel != nil {
			out.Set(name, sel[1])
		} else if first := firstOptRE.FindStringSubmatch(body); first != nil {
			out.Set(name, first[1])
		}
	}
	return out
}
