// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"strings"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/store"
	"github.com/spf13/cobra"
)

func newSyncCmd(flags *rootFlags) *cobra.Command {
	return newSyncCmdWithScopeSearch(flags, liveSearch)
}

func newSyncCmdWithScopeSearch(flags *rootFlags, scopeSearch scopeSearchFunc) *cobra.Command {
	var college, term, subject, catalog, dbPath string
	var maxCourses int
	// Framework-sync compatibility flags. This CLI syncs by college/term/subject
	// scope rather than the generic resource model, but accepting these avoids
	// "unknown flag" errors from tooling that drives sync the framework way.
	var fwResources, fwSince string
	var fwFull, fwLatestOnly bool
	var fwMaxPages int

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync a college/term/subject's sections into the local store",
		Long: strings.Trim(`
Fetch every section for a college, term, and subject from the live portal and
upsert them into the local SQLite mirror, recording a status snapshot for each.
Run it before offline 'search --data-source local', and run it repeatedly over
time to give 'changed' and 'watch' history to diff against.

Without --catalog, sync fans out one request per course in the subject so the
mirror is complete.`, "\n"),
		Example: strings.Trim(`
  laccd-class-search-pp-cli sync --college LAPC --term 2268 --subject MATH
  laccd-class-search-pp-cli sync --college ELAC --term 2268 --subject ENGL --catalog 101`, "\n"),
		Annotations: map[string]string{"mcp:hidden": "true", "pp:happy-args": "--college=LAPC;--term=2268;--subject=MATH"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				fmt.Fprintln(cmd.OutOrStdout(), "would sync sections into the local store")
				return nil
			}
			// No scope at all: print guidance as a clean no-op (rc=0) so a bare
			// 'sync' — in any output mode — is a help-shaped result, not a
			// usage error. Partial scope (some but not all) is a real mistake.
			if college == "" && term == "" && subject == "" {
				if flags.asJSON {
					return printJSONFiltered(cmd.OutOrStdout(), map[string]any{"synced": 0, "note": "specify --college, --term, and --subject to sync"}, flags)
				}
				return cmd.Help()
			}
			if college == "" || term == "" || subject == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("--college, --term, and --subject are all required"))
			}
			if !laccd.IsCollege(college) {
				return usageErr(fmt.Errorf("unknown college %q; run '%s colleges' for valid codes", college, appName))
			}
			if maxCourses < 0 {
				return usageErr(fmt.Errorf("--max-courses must be zero or positive, got %d", maxCourses))
			}
			if strings.TrimSpace(catalog) == "" && laccd.TermLabel(term) == "" {
				return usageErr(fmt.Errorf("unsupported term %q for whole-subject schedule sync", term))
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()

			if cliutil.IsDogfoodEnv() && maxCourses > 2 {
				maxCourses = 2
			}

			scope, err := scopeSearch(ctx, flags, college, term, subject, catalog, maxCourses)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			if scope.Truncated {
				return usageErr(fmt.Errorf("sync would be incomplete after scanning %d of %d courses; pass --max-courses 0 or raise --max-courses to at least %d", scope.CoursesScanned, scope.CoursesTotal, scope.CoursesTotal))
			}
			st, err := openStore(ctx, dbPath)
			if err != nil {
				return err
			}
			defer st.Close()
			if strings.TrimSpace(catalog) == "" {
				marker := store.ScopeCompleteness{
					Version:         2,
					DiscoverySource: "laccd-schedule-api",
					Catalogs:        append([]string{}, scope.Catalogs...),
					CoursesTotal:    scope.CoursesTotal,
					CoursesScanned:  scope.CoursesScanned,
					Truncated:       scope.Truncated,
				}
				if err := persistCompleteSections(st, scheduleScopeKey(college, term, subject), scope.Sections, marker); err != nil {
					return err
				}
			} else if err := persistSections(st, scope.Sections, true); err != nil {
				return err
			}

			out := map[string]any{
				"synced":          len(scope.Sections),
				"college":         strings.ToUpper(college),
				"term":            term,
				"subject":         strings.ToUpper(subject),
				"courses_scanned": scope.CoursesScanned,
				"courses_total":   scope.CoursesTotal,
				"truncated":       scope.Truncated,
			}
			if flags.asJSON {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced %d sections for %s %s %s (scanned %d/%d courses)\n",
				len(scope.Sections), strings.ToUpper(college), strings.ToUpper(subject), term, scope.CoursesScanned, scope.CoursesTotal)
			if scope.Truncated {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: stopped at %d courses; raise --max-courses for full coverage\n", maxCourses)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&college, "college", "", "College campus code (e.g. LAPC)")
	cmd.Flags().StringVar(&college, "campus", "", "Alias for --college")
	_ = cmd.Flags().MarkHidden("campus")
	cmd.Flags().StringVar(&term, "term", "", "Term code (e.g. 2268)")
	cmd.Flags().StringVar(&subject, "subject", "", "Subject code (e.g. MATH)")
	cmd.Flags().StringVar(&catalog, "catalog", "", "Catalog number to sync one course (optional)")
	cmd.Flags().IntVar(&maxCourses, "max-courses", 50, "Max courses to scan when syncing a whole subject")
	cmd.Flags().StringVar(&dbPath, "db", "", "Local database path")
	// Framework-sync compatibility flags (accepted, mapped onto the scope model where sensible).
	cmd.Flags().StringVar(&fwResources, "resources", "", "Accepted for compatibility; this CLI syncs by --college/--term/--subject scope")
	cmd.Flags().StringVar(&fwSince, "since", "", "Accepted for compatibility (no incremental cursor for the guest portal)")
	cmd.Flags().BoolVar(&fwFull, "full", false, "Accepted for compatibility (every sync is a full scope fetch)")
	cmd.Flags().BoolVar(&fwLatestOnly, "latest-only", false, "Accepted for compatibility")
	cmd.Flags().IntVar(&fwMaxPages, "max-pages", 0, "Accepted for compatibility; use --max-courses to bound a whole-subject sync")
	for _, f := range []string{"resources", "since", "full", "latest-only", "max-pages"} {
		_ = cmd.Flags().MarkHidden(f)
	}
	return cmd
}
