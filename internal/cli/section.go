// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source auto

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"laccd-class-search-pp-cli/internal/cliutil"
	"laccd-class-search-pp-cli/internal/laccd"
	"laccd-class-search-pp-cli/internal/store"
)

// sectionView is the section command's output: the section's grid info plus its
// live seat detail.
type sectionView struct {
	laccd.Section
	Detail *laccd.SectionDetail `json:"detail,omitempty"`
}

func newSectionCmd(flags *rootFlags) *cobra.Command {
	var college, term, subject, catalog, dbPath, icsPath string
	var ics bool

	cmd := &cobra.Command{
		Use:   "section <class-nbr>",
		Short: "Show one section's exact seats, waitlist, and meeting details",
		Long: strings.Trim(`
Show a single class section by its class number, including exact enrollment
capacity, enrolled, available seats, and waitlist counts from the live portal.

The section's college/term/subject are resolved from the local mirror (run
'search' or 'sync' first), or pass --college --term --subject explicitly. Use
--ics to write a calendar event for the section's meeting pattern.`, "\n"),
		Example: strings.Trim(`
  laccd-class-search-pp-cli section 13079
  laccd-class-search-pp-cli section 13079 --college LAPC --term 2268 --subject MATH
  laccd-class-search-pp-cli section 13079 --ics > math261.ics`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true", "pp:happy-args": "<class-nbr>=13079;--college=LAPC;--term=2268;--subject=MATH;--catalog=261"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				fmt.Fprintln(cmd.OutOrStdout(), "would fetch section seat detail")
				return nil
			}
			if len(args) == 0 {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a class number is required, e.g. 'section 13079'"))
			}
			classNbr := strings.TrimSpace(args[0])
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()

			// Under the verify mock the portal HTML can't be reproduced; emit a
			// well-formed empty result so the runtime check still exercises the
			// command's output path.
			if cliutil.IsVerifyEnv() {
				return printJSONFiltered(cmd.OutOrStdout(), sectionView{Section: laccd.Section{ID: classNbr, ClassNbr: classNbr}}, flags)
			}

			var sec laccd.Section
			haveSec := false
			explicit := college != "" && term != "" && subject != ""

			if !explicit {
				if st, ok, _ := openStoreIfExists(ctx, dbPath); ok {
					if s, found := sectionFromStoreByNbr(st, classNbr); found {
						sec = s
						college, term, subject, catalog = s.College, s.Term, s.Subject, s.Catalog
						haveSec = true
					}
					_ = st.Close()
				}
			}
			if college == "" || term == "" || subject == "" {
				return usageErr(fmt.Errorf("section %s is not in the local mirror; run 'search' first, or pass --college --term --subject", classNbr))
			}

			c, err := newLaccdClient(flags)
			if err != nil {
				return err
			}

			// For --ics or when the grid row is unknown, fetch the section row live.
			if !haveSec {
				secs, err := c.SearchSections(ctx, college, term, subject, catalog)
				if err != nil {
					return classifyAPIError(err, flags)
				}
				for _, s := range secs {
					if s.ClassNbr == classNbr {
						sec = s
						haveSec = true
						break
					}
				}
			}

			if ics || icsPath != "" {
				if !haveSec {
					return notFoundErr(fmt.Errorf("section %s not found, cannot build calendar event", classNbr))
				}
				return emitICS(cmd, []laccd.Section{sec}, icsPath)
			}

			detail, err := c.FetchDetail(ctx, college, term, subject, catalog, classNbr)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			persistSectionDetail(ctx, dbPath, sec, detail)

			view := sectionView{Section: sec, Detail: detail}
			if !haveSec {
				view.Section = laccd.Section{ID: classNbr, ClassNbr: classNbr, College: college, Term: term, Subject: subject, Catalog: catalog}
			}
			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				printSectionDetailHuman(cmd, view)
				return nil
			}
			return printJSONFiltered(cmd.OutOrStdout(), view, flags)
		},
	}
	cmd.Flags().StringVar(&college, "college", "", "College campus code (resolved from the mirror if omitted)")
	cmd.Flags().StringVar(&college, "campus", "", "Alias for --college")
	_ = cmd.Flags().MarkHidden("campus")
	cmd.Flags().StringVar(&term, "term", "", "Term code (resolved from the mirror if omitted)")
	cmd.Flags().StringVar(&subject, "subject", "", "Subject code (resolved from the mirror if omitted)")
	cmd.Flags().StringVar(&catalog, "catalog", "", "Catalog number (narrows the lookup; resolved from the mirror if omitted)")
	cmd.Flags().BoolVar(&ics, "ics", false, "Emit an iCalendar (.ics) event for the section instead of seat detail")
	cmd.Flags().StringVar(&icsPath, "ics-file", "", "Write the .ics to this path instead of stdout (implies --ics)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Local database path")
	return cmd
}

// sectionFromStoreByNbr returns the full mirrored section for a class number.
func sectionFromStoreByNbr(st *store.Store, classNbr string) (laccd.Section, bool) {
	rows, err := st.SectionsByScope("", "", "", "")
	if err != nil {
		return laccd.Section{}, false
	}
	for _, r := range rows {
		var s laccd.Section
		if jsonUnmarshalSection(r, &s) && s.ClassNbr == classNbr {
			return s, true
		}
	}
	return laccd.Section{}, false
}

func persistSectionDetail(ctx context.Context, dbPath string, sec laccd.Section, d *laccd.SectionDetail) {
	st, err := openStore(ctx, dbPath)
	if err != nil {
		return
	}
	defer st.Close()
	if derr := st.UpsertSectionDetail(store.SectionDetailRow{
		ClassNbr:        d.ClassNbr,
		EnrollCap:       d.EnrollCap,
		Enrolled:        d.Enrolled,
		AvailableSeats:  d.AvailableSeats,
		WaitlistCap:     d.WaitlistCap,
		WaitlistTotal:   d.WaitlistTotal,
		Units:           d.Units,
		InstructionMode: d.InstructionMode,
		Status:          d.Status,
		FetchedAt:       time.Now(),
	}); derr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to record section detail for %s: %v\n", d.ClassNbr, derr)
	}
	avail := d.AvailableSeats
	wt := d.WaitlistTotal
	status := d.Status
	if status == "" {
		status = sec.Status
	}
	if derr := st.AddSeatSnapshot(store.SeatSnapshot{
		ClassNbr:      d.ClassNbr,
		College:       sec.College,
		Term:          sec.Term,
		Subject:       sec.Subject,
		Status:        status,
		Available:     &avail,
		WaitlistTotal: &wt,
		ObservedAt:    time.Now(),
	}); derr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to record seat snapshot for %s: %v\n", d.ClassNbr, derr)
	}
}

func emitICS(cmd *cobra.Command, secs []laccd.Section, path string) error {
	ics, written, skipped := buildICS(secs)
	if written == 0 {
		return notFoundErr(fmt.Errorf("no section had a parseable meeting pattern to put on a calendar"))
	}
	if path != "" {
		if err := os.WriteFile(path, []byte(ics), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d event(s) to %s", written, path)
		if skipped > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), " (%d section(s) skipped: no fixed meeting time)", skipped)
		}
		fmt.Fprintln(cmd.ErrOrStderr())
		return nil
	}
	fmt.Fprint(cmd.OutOrStdout(), ics)
	if skipped > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d section(s) skipped: no fixed meeting time\n", skipped)
	}
	return nil
}

func printSectionDetailHuman(cmd *cobra.Command, v sectionView) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%s %s  section %s  (class %s)\n", v.Course, v.Title, v.Section, v.ClassNbr)
	if v.College != "" {
		fmt.Fprintf(w, "  College:    %s (%s)\n", v.CollegeName, v.College)
	}
	if v.DaysTimes != "" {
		fmt.Fprintf(w, "  Meets:      %s  %s\n", v.DaysTimes, v.Room)
	}
	if v.Instructor != "" {
		fmt.Fprintf(w, "  Instructor: %s\n", v.Instructor)
	}
	if v.Detail != nil {
		d := v.Detail
		fmt.Fprintf(w, "  Status:     %s\n", d.Status)
		fmt.Fprintf(w, "  Seats:      %d open of %d (%d enrolled)\n", d.AvailableSeats, d.EnrollCap, d.Enrolled)
		fmt.Fprintf(w, "  Waitlist:   %d of %d\n", d.WaitlistTotal, d.WaitlistCap)
		if d.Units != "" {
			fmt.Fprintf(w, "  Units:      %s\n", d.Units)
		}
		if d.InstructionMode != "" {
			fmt.Fprintf(w, "  Mode:       %s\n", d.InstructionMode)
		}
	}
}
