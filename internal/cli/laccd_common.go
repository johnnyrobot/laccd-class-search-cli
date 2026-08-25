// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/config"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/store"
	"github.com/spf13/cobra"
)

const appName = "laccd-class-search-pp-cli"

type scopeSearchFunc func(context.Context, *rootFlags, string, string, string, string, int) (laccd.ScopeResult, error)

// newLaccdClient builds the LACCD portal client from config (so the verify
// pipeline's base-URL override is honored) and the root rate-limit flag.
func newLaccdClient(flags *rootFlags) (*laccd.Client, error) {
	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return nil, configErr(err)
	}
	return laccd.New(cfg.BaseURL, flags.rateLimit, flags.timeout), nil
}

// newFanoutClient builds a client with a higher floor rate for same-host
// cross-college fan-outs (compare-colleges, ccn), where the per-college requests
// share one limiter and the strict 2 rps default would serialize them too
// slowly. The portal showed no throttling across rapid requests; the limiter
// still backs off on any 429.
func newFanoutClient(flags *rootFlags) (*laccd.Client, error) {
	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return nil, configErr(err)
	}
	rate := flags.rateLimit
	if rate > 0 && rate < 6 {
		rate = 6
	}
	return laccd.New(cfg.BaseURL, rate, flags.timeout), nil
}

// resolveDBPath returns the explicit --db value or the canonical default.
func resolveDBPath(dbFlag string) string {
	if strings.TrimSpace(dbFlag) != "" {
		return dbFlag
	}
	return defaultDBPath(appName)
}

// openStore opens (creating if needed) the local SQLite mirror.
func openStore(ctx context.Context, dbFlag string) (*store.Store, error) {
	return store.OpenWithContext(ctx, resolveDBPath(dbFlag))
}

// openStoreIfExists opens the mirror only if the file is already present,
// returning ok=false otherwise so read commands can emit the "run sync" hint
// instead of creating an empty database.
func openStoreIfExists(ctx context.Context, dbFlag string) (*store.Store, bool, error) {
	path := resolveDBPath(dbFlag)
	if _, err := os.Stat(path); err != nil {
		return nil, false, nil
	}
	st, err := openStore(ctx, dbFlag)
	if err != nil {
		return nil, false, err
	}
	return st, true, nil
}

// persistSections upserts sections into the mirror and, when snapshot is true,
// appends a grid-level seat snapshot (status only — exact seats need detail).
func persistSections(st *store.Store, secs []laccd.Section, snapshot bool) error {
	if st == nil || len(secs) == 0 {
		return nil
	}
	items := make([]json.RawMessage, 0, len(secs))
	for _, s := range secs {
		raw, err := json.Marshal(s)
		if err != nil {
			return err
		}
		items = append(items, raw)
	}
	if _, _, err := st.UpsertBatch("sections", items); err != nil {
		return err
	}
	if snapshot {
		now := time.Now()
		for _, s := range secs {
			_ = st.AddSeatSnapshot(store.SeatSnapshot{
				ClassNbr:   s.ClassNbr,
				College:    s.College,
				Term:       s.Term,
				Subject:    s.Subject,
				Status:     s.Status,
				ObservedAt: now,
			})
		}
	}
	return nil
}

// scheduleScopeKey returns the canonical sync_state key for one whole-subject
// schedule mirror. Direct-catalog rows deliberately do not get their own
// completeness marker.
func scheduleScopeKey(college, term, subject string) string {
	return "sections:" + strings.ToUpper(strings.TrimSpace(college)) + ":" + strings.TrimSpace(term) + ":" + strings.ToUpper(strings.TrimSpace(subject))
}

// persistCompleteSections stores every section row, its grid-level snapshot,
// and the marker authorizing future whole-subject local reads in one
// transaction.
func persistCompleteSections(st *store.Store, scopeKey string, secs []laccd.Section, marker store.ScopeCompleteness) error {
	if st == nil {
		return fmt.Errorf("persist complete sections: nil store")
	}
	items := make([]json.RawMessage, 0, len(secs))
	snapshots := make([]store.SeatSnapshot, 0, len(secs))
	syncedAt := marker.SyncedAt
	if syncedAt.IsZero() {
		syncedAt = time.Now().UTC()
	}
	marker.SyncedAt = syncedAt
	for _, section := range secs {
		raw, err := json.Marshal(section)
		if err != nil {
			return fmt.Errorf("marshal section %s: %w", section.ClassNbr, err)
		}
		items = append(items, raw)
		snapshots = append(snapshots, store.SeatSnapshot{
			ClassNbr:   section.ClassNbr,
			College:    section.College,
			Term:       section.Term,
			Subject:    section.Subject,
			Status:     section.Status,
			ObservedAt: syncedAt,
		})
	}
	return st.SaveCompleteScheduleScope(scopeKey, items, snapshots, marker)
}

// readCompleteLocalScope reads a whole-subject mirror only when its current
// completeness marker proves full coverage. The marker's catalog array is the
// authority for bounded selection, including catalogs that produced no rows.
func readCompleteLocalScope(ctx context.Context, dbPath, college, term, subject string, maxCourses int) ([]laccd.Section, store.ScopeCompleteness, bool, error) {
	var marker store.ScopeCompleteness
	if maxCourses < 0 {
		return nil, marker, false, fmt.Errorf("maxCourses must be zero or positive, got %d", maxCourses)
	}
	st, ok, err := openStoreIfExists(ctx, dbPath)
	if err != nil || !ok {
		return nil, marker, false, err
	}
	defer st.Close()

	rows, marker, ok, err := st.ReadCompleteScheduleScope(ctx, scheduleScopeKey(college, term, subject))
	if err != nil || !ok {
		return nil, marker, false, err
	}
	selected := marker.Catalogs
	if maxCourses > 0 && maxCourses < len(selected) {
		selected = selected[:maxCourses]
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, catalog := range selected {
		selectedSet[strings.ToUpper(strings.TrimSpace(catalog))] = true
	}

	secs := sectionsFromRows(rows)
	bounded := make([]laccd.Section, 0, len(secs))
	for _, section := range secs {
		if selectedSet[strings.ToUpper(strings.TrimSpace(section.Catalog))] {
			bounded = append(bounded, section)
		}
	}
	marker.Catalogs = append([]string{}, selected...)
	marker.CoursesScanned = len(selected)
	marker.Truncated = marker.CoursesScanned < marker.CoursesTotal
	return bounded, marker, true, nil
}

// jsonUnmarshalSection decodes a stored section row, reporting success so
// callers in packages that don't import encoding/json stay clean.
func jsonUnmarshalSection(r json.RawMessage, s *laccd.Section) bool {
	return json.Unmarshal(r, s) == nil
}

// sectionsFromStore reads sections matching a scope from the mirror.
func sectionsFromStore(st *store.Store, college, term, subject, catalog string) ([]laccd.Section, error) {
	rows, err := st.SectionsByScope(college, term, subject, catalog)
	if err != nil {
		return nil, err
	}
	return sectionsFromRows(rows), nil
}

func sectionsFromRows(rows []json.RawMessage) []laccd.Section {
	out := make([]laccd.Section, 0, len(rows))
	for _, r := range rows {
		var s laccd.Section
		if err := json.Unmarshal(r, &s); err != nil {
			continue
		}
		out = append(out, s)
	}
	laccd.SortSections(out)
	return out
}

// resolveSectionScope finds a section's (college, term, subject, catalog) from
// the mirror by class number, so detail/watch commands can rebuild the
// results-page deep link. ok=false when the section is not in the mirror.
func resolveSectionScope(st *store.Store, classNbr string) (college, term, subject, catalog string, ok bool) {
	rows, err := st.SectionsByScope("", "", "", "")
	if err != nil {
		return "", "", "", "", false
	}
	for _, r := range rows {
		var s laccd.Section
		if err := json.Unmarshal(r, &s); err != nil {
			continue
		}
		if s.ClassNbr == classNbr {
			return s.College, s.Term, s.Subject, s.Catalog, true
		}
	}
	return "", "", "", "", false
}

// applySectionFilters keeps only sections matching the client-side filters that
// the deep-link GET cannot express (open-only, instruction mode, instructor,
// meeting days). All filters are case-insensitive substring/equality checks.
type sectionFilters struct {
	openOnly   bool
	mode       string
	instructor string
	days       string
}

func applySectionFilters(secs []laccd.Section, f sectionFilters) []laccd.Section {
	out := make([]laccd.Section, 0, len(secs))
	instr := strings.ToLower(strings.TrimSpace(f.instructor))
	days := strings.ToLower(strings.TrimSpace(f.days))
	for _, s := range secs {
		if f.openOnly && !strings.EqualFold(s.Status, "Open") {
			continue
		}
		if instr != "" && !strings.Contains(strings.ToLower(s.Instructor), instr) {
			continue
		}
		if days != "" && !matchesDays(s.DaysTimes, days) {
			continue
		}
		if f.mode != "" && !laccd.MatchesMode(f.mode, s.InstructionMode) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// matchesDays reports whether a section's "MoTuWeTh 8:00AM - 9:10AM" meeting
// string contains every requested day token (e.g. "mw" -> Mo and We).
func matchesDays(daysTimes, filter string) bool {
	dt := strings.ToLower(daysTimes)
	for _, tok := range splitDayTokens(filter) {
		if !strings.Contains(dt, tok) {
			return false
		}
	}
	return true
}

// splitDayTokens turns "mw" or "mo,we" into the lowercase 2-letter PeopleSoft
// day abbreviations the meeting string uses (mo tu we th fr sa su).
func splitDayTokens(filter string) []string {
	filter = strings.ToLower(strings.NewReplacer(",", "", " ", "").Replace(filter))
	single := map[byte]string{'m': "mo", 't': "tu", 'w': "we", 'r': "th", 'f': "fr", 's': "sa", 'u': "su"}
	twoLetter := map[string]bool{"mo": true, "tu": true, "we": true, "th": true, "fr": true, "sa": true, "su": true}
	var out []string
	for i := 0; i < len(filter); {
		if i+2 <= len(filter) && twoLetter[filter[i:i+2]] {
			out = append(out, filter[i:i+2])
			i += 2
			continue
		}
		if d, ok := single[filter[i]]; ok {
			out = append(out, d)
		}
		i++
	}
	return out
}

// missingMirrorHint prints the standard "no local data, run sync" guidance and
// emits an empty JSON array for machine consumers.
func missingMirrorHint(cmd *cobra.Command, flags *rootFlags, scope string) {
	fmt.Fprintf(cmd.ErrOrStderr(), "no local data for %s; run: %s sync %s\n", scope, appName, scope)
	if flags.asJSON || flags.agent {
		fmt.Fprintln(cmd.OutOrStdout(), "[]")
	}
}

// dedupeStrings returns the unique values preserving first-seen order.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// sortColleges returns the college codes in canonical order for deterministic
// cross-college output.
func sortedCollegeCodes(codes []string) []string {
	order := map[string]int{}
	for i, c := range laccd.CollegeCodes() {
		order[c] = i
	}
	out := append([]string(nil), codes...)
	sort.SliceStable(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out
}

// parseSinceDuration parses a --since value (7d, 24h, 1w) into a cutoff time.
func parseSinceDuration(since string) (time.Time, error) {
	d, err := cliutil.ParseDurationLoose(since)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-d), nil
}
