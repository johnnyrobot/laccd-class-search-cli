// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
)

// elementTextRECache memoizes the per-id regexes elementText builds. The same
// field ids (MTG_DAYTIME$0, SSR_CLS_DTL_WRK_ENRL_CAP, …) recur across every
// parsed page, so caching avoids recompiling ~hundreds of regexes per
// cross-college fan-out.
var elementTextRECache sync.Map // map[string]*regexp.Regexp

func elementTextRE(id string) *regexp.Regexp {
	if re, ok := elementTextRECache.Load(id); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`(?s)id='` + regexp.QuoteMeta(id) + `'[^>]*>(.*?)</(?:span|a|div)>`)
	elementTextRECache.Store(id, re)
	return re
}

var (
	// Course-group markers carry the full course code + title in the collapse
	// toggle's title attribute, e.g. title='Collapse section MATH 261 - Calculus I'.
	courseMarkerRE = regexp.MustCompile(`title='(?:Collapse|Expand) section ([^']+)'`)
	// Per-row class-number anchors. The $N index is shared across the page; the
	// course a row belongs to is the nearest preceding course marker.
	classNbrRowRE = regexp.MustCompile(`id='MTG_CLASS_NBR\$(\d+)'`)
	// Per-row status icons. Three legend icons appear before the grid; each
	// row's own icon is the nearest one before its class-number anchor.
	statusIconRE = regexp.MustCompile(`PS_CS_STATUS_(OPEN|CLOSED|WAITLIST)_ICN`)
	strmNameRE   = regexp.MustCompile(`name='CLASS_SRCH_WRK2_STRM\$\d+\$'`)
	// PeopleSoft double-quotes <option value="..."> and omits </option> close
	// tags, so the label is captured up to the next tag rather than </option>.
	optionRE  = regexp.MustCompile(`(?s)<option[^>]*value=["']([^"']*)["'][^>]*>([^<]*)`)
	hiddenRE  = regexp.MustCompile(`<input\b[^>]*type='hidden'[^>]*>`)
	overCapRE = regexp.MustCompile(`(?i)will return over \d+ classes`)
)

// elementText returns the cleaned inner text of the first span/div/a element
// whose id attribute exactly equals id. The single-quote boundaries on the
// search prevent matching the win0div<id> wrapper. RE2 has no backreferences,
// so the close tag is the first </span|a|div> after the open — correct for the
// leaf value elements PeopleSoft emits.
func elementText(html, id string) string {
	m := elementTextRE(id).FindStringSubmatch(html)
	if m == nil {
		return ""
	}
	return cliutil.CleanText(stripTags(m[1]))
}

var (
	tagRE       = regexp.MustCompile(`<[^>]+>`)
	brRE        = regexp.MustCompile(`(?i)<br\s*/?>`)
	controlWSRE = regexp.MustCompile(`[\r\n\t]+`)
)

func stripTags(s string) string {
	// Preserve line breaks between stacked meeting patterns as separators.
	s = brRE.ReplaceAllString(s, "; ")
	s = tagRE.ReplaceAllString(s, " ")
	// PeopleSoft embeds raw CR/LF inside cell values (e.g. "LEC\r\nRegular");
	// normalize them to spaces so they don't leak into parsed fields and
	// corrupt plain/CSV output with carriage returns.
	return controlWSRE.ReplaceAllString(s, " ")
}

// ParseSections extracts every section from a results-grid page. college, term,
// and subject come from the request that produced the page (they are not
// repeated per row in the grid). Returns an empty slice for a no-results page.
func ParseSections(html, college, term, subject string) []Section {
	college = strings.ToUpper(strings.TrimSpace(college))
	subject = strings.ToUpper(strings.TrimSpace(subject))

	type marker struct {
		pos     int
		catalog string
		title   string
	}
	var courses []marker
	for _, loc := range courseMarkerRE.FindAllStringSubmatchIndex(html, -1) {
		code := html[loc[2]:loc[3]]
		catalog, title := splitCourseCode(code, subject)
		courses = append(courses, marker{pos: loc[0], catalog: catalog, title: cliutil.CleanText(title)})
	}

	type statusAt struct {
		pos    int
		status string
	}
	var statuses []statusAt
	for _, loc := range statusIconRE.FindAllStringSubmatchIndex(html, -1) {
		statuses = append(statuses, statusAt{pos: loc[0], status: statusLabel(html[loc[2]:loc[3]])})
	}

	courseBefore := func(pos int) marker {
		var cur marker
		for _, c := range courses {
			if c.pos < pos {
				cur = c
			} else {
				break
			}
		}
		return cur
	}
	statusBefore := func(pos int) string {
		s := ""
		for _, st := range statuses {
			if st.pos < pos {
				s = st.status
			} else {
				break
			}
		}
		return s
	}

	var out []Section
	for _, loc := range classNbrRowRE.FindAllStringSubmatchIndex(html, -1) {
		pos := loc[0]
		idx := html[loc[2]:loc[3]]
		classNbr := elementText(html, "MTG_CLASS_NBR$"+idx)
		if classNbr == "" {
			continue
		}
		course := courseBefore(pos)
		sec, comp, sess := splitClassName(elementText(html, "MTG_CLASSNAME$"+idx))
		start, end := splitDateRange(elementText(html, "MTG_TOPIC$"+idx))
		courseCode := strings.TrimSpace(subject + " " + course.catalog)
		out = append(out, Section{
			ID:          classNbr,
			ClassNbr:    classNbr,
			College:     college,
			CollegeName: CollegeName(college),
			Term:        term,
			TermLabel:   TermLabel(term),
			Subject:     subject,
			Catalog:     course.catalog,
			Course:      courseCode,
			Title:       course.title,
			Section:     sec,
			Component:   comp,
			Session:     sess,
			DaysTimes:   elementText(html, "MTG_DAYTIME$"+idx),
			Room:        elementText(html, "MTG_ROOM$"+idx),
			Instructor:  elementText(html, "MTG_INSTR$"+idx),
			StartDate:   start,
			EndDate:     end,
			Status:      statusBefore(pos),
		})
	}
	return out
}

func statusLabel(icon string) string {
	switch icon {
	case "OPEN":
		return "Open"
	case "CLOSED":
		return "Closed"
	case "WAITLIST":
		return "Wait List"
	}
	return ""
}

// splitCourseCode turns "MATH 261 - Calculus I" into ("261", "Calculus I").
// The subject prefix is stripped so the catalog is just the number portion.
func splitCourseCode(code, subject string) (catalog, title string) {
	code = cliutil.CleanText(code)
	if i := strings.Index(code, " - "); i >= 0 {
		title = strings.TrimSpace(code[i+3:])
		code = strings.TrimSpace(code[:i])
	}
	// code is now "MATH 261"; drop the subject token, keep the catalog number.
	fields := strings.Fields(code)
	if len(fields) >= 2 {
		catalog = fields[len(fields)-1]
	} else if len(fields) == 1 && !strings.EqualFold(fields[0], subject) {
		catalog = fields[0]
	}
	return catalog, title
}

// splitClassName turns "P01-LEC Regular" into ("P01", "LEC", "Regular"). The
// portal stacks component and session with a <br>, which the parser flattens to
// "; ", so ";" is treated as a token separator alongside whitespace.
func splitClassName(name string) (section, component, session string) {
	name = cliutil.CleanText(name)
	if name == "" {
		return "", "", ""
	}
	if i := strings.Index(name, "-"); i >= 0 {
		section = strings.TrimSpace(name[:i])
		rest := strings.TrimSpace(name[i+1:])
		fields := strings.FieldsFunc(rest, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ';' || r == ','
		})
		if len(fields) > 0 {
			component = fields[0]
			session = strings.Join(fields[1:], " ")
		}
		return section, component, session
	}
	return strings.Trim(name, "; ,"), "", ""
}

// splitDateRange turns "08/31/2026 - 12/20/2026" into the two dates.
func splitDateRange(s string) (start, end string) {
	s = cliutil.CleanText(s)
	if i := strings.Index(s, " - "); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:])
	}
	return s, ""
}

// detailFieldIDs maps SectionDetail integer fields to their PeopleSoft span ids.
var detailIntFields = []struct {
	id     string
	assign func(*SectionDetail, int)
}{
	{"SSR_CLS_DTL_WRK_ENRL_CAP", func(d *SectionDetail, v int) { d.EnrollCap = v }},
	{"SSR_CLS_DTL_WRK_ENRL_TOT", func(d *SectionDetail, v int) { d.Enrolled = v }},
	{"SSR_CLS_DTL_WRK_AVAILABLE_SEATS", func(d *SectionDetail, v int) { d.AvailableSeats = v }},
	{"SSR_CLS_DTL_WRK_SSR_WAIT_TOT_CAP", func(d *SectionDetail, v int) { d.WaitlistCap = v }},
	{"SSR_CLS_DTL_WRK_WAIT_CAP", func(d *SectionDetail, v int) { d.WaitlistCap = v }},
	{"SSR_CLS_DTL_WRK_SSR_WAIT_TOT_ENRL", func(d *SectionDetail, v int) { d.WaitlistTotal = v }},
	{"SSR_CLS_DTL_WRK_WAIT_TOT", func(d *SectionDetail, v int) { d.WaitlistTotal = v }},
}

// ParseDetail extracts seat counts and metadata from a section-detail page.
// The boolean is false when the page carried none of the expected fields (a
// stale/expired page), so callers can distinguish "no detail" from "0 seats".
func ParseDetail(html, classNbr string) (SectionDetail, bool) {
	d := SectionDetail{ClassNbr: classNbr}
	found := false
	for _, f := range detailIntFields {
		if txt := elementText(html, f.id); txt != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(firstToken(txt))); err == nil {
				f.assign(&d, n)
				found = true
			}
		}
	}
	if u := elementText(html, "SSR_CLS_DTL_WRK_UNITS_RANGE"); u != "" {
		d.Units = u
		found = true
	}
	if s := elementText(html, "SSR_CLS_DTL_WRK_SSR_DESCRSHORT"); s != "" {
		d.Status = s
		found = true
	}
	d.InstructionMode = parseInstructionMode(html)
	return d, found
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

var instrModeLabelRE = regexp.MustCompile(`(?is)Instruction Mode</[^>]*>.*?<span[^>]*>([^<]{2,40})</span>`)

// parseInstructionMode reads the "Instruction Mode" value from the detail page.
// The field id varies across PeopleSoft revisions, so this scans for the label
// then takes the adjacent value span.
func parseInstructionMode(html string) string {
	for _, id := range []string{"SSR_CLS_DTL_WRK_SSR_INSTR_MODE", "SSR_CLS_DTL_WRK_INSTRUCTION_MODE", "PSXLATITEM_XLATLONGNAME"} {
		if v := elementText(html, id); v != "" {
			return v
		}
	}
	if m := instrModeLabelRE.FindStringSubmatch(html); m != nil {
		return cliutil.CleanText(m[1])
	}
	return ""
}

// ParseTerms reads the term (STRM) dropdown from an entry-form page. Only
// currently-active terms appear, so this is the source of truth for valid term
// codes rather than the synthetic TermLabel formula.
//
// Position-based rather than one big <select ...>(.*?)</select> regex: the
// select's onchange attribute carries PeopleSoft JavaScript that can contain
// '>' characters, which would truncate a greedy [^>]* tag match before the
// option list is reached.
func ParseTerms(html string) []Term {
	loc := strmNameRE.FindStringIndex(html)
	if loc == nil {
		return nil
	}
	rest := html[loc[1]:]
	end := strings.Index(rest, "</select>")
	if end < 0 {
		return nil
	}
	body := rest[:end]
	var out []Term
	seen := map[string]bool{}
	for _, m := range optionRE.FindAllStringSubmatch(body, -1) {
		code := strings.TrimSpace(m[1])
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		label := cliutil.CleanText(stripTags(m[2]))
		if label == "" {
			label = TermLabel(code)
		}
		out = append(out, Term{Code: code, Label: label})
	}
	return out
}

// ParseCourseCatalogs returns the distinct catalog numbers offered for a subject
// from a whole-subject (no-catalog) results page. That page renders a collapsed
// sample of sections per course, so a complete per-course result requires
// fanning out one catalog-scoped request per number returned here.
func ParseCourseCatalogs(html, subject string) []string {
	subject = strings.ToUpper(strings.TrimSpace(subject))
	var out []string
	seen := map[string]bool{}
	for _, m := range courseMarkerRE.FindAllStringSubmatch(html, -1) {
		catalog, _ := splitCourseCode(m[1], subject)
		if catalog == "" || seen[catalog] {
			continue
		}
		seen[catalog] = true
		out = append(out, catalog)
	}
	return out
}

var subjectCodeRE = regexp.MustCompile(`(?s)id='SSR_CLSRCH_SUBJ_SUBJECT\$(\d+)'[^>]*>(.*?)</span>`)

// ParseSubjects extracts the subject code/description rows from a subject-lookup
// page. Codes are kept verbatim (spaces are significant in LACCD subject codes).
func ParseSubjects(html string) []Subject {
	var out []Subject
	seen := map[string]bool{}
	for _, m := range subjectCodeRE.FindAllStringSubmatch(html, -1) {
		idx := m[1]
		code := cliutil.CleanText(stripTags(m[2]))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		name := elementText(html, "SUBJECT_TBL_DESCRFORMAL$"+idx)
		out = append(out, Subject{Code: code, Name: name, CCN: strings.Contains(name, "(CCN)")})
	}
	return out
}

// ParseHiddenInputs collects every hidden form field on a page into a map,
// keyed by name. Used to thread ICSID/ICStateNum and the rest of the form
// state into a section-detail ICAction POST.
func ParseHiddenInputs(html string) map[string]string {
	out := map[string]string{}
	nameRE := regexp.MustCompile(`name='([^']*)'`)
	valRE := regexp.MustCompile(`value='([^']*)'`)
	for _, tag := range hiddenRE.FindAllString(html, -1) {
		nm := nameRE.FindStringSubmatch(tag)
		if nm == nil {
			continue
		}
		val := ""
		if vm := valRE.FindStringSubmatch(tag); vm != nil {
			val = vm[1]
		}
		out[nm[1]] = val
	}
	return out
}

// ClassNbrFieldIndex returns the MTG_CLASS_NBR$N field name for the row whose
// rendered value equals classNbr, so the detail POST can set ICAction to it.
func ClassNbrFieldIndex(html, classNbr string) (string, bool) {
	classNbr = strings.TrimSpace(classNbr)
	for _, loc := range classNbrRowRE.FindAllStringSubmatchIndex(html, -1) {
		idx := html[loc[2]:loc[3]]
		if elementText(html, "MTG_CLASS_NBR$"+idx) == classNbr {
			return "MTG_CLASS_NBR$" + idx, true
		}
	}
	return "", false
}

// IsOverCap reports whether a results page is the "over N classes, continue?"
// interstitial rather than a real result set.
func IsOverCap(html string) bool {
	return overCapRE.MatchString(html)
}
