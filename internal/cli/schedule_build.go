// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source local

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"laccd-class-search-pp-cli/internal/laccd"
)

type scheduleConflict struct {
	A    string `json:"a"`
	B    string `json:"b"`
	Days string `json:"shared_days"`
}

type scheduleResult struct {
	ClassNbrs    []string           `json:"class_nbrs"`
	Resolved     []laccd.Section    `json:"resolved"`
	Unknown      []string           `json:"unknown,omitempty"`
	ConflictFree bool               `json:"conflict_free"`
	Conflicts    []scheduleConflict `json:"conflicts"`
	Note         string             `json:"note,omitempty"`
}

func newNovelScheduleBuildCmd(flags *rootFlags) *cobra.Command {
	var dbPath, icsPath string
	var ics bool

	cmd := &cobra.Command{
		Use:   "build <class-nbr>...",
		Short: "Given a set of class numbers, report the conflict-free combination or the exact overlapping pairs.",
		Long: strings.Trim(`
Check whether a set of sections fits together without meeting-time overlaps.
Pass two or more class numbers; the command loads each from the local mirror
(run 'search' first) and reports either that the set is conflict-free or the
exact pairs that overlap and on which days. Use --ics to export the schedule.`, "\n"),
		Example: strings.Trim(`
  laccd-class-search-pp-cli schedule build 13079 14512 15003
  laccd-class-search-pp-cli schedule build 13079 14512 --ics > schedule.ics`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				fmt.Fprintln(cmd.OutOrStdout(), "would check a schedule for time conflicts")
				return nil
			}
			if len(args) < 2 {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("provide at least two class numbers, e.g. 'schedule build 13079 14512'"))
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()

			st, ok, err := openStoreIfExists(ctx, dbPath)
			if err != nil {
				return err
			}
			if !ok {
				missingMirrorHint(cmd, flags, "--college <c> --term <t> --subject <s>")
				return nil
			}
			defer st.Close()

			result := scheduleResult{ClassNbrs: args, Conflicts: []scheduleConflict{}, Resolved: []laccd.Section{}}
			for _, nbr := range args {
				if s, found := sectionFromStoreByNbr(st, strings.TrimSpace(nbr)); found {
					result.Resolved = append(result.Resolved, s)
				} else {
					result.Unknown = append(result.Unknown, nbr)
				}
			}
			if len(result.Unknown) > 0 {
				result.Note = fmt.Sprintf("%d class number(s) not in the mirror and excluded from the conflict check: %s — run 'search' to add them",
					len(result.Unknown), strings.Join(result.Unknown, ", "))
			}

			result.Conflicts = detectConflicts(result.Resolved)
			result.ConflictFree = len(result.Conflicts) == 0 && len(result.Resolved) >= 2

			if ics || icsPath != "" {
				return emitICS(cmd, result.Resolved, icsPath)
			}

			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				printScheduleHuman(cmd, result)
				return nil
			}
			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}
	cmd.Flags().BoolVar(&ics, "ics", false, "Emit an iCalendar (.ics) file for the schedule instead of the conflict report")
	cmd.Flags().StringVar(&icsPath, "ics-file", "", "Write the .ics to this path instead of stdout (implies --ics)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Local database path")
	return cmd
}

// detectConflicts returns every pair of sections that share a meeting day with
// overlapping time ranges. Sections without a parseable meeting time (TBA,
// online async) never conflict.
func detectConflicts(secs []laccd.Section) []scheduleConflict {
	type meeting struct {
		days       map[string]bool
		start, end int // minutes from midnight
		ok         bool
	}
	meetings := make([]meeting, len(secs))
	for i, s := range secs {
		days, start, end, ok := parseMeeting(s.DaysTimes)
		if !ok {
			continue
		}
		m := meeting{days: map[string]bool{}, start: start.h*60 + start.m, end: end.h*60 + end.m, ok: true}
		for _, d := range days {
			m.days[d] = true
		}
		meetings[i] = m
	}
	out := []scheduleConflict{}
	for i := 0; i < len(secs); i++ {
		for j := i + 1; j < len(secs); j++ {
			mi, mj := meetings[i], meetings[j]
			if !mi.ok || !mj.ok {
				continue
			}
			var shared []string
			for d := range mi.days {
				if mj.days[d] {
					shared = append(shared, d)
				}
			}
			if len(shared) == 0 {
				continue
			}
			if mi.start < mj.end && mj.start < mi.end {
				out = append(out, scheduleConflict{
					A:    fmt.Sprintf("%s (%s %s)", secs[i].ClassNbr, secs[i].Course, secs[i].Section),
					B:    fmt.Sprintf("%s (%s %s)", secs[j].ClassNbr, secs[j].Course, secs[j].Section),
					Days: strings.Join(shared, ""),
				})
			}
		}
	}
	return out
}

func printScheduleHuman(cmd *cobra.Command, r scheduleResult) {
	w := cmd.OutOrStdout()
	for _, s := range r.Resolved {
		fmt.Fprintf(w, "  %s  %s %s  %s  (%s)\n", s.ClassNbr, s.Course, s.Section, s.DaysTimes, s.Status)
	}
	if len(r.Conflicts) == 0 {
		if len(r.Resolved) >= 2 {
			fmt.Fprintln(w, "conflict-free: these sections do not overlap")
		}
	} else {
		fmt.Fprintf(w, "%d conflict(s):\n", len(r.Conflicts))
		for _, c := range r.Conflicts {
			fmt.Fprintf(w, "  %s overlaps %s on %s\n", c.A, c.B, c.Days)
		}
	}
	if r.Note != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), r.Note)
	}
}
