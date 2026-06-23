// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// Package laccd is the hand-written client + HTML parser for the Los Angeles
// Community College District PeopleSoft guest class-search portal. It is kept
// outside the generated internal/client package so regeneration cannot clobber
// it. The list path is a replayable deep-link GET; section-detail (seat counts)
// is a stateful PeopleSoft ICAction POST.
package laccd

import (
	"sort"
	"strings"
)

// ComponentPath is the PeopleSoft class-search component path, appended to the
// base URL for every request. Kept relative so the config base URL (which the
// verify pipeline overrides to a mock server) drives the host.
const ComponentPath = "/psc/classsearchguest/EMPLOYEE/HRMS/c/COMMUNITY_ACCESS.CLASS_SEARCH.GBL"

// DefaultBaseURL is the live LACCD guest portal host. Real config.Load always
// supplies this; the constant is the fallback for direct laccd.New callers.
const DefaultBaseURL = "https://mycollege-guest.laccd.edu"

// userAgent is a real desktop Chrome UA. PeopleSoft serves the cookie-check
// shell to clients it does not recognize as browsers.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

// College is one LACCD college (PeopleSoft Campus dimension).
type College struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Colleges is the authoritative list of the 9 LACCD colleges, confirmed from
// the live portal Campus dropdown. ITV (Instructional Television) and the
// LACCD district-wide option are intentionally excluded: ITV is not a campus a
// student attends and LACCD is not a searchable college (it is the institution).
var Colleges = []College{
	{Code: "ELAC", Name: "East Los Angeles College"},
	{Code: "LACC", Name: "Los Angeles City College"},
	{Code: "LAHC", Name: "Los Angeles Harbor College"},
	{Code: "LAMC", Name: "Los Angeles Mission College"},
	{Code: "LAPC", Name: "Pierce College"},
	{Code: "LASC", Name: "Los Angeles Southwest College"},
	{Code: "LATTC", Name: "Los Angeles Trade-Technical College"},
	{Code: "LAVC", Name: "Los Angeles Valley College"},
	{Code: "WLAC", Name: "West Los Angeles College"},
}

// collegeByCode indexes Colleges for O(1) name lookups.
var collegeByCode = func() map[string]string {
	m := make(map[string]string, len(Colleges))
	for _, c := range Colleges {
		m[c.Code] = c.Name
	}
	return m
}()

// CollegeName resolves a campus code to its display name, or "" if unknown.
// Comparison is case-insensitive on the canonical upper-case code.
func CollegeName(code string) string {
	return collegeByCode[strings.ToUpper(strings.TrimSpace(code))]
}

// IsCollege reports whether code is one of the 9 searchable colleges.
func IsCollege(code string) bool {
	_, ok := collegeByCode[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}

// CollegeCodes returns the campus codes in display order.
func CollegeCodes() []string {
	out := make([]string, len(Colleges))
	for i, c := range Colleges {
		out[i] = c.Code
	}
	return out
}

// Term is a PeopleSoft STRM term code plus its human label.
type Term struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// Subject is a class-search subject area. Codes can contain spaces (e.g.
// "ADM JUS", "AFRO AM"), so they are preserved verbatim. CCN marks subjects
// introduced under California's Common Course Numbering (their description ends
// with "(CCN)") so they sort/label alongside their legacy equivalents.
type Subject struct {
	Code string `json:"code"`
	Name string `json:"name"`
	CCN  bool   `json:"ccn"`
}

// TermLabel derives a human label from a 4-digit STRM code using the standard
// California Community College convention: "2" + 2-digit year + term digit
// (2=Spring, 6=Summer, 8=Fall). Returns "" for codes that do not fit, since
// the portal also exposes non-standard intersession codes that should be read
// from the live dropdown rather than guessed.
func TermLabel(strm string) string {
	strm = strings.TrimSpace(strm)
	if len(strm) != 4 || strm[0] != '2' {
		return ""
	}
	year := 2000 + int(strm[1]-'0')*10 + int(strm[2]-'0')
	if year < 2000 || year > 2099 {
		return ""
	}
	var season string
	switch strm[3] {
	case '2':
		season = "Spring"
	case '6':
		season = "Summer"
	case '8':
		season = "Fall"
	default:
		return ""
	}
	return season + " " + itoa(year)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// InstructionModes maps the portal's "Class Search Type" attribute values to
// human labels. Confirmed from the live ATTR_VAL dropdown.
var InstructionModes = map[string]string{
	"IN-PERSON": "In Person",
	"ONLINE":    "Online (asynchronous)",
	"C19S":      "Online Live (synchronous)",
	"HYFLEX":    "HyFlex (in person or online)",
	"HYBRID":    "Hybrid (partially online)",
	"OER":       "Zero Textbook Cost (ZTC)",
	"LCT":       "Low Textbook Cost",
	"HONORS":    "Honors",
	"DUAL":      "Dual Enrollment",
	"NONCREDIT": "Noncredit",
}

// modeKeywordMap maps lower-cased free-text mode tokens (what a user passes to
// --mode) to the canonical labels used for client-side filtering against the
// instruction_mode parsed from section detail.
var modeKeywordMap = map[string][]string{
	"in-person": {"in person"},
	"inperson":  {"in person"},
	"person":    {"in person"},
	"online":    {"online"},
	"async":     {"online (asynchronous)", "asynchronous"},
	"sync":      {"online live", "synchronous"},
	"live":      {"online live"},
	"hyflex":    {"hyflex"},
	"hybrid":    {"hybrid"},
}

// MatchesMode reports whether a section's parsed instruction_mode text satisfies
// the user-supplied --mode filter token. An empty filter matches everything.
func MatchesMode(filter, instructionMode string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	mode := strings.ToLower(instructionMode)
	if mode == "" {
		return false
	}
	needles, ok := modeKeywordMap[filter]
	if !ok {
		needles = []string{filter}
	}
	for _, n := range needles {
		if strings.Contains(mode, n) {
			return true
		}
	}
	return false
}

// Section is one class section parsed from the results grid. ID mirrors
// ClassNbr so the generated store keys the row on the unique class number.
type Section struct {
	ID              string `json:"id"`
	ClassNbr        string `json:"class_nbr"`
	College         string `json:"college"`
	CollegeName     string `json:"college_name"`
	Term            string `json:"term"`
	TermLabel       string `json:"term_label"`
	Subject         string `json:"subject"`
	Catalog         string `json:"catalog"`
	Course          string `json:"course"`
	Title           string `json:"title"`
	Section         string `json:"section"`
	Component       string `json:"component"`
	Session         string `json:"session"`
	DaysTimes       string `json:"days_times"`
	Room            string `json:"room"`
	Instructor      string `json:"instructor"`
	StartDate       string `json:"start_date"`
	EndDate         string `json:"end_date"`
	Status          string `json:"status"`
	InstructionMode string `json:"instruction_mode"`
}

// SectionDetail carries the seat counts and metadata that only the section
// drill-down page exposes.
type SectionDetail struct {
	ClassNbr        string `json:"class_nbr"`
	EnrollCap       int    `json:"enrollment_capacity"`
	Enrolled        int    `json:"enrolled"`
	AvailableSeats  int    `json:"available_seats"`
	WaitlistCap     int    `json:"waitlist_capacity"`
	WaitlistTotal   int    `json:"waitlist_total"`
	Units           string `json:"units"`
	InstructionMode string `json:"instruction_mode"`
	Status          string `json:"status"`
}

// NormalizeTitle lower-cases and collapses whitespace so two catalog numbers
// offering the same course can be matched for CCN alias discovery.
func NormalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// SortSections orders sections by catalog number then section code for stable
// output. Numeric-aware on the catalog so 9 sorts before 103.
func SortSections(secs []Section) {
	sort.SliceStable(secs, func(i, j int) bool {
		ci, cj := catalogSortKey(secs[i].Catalog), catalogSortKey(secs[j].Catalog)
		if ci != cj {
			return ci < cj
		}
		if secs[i].Catalog != secs[j].Catalog {
			return secs[i].Catalog < secs[j].Catalog
		}
		return secs[i].Section < secs[j].Section
	})
}

// catalogSortKey extracts the leading integer of a catalog number for numeric
// sorting; non-numeric or suffixed numbers fall back to a large key so they
// sort after plain numbers without panicking.
func catalogSortKey(catalog string) int {
	n := 0
	found := false
	for _, r := range catalog {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		found = true
	}
	if !found {
		return 1 << 30
	}
	return n
}
