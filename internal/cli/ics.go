// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"strings"
	"time"

	"laccd-class-search-pp-cli/internal/laccd"
)

// dayToICS maps PeopleSoft 2-letter meeting-day abbreviations to RFC 5545 BYDAY
// codes.
var dayToICS = map[string]string{
	"Mo": "MO", "Tu": "TU", "We": "WE", "Th": "TH", "Fr": "FR", "Sa": "SA", "Su": "SU",
}

// buildICS renders a VCALENDAR of weekly-recurring events for the given
// sections. Sections whose meeting pattern cannot be parsed (TBA, online async)
// are skipped with a count returned so the caller can report them honestly.
func buildICS(secs []laccd.Section) (ics string, written, skipped int) {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//laccd-class-search-pp-cli//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	for _, s := range secs {
		ev, ok := sectionVEvent(s)
		if !ok {
			skipped++
			continue
		}
		b.WriteString(ev)
		written++
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String(), written, skipped
}

// sectionVEvent builds a single VEVENT for a section. Returns ok=false when the
// meeting days/times or date range are not parseable.
func sectionVEvent(s laccd.Section) (string, bool) {
	days, startClock, endClock, ok := parseMeeting(s.DaysTimes)
	if !ok {
		return "", false
	}
	startDate, ok := parseUSDate(s.StartDate)
	if !ok {
		return "", false
	}
	endDate, ok := parseUSDate(s.EndDate)
	if !ok {
		endDate = startDate.AddDate(0, 4, 0)
	}
	// First occurrence: the first requested weekday on/after the start date.
	first := firstOccurrence(startDate, days)
	dtStart := time.Date(first.Year(), first.Month(), first.Day(), startClock.h, startClock.m, 0, 0, time.Local)
	dtEnd := time.Date(first.Year(), first.Month(), first.Day(), endClock.h, endClock.m, 0, 0, time.Local)
	until := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 23, 59, 0, 0, time.Local)

	byday := make([]string, 0, len(days))
	for _, d := range days {
		byday = append(byday, dayToICS[d])
	}

	var b strings.Builder
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(&b, "UID:%s@laccd-class-search-pp-cli\r\n", s.ClassNbr)
	fmt.Fprintf(&b, "SUMMARY:%s %s (%s)\r\n", icsEscape(s.Course), icsEscape(s.Title), icsEscape(s.Section))
	fmt.Fprintf(&b, "DTSTART:%s\r\n", dtStart.Format("20060102T150405"))
	fmt.Fprintf(&b, "DTEND:%s\r\n", dtEnd.Format("20060102T150405"))
	fmt.Fprintf(&b, "RRULE:FREQ=WEEKLY;BYDAY=%s;UNTIL=%s\r\n", strings.Join(byday, ","), until.Format("20060102T150405"))
	if s.Room != "" {
		fmt.Fprintf(&b, "LOCATION:%s\r\n", icsEscape(s.Room))
	}
	desc := strings.TrimSpace(fmt.Sprintf("Class %s — %s. %s", s.ClassNbr, s.Instructor, s.Status))
	fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", icsEscape(desc))
	b.WriteString("END:VEVENT\r\n")
	return b.String(), true
}

type clock struct{ h, m int }

// parseMeeting parses "MoTuWeTh 8:00AM - 9:10AM" into day codes and start/end
// clock times. Returns ok=false for TBA / no-time patterns.
func parseMeeting(dt string) (days []string, start, end clock, ok bool) {
	dt = strings.TrimSpace(dt)
	if dt == "" || strings.Contains(strings.ToUpper(dt), "TBA") {
		return nil, clock{}, clock{}, false
	}
	parts := strings.Fields(dt)
	if len(parts) < 2 {
		return nil, clock{}, clock{}, false
	}
	dayStr := parts[0]
	for i := 0; i+2 <= len(dayStr); i += 2 {
		code := dayStr[i : i+2]
		if _, valid := dayToICS[code]; valid {
			days = append(days, code)
		}
	}
	if len(days) == 0 {
		return nil, clock{}, clock{}, false
	}
	rest := strings.Join(parts[1:], " ")
	timeParts := strings.SplitN(rest, "-", 2)
	if len(timeParts) != 2 {
		return nil, clock{}, clock{}, false
	}
	start, ok1 := parseClock(strings.TrimSpace(timeParts[0]))
	end, ok2 := parseClock(strings.TrimSpace(timeParts[1]))
	if !ok1 || !ok2 {
		return nil, clock{}, clock{}, false
	}
	return days, start, end, true
}

func parseClock(s string) (clock, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"3:04PM", "3:04 PM", "15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return clock{h: t.Hour(), m: t.Minute()}, true
		}
	}
	return clock{}, false
}

func parseUSDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("01/02/2006", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func firstOccurrence(start time.Time, days []string) time.Time {
	want := map[time.Weekday]bool{}
	wd := map[string]time.Weekday{"Mo": time.Monday, "Tu": time.Tuesday, "We": time.Wednesday, "Th": time.Thursday, "Fr": time.Friday, "Sa": time.Saturday, "Su": time.Sunday}
	for _, d := range days {
		want[wd[d]] = true
	}
	for i := 0; i < 7; i++ {
		day := start.AddDate(0, 0, i)
		if want[day.Weekday()] {
			return day
		}
	}
	return start
}

func icsEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n")
	return r.Replace(s)
}
