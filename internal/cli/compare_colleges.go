// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source live

package cli

import (
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
)

// collegeAvailability summarizes one college's offerings of a course.
type collegeAvailability struct {
	College  string `json:"college"`
	Name     string `json:"college_name"`
	Sections int    `json:"sections"`
	Open     int    `json:"open"`
	Waitlist int    `json:"waitlist"`
	Closed   int    `json:"closed"`
}

type fetchFailure struct {
	College string `json:"college"`
	Error   string `json:"error"`
}

type compareResult struct {
	Subject       string                `json:"subject"`
	Catalog       string                `json:"catalog"`
	Term          string                `json:"term"`
	Colleges      []collegeAvailability `json:"colleges"`
	TotalOpen     int                   `json:"total_open"`
	FetchFailures []fetchFailure        `json:"fetch_failures"`
}

func newNovelCompareCollegesCmd(flags *rootFlags) *cobra.Command {
	var subject, catalog, term string

	cmd := &cobra.Command{
		Use:   "compare-colleges",
		Short: "Compare one course across all 9 LACCD colleges and see open-seat counts side by side.",
		Long: strings.Trim(`
Compare a single course (subject + catalog number) across all 9 LACCD colleges
for a term, showing how many sections each college offers and how many are open.
Because every college shares one PeopleSoft institution, this answers "where can
I still get into this course?" in one command — something the portal, which
searches one college at a time, cannot do.`, "\n"),
		Example: strings.Trim(`
  laccd-class-search-pp-cli compare-colleges --subject MATH --catalog 261 --term 2268
  laccd-class-search-pp-cli compare-colleges --subject ENGL --catalog 101 --term 2268 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true", "pp:happy-args": "--subject=MATH;--catalog=261;--term=2268"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().NFlag() == 0 && len(args) == 0 && !flags.dryRun {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				fmt.Fprintln(cmd.OutOrStdout(), "would compare a course across LACCD colleges")
				return nil
			}
			if subject == "" || catalog == "" || term == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("--subject, --catalog, and --term are all required"))
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			c, err := newFanoutClient(flags)
			if err != nil {
				return err
			}

			codes := laccd.CollegeCodes()
			type item struct {
				idx     int
				college string
				avail   collegeAvailability
				err     error
			}
			results := make(chan item, len(codes))
			var wg sync.WaitGroup
			for idx, code := range codes {
				wg.Add(1)
				go func(idx int, code string) {
					defer wg.Done()
					secs, err := c.SearchSections(ctx, code, term, subject, catalog)
					if err == laccd.ErrOverCap {
						err = nil // treat over-cap as "no narrow result" rather than a hard failure
					}
					if err != nil {
						results <- item{idx: idx, college: code, err: err}
						return
					}
					av := collegeAvailability{College: code, Name: laccd.CollegeName(code), Sections: len(secs)}
					for _, s := range secs {
						switch strings.ToLower(s.Status) {
						case "open":
							av.Open++
						case "wait list":
							av.Waitlist++
						case "closed":
							av.Closed++
						}
					}
					results <- item{idx: idx, college: code, avail: av}
				}(idx, code)
			}
			go func() { wg.Wait(); close(results) }()

			ordered := make([]item, len(codes))
			for r := range results {
				ordered[r.idx] = r
			}

			out := compareResult{Subject: strings.ToUpper(subject), Catalog: catalog, Term: term, FetchFailures: []fetchFailure{}, Colleges: []collegeAvailability{}}
			for _, r := range ordered {
				if r.err != nil {
					out.FetchFailures = append(out.FetchFailures, fetchFailure{College: r.college, Error: r.err.Error()})
					continue
				}
				// Include colleges that offer 0 sections so all fetched colleges
				// appear — distinguishing "doesn't offer this course" from a
				// silently dropped fetch.
				out.Colleges = append(out.Colleges, r.avail)
				out.TotalOpen += r.avail.Open
			}

			if len(out.FetchFailures) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %d of %d colleges failed to fetch; totals cover the remaining %d\n",
					len(out.FetchFailures), len(codes), len(codes)-len(out.FetchFailures))
			}

			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				if len(out.Colleges) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "no college offers %s %s in term %s\n", out.Subject, out.Catalog, out.Term)
					return nil
				}
				rows := make([]map[string]any, 0, len(out.Colleges))
				for _, a := range out.Colleges {
					rows = append(rows, map[string]any{
						"college": a.College, "name": a.Name,
						"sections": a.Sections, "open": a.Open, "waitlist": a.Waitlist, "closed": a.Closed,
					})
				}
				if err := printAutoTable(cmd.OutOrStdout(), rows); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%d open section(s) of %s %s across the district\n", out.TotalOpen, out.Subject, out.Catalog)
				return nil
			}
			return printJSONFiltered(cmd.OutOrStdout(), out, flags)
		},
	}
	cmd.Flags().StringVar(&subject, "subject", "", "Subject code (e.g. MATH)")
	cmd.Flags().StringVar(&catalog, "catalog", "", "Catalog number (e.g. 261)")
	cmd.Flags().StringVar(&term, "term", "", "Term code (e.g. 2268)")
	return cmd
}
