// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source auto

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
)

// searchResult is the search command's JSON envelope. The sections array is a
// named key so agents can narrow with --select sections.class_nbr,... and the
// scan metadata is visible alongside the rows.
type searchResult struct {
	Sections       []laccd.Section `json:"sections"`
	Count          int             `json:"count"`
	Source         string          `json:"source"`
	ScannedCourses int             `json:"scanned_courses,omitempty"`
	CoursesTotal   int             `json:"courses_total,omitempty"`
	Truncated      bool            `json:"truncated,omitempty"`
	Note           string          `json:"note,omitempty"`
}

func newSearchCmd(flags *rootFlags) *cobra.Command {
	var college, term, subject, catalog, mode, instructor, days, dbPath string
	var openOnly bool
	var limit, maxCourses int

	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search LACCD class sections by college, term, and subject",
		Long: strings.Trim(`
Search class sections at one LACCD college for a term and subject, with optional
client-side filters for open seats, instruction mode, instructor, and meeting
days. Pass --catalog to get every section of one course; without it the command
fans out one request per course in the subject so the result is complete.

Results are mirrored into the local SQLite store, so later runs with
--data-source local work offline.`, "\n"),
		Example: strings.Trim(`
  laccd-class-search-pp-cli search --college LAPC --term 2268 --subject MATH --catalog 261
  laccd-class-search-pp-cli search --college ELAC --term 2268 --subject ENGL --open --agent
  laccd-class-search-pp-cli search --college LAVC --term 2268 --subject PSYCH --mode online --select sections.class_nbr,sections.status`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only": "true",
			"pp:happy-args": "--college=LAPC;--term=2268;--subject=MATH;--catalog=261",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				fmt.Fprintln(cmd.OutOrStdout(), "would search LACCD class sections")
				return nil
			}
			if college == "" || term == "" || subject == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("--college, --term, and --subject are all required"))
			}
			if !laccd.IsCollege(college) {
				return usageErr(fmt.Errorf("unknown college %q; run '%s colleges' for valid codes", college, appName))
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()

			if cliutil.IsDogfoodEnv() && maxCourses > 2 {
				maxCourses = 2
			}

			result := searchResult{Source: "live"}
			filters := sectionFilters{openOnly: openOnly, mode: mode, instructor: instructor, days: days}

			switch flags.dataSource {
			case "local":
				secs, err := readLocalSearch(ctx, dbPath, college, term, subject, catalog)
				if err != nil {
					return err
				}
				if secs == nil {
					missingMirrorHint(cmd, flags, fmt.Sprintf("--college %s --term %s --subject %s", college, term, subject))
					return nil
				}
				result.Sections = applySectionFilters(secs, filters)
				result.Source = "local"
			default: // auto, live
				scope, err := liveSearch(ctx, flags, college, term, subject, catalog, maxCourses)
				if err != nil {
					if flags.dataSource == "auto" {
						if secs, lerr := readLocalSearch(ctx, dbPath, college, term, subject, catalog); lerr == nil && secs != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "live search failed (%v); serving local mirror\n", err)
							result.Sections = applySectionFilters(secs, filters)
							result.Source = "local"
							break
						}
					}
					return classifyAPIError(err, flags)
				}
				result.Sections = applySectionFilters(scope.Sections, filters)
				result.ScannedCourses = scope.CoursesScanned
				result.CoursesTotal = scope.CoursesTotal
				result.Truncated = scope.Truncated
				// Persist the unfiltered live set so the mirror stays complete.
				if st, err := openStore(ctx, dbPath); err == nil {
					_ = persistSections(st, scope.Sections, true)
					_ = st.Close()
				}
			}

			if limit > 0 && len(result.Sections) > limit {
				result.Sections = result.Sections[:limit]
			}
			result.Count = len(result.Sections)
			if result.Truncated {
				result.Note = fmt.Sprintf("scanned %d of %d courses; raise --max-courses for full coverage", result.ScannedCourses, result.CoursesTotal)
			}

			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				printSectionsHuman(cmd, result.Sections)
				printSearchSummary(cmd, result)
				return nil
			}
			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}
	cmd.Flags().StringVar(&college, "college", "", "College campus code (e.g. LAPC, ELAC). Run 'colleges' to list.")
	cmd.Flags().StringVar(&college, "campus", "", "Alias for --college")
	_ = cmd.Flags().MarkHidden("campus")
	cmd.Flags().StringVar(&term, "term", "", "Term code (e.g. 2268 = Fall 2026). Run 'terms' to list.")
	cmd.Flags().StringVar(&subject, "subject", "", "Subject code (e.g. MATH, ENGL, CHEM).")
	cmd.Flags().StringVar(&subject, "subj", "", "Alias for --subject")
	_ = cmd.Flags().MarkHidden("subj")
	cmd.Flags().StringVar(&catalog, "catalog", "", "Catalog number for one course (e.g. 261). Omit to search the whole subject.")
	cmd.Flags().BoolVar(&openOnly, "open", false, "Show only sections with open seats")
	cmd.Flags().StringVar(&mode, "mode", "", "Instruction mode filter: in-person, online, online-live, hybrid, hyflex")
	cmd.Flags().StringVar(&instructor, "instructor", "", "Filter by instructor name (substring match)")
	cmd.Flags().StringVar(&days, "days", "", "Filter by meeting days, e.g. MW or TuTh")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum sections to return (0 = no limit)")
	cmd.Flags().IntVar(&maxCourses, "max-courses", 30, "Max courses to scan when searching a whole subject (no --catalog)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Local database path")
	return cmd
}

// readLocalSearch reads matching sections from the mirror, returning nil (not an
// empty slice) when no mirror exists so the caller can print the sync hint.
func readLocalSearch(ctx context.Context, dbPath, college, term, subject, catalog string) ([]laccd.Section, error) {
	st, ok, err := openStoreIfExists(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	defer st.Close()
	secs, err := sectionsFromStore(st, college, term, subject, catalog)
	if err != nil {
		return nil, err
	}
	if secs == nil {
		secs = []laccd.Section{}
	}
	return secs, nil
}

func liveSearch(ctx context.Context, flags *rootFlags, college, term, subject, catalog string, maxCourses int) (laccd.ScopeResult, error) {
	c, err := newLaccdClient(flags)
	if err != nil {
		return laccd.ScopeResult{}, err
	}
	return c.SearchScope(ctx, college, term, subject, catalog, maxCourses)
}

func printSectionsHuman(cmd *cobra.Command, secs []laccd.Section) {
	if len(secs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no sections found")
		return
	}
	rows := make([]map[string]any, 0, len(secs))
	for _, s := range secs {
		rows = append(rows, map[string]any{
			"class":      s.ClassNbr,
			"course":     s.Course,
			"section":    s.Section,
			"days_times": s.DaysTimes,
			"instructor": s.Instructor,
			"status":     s.Status,
		})
	}
	raw, _ := json.Marshal(rows)
	var items []map[string]any
	_ = json.Unmarshal(raw, &items)
	_ = printAutoTable(cmd.OutOrStdout(), items)
}

func printSearchSummary(cmd *cobra.Command, r searchResult) {
	src := r.Source
	if r.CoursesTotal > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d sections (%s, scanned %d/%d courses)\n", r.Count, src, r.ScannedCourses, r.CoursesTotal)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d sections (%s)\n", r.Count, src)
	}
	if r.Note != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), r.Note)
	}
}
