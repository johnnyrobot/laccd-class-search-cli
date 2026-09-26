// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source live

package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
	"github.com/spf13/cobra"
)

// ccnEquivalent is one (subject, catalog) that offers the same course title.
type ccnEquivalent struct {
	Subject  string `json:"subject"`
	Catalog  string `json:"catalog"`
	Course   string `json:"course"`
	Title    string `json:"title"`
	CCN      bool   `json:"ccn"`
	Sections int    `json:"sections"`
	Open     int    `json:"open"`
}

type ccnResult struct {
	Query       string          `json:"query"`
	Title       string          `json:"title"`
	College     string          `json:"college"`
	Term        string          `json:"term"`
	Equivalents []ccnEquivalent `json:"equivalents"`
	Note        string          `json:"note,omitempty"`
}

func newNovelCcnCmd(flags *rootFlags) *cobra.Command {
	var college, term string

	cmd := &cobra.Command{
		Use:   "ccn <subject> <number>",
		Short: "Resolve a legacy catalog number to its new AB1111 Common Course Number (and back), then list sections under both.",
		Long: strings.Trim(`
Find every subject/catalog number offering the same course as <subject> <number>
at a college this term — bridging California's AB1111 Common Course Numbering,
where a renumbered course can appear under a new subject code (e.g. ANTHRO vs
ANTH) or catalog number. Matching is by course title against the live schedule
and the CCN-flagged subject list, so it reflects what is actually offered rather
than a hardcoded crosswalk.`, "\n"),
		Example: strings.Trim(`
  laccd-class-search-pp-cli ccn MATH 261 --college LAPC --term 2268
  laccd-class-search-pp-cli ccn ANTHRO 101 --college ELAC --term 2268 --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true", "pp:happy-args": "<subject>=MATH;<number>=261;--college=LAPC;--term=2268"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				fmt.Fprintln(cmd.OutOrStdout(), "would resolve course-number equivalents")
				return nil
			}
			if len(args) < 2 {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("usage: ccn <subject> <number>, e.g. 'ccn MATH 261'"))
			}
			if college == "" || term == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("--college and --term are both required"))
			}
			subject := strings.ToUpper(strings.TrimSpace(args[0]))
			number := strings.TrimSpace(args[1])
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			c, err := newLaccdClient(flags)
			if err != nil {
				return err
			}

			// 1. Resolve the requested course's title.
			base, err := c.SearchSections(ctx, college, term, subject, number)
			if err != nil && err != laccd.ErrOverCap {
				return classifyAPIError(err, flags)
			}
			var title string
			for _, s := range base {
				if title == "" {
					title = s.Title
				}
			}
			result := ccnResult{
				Query:       fmt.Sprintf("%s %s", subject, number),
				College:     college,
				Term:        term,
				Equivalents: []ccnEquivalent{},
			}
			if title == "" {
				result.Note = fmt.Sprintf("%s %s is not offered at %s in term %s; cannot resolve equivalents by title", subject, number, college, term)
				return outputCCN(cmd, flags, result)
			}
			result.Title = title

			// 2. Find related subjects (the CCN/legacy pair) by base name.
			related := relatedSubjectCodes(ctx, c, college, term, subject)
			if cliutil.IsDogfoodEnv() && len(related) > 2 {
				related = related[:2]
			}

			// 3. Across related subjects, collect courses with the same title.
			wantTitle := laccd.NormalizeTitle(title)
			for _, rs := range related {
				secs := base
				if rs.code != subject {
					var serr error
					secs, serr = c.SearchSections(ctx, college, term, rs.code, "")
					if serr != nil {
						continue
					}
				}
				byCatalog := map[string]*ccnEquivalent{}
				for _, s := range secs {
					if laccd.NormalizeTitle(s.Title) != wantTitle {
						continue
					}
					eq := byCatalog[s.Catalog]
					if eq == nil {
						eq = &ccnEquivalent{Subject: rs.code, Catalog: s.Catalog, Course: s.Course, Title: s.Title, CCN: rs.ccn || isCCNCatalog(s.Catalog)}
						byCatalog[s.Catalog] = eq
					}
					eq.Sections++
					if strings.EqualFold(s.Status, "Open") {
						eq.Open++
					}
				}
				for _, eq := range byCatalog {
					result.Equivalents = append(result.Equivalents, *eq)
				}
			}

			// An equivalent carrying the queried subject+number is the course
			// the caller already has; only a different subject or catalog
			// number is an alternate. Counting rows instead of alternates
			// contradicted the payload whenever the portal returned the course
			// under its new CCN number, which is this command's whole point.
			alternates := 0
			for _, eq := range result.Equivalents {
				if !strings.EqualFold(eq.Subject, subject) || !strings.EqualFold(eq.Catalog, number) {
					alternates++
				}
			}
			if alternates == 0 {
				result.Note = "no alternate subject/catalog number offers this course this term; the renumbering may not be active yet for this college"
			}
			return outputCCN(cmd, flags, result)
		},
	}
	cmd.Flags().StringVar(&college, "college", "", "College campus code (e.g. LAPC)")
	cmd.Flags().StringVar(&term, "term", "", "Term code (e.g. 2268)")
	return cmd
}

type relatedSubject struct {
	code string
	ccn  bool
}

// ccnCatalogPattern matches an AB1111 common course number such as C1000.
// The portal only marks the *subject* as "(CCN)", so a legacy subject that
// has already been renumbered carries a CCN catalog number under a subject
// flagged false; matching the number keeps the row's ccn field honest.
var ccnCatalogPattern = regexp.MustCompile(`^C\d{3,4}$`)

func isCCNCatalog(catalog string) bool {
	return ccnCatalogPattern.MatchString(strings.ToUpper(strings.TrimSpace(catalog)))
}

// relatedSubjectCodes returns the subject codes that share a base name with the
// given subject (the CCN and legacy variants), always including the input. On
// any lookup failure it falls back to just the input subject so the command
// still resolves within-subject equivalents.
func relatedSubjectCodes(ctx context.Context, c *laccd.Client, college, term, subject string) []relatedSubject {
	out := []relatedSubject{{code: subject}}
	subs, err := c.ListSubjects(ctx, college, term)
	if err != nil || len(subs) == 0 {
		return out
	}
	baseOf := func(name string) string {
		return laccd.NormalizeTitle(strings.ReplaceAll(name, "(CCN)", ""))
	}
	var inputBase string
	for _, s := range subs {
		if s.Code == subject {
			inputBase = baseOf(s.Name)
			out[0].ccn = s.CCN
		}
	}
	if inputBase == "" {
		return out
	}
	for _, s := range subs {
		if s.Code == subject {
			continue
		}
		if baseOf(s.Name) == inputBase {
			out = append(out, relatedSubject{code: s.Code, ccn: s.CCN})
		}
	}
	return out
}

func outputCCN(cmd *cobra.Command, flags *rootFlags, r ccnResult) error {
	if wantsHumanTable(cmd.OutOrStdout(), flags) {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%s", r.Query)
		if r.Title != "" {
			fmt.Fprintf(w, " — %s", r.Title)
		}
		fmt.Fprintln(w)
		if len(r.Equivalents) > 0 {
			rows := make([]map[string]any, 0, len(r.Equivalents))
			for _, e := range r.Equivalents {
				rows = append(rows, map[string]any{
					"course": e.Course, "ccn": e.CCN, "sections": e.Sections, "open": e.Open,
				})
			}
			_ = printAutoTable(w, rows)
		}
		if r.Note != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), r.Note)
		}
		return nil
	}
	return printJSONFiltered(cmd.OutOrStdout(), r, flags)
}
