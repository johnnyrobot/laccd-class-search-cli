// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source live

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
)

func newSubjectsCmd(flags *rootFlags) *cobra.Command {
	var college, term string
	cmd := &cobra.Command{
		Use:   "subjects",
		Short: "List the subject codes offered at a college for a term",
		Long: "List the subject area codes available for a college and term, with their names. " +
			"Codes can contain spaces (e.g. 'ADM JUS'); pass them verbatim to 'search --subject'. " +
			"Subjects marked ccn use California's Common Course Numbering.",
		Example:     "  laccd-class-search-pp-cli subjects --college LAPC --term 2268",
		Annotations: map[string]string{"mcp:read-only": "true", "pp:happy-args": "--college=LAPC;--term=2268"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().NFlag() == 0 && len(args) == 0 && !flags.dryRun {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return nil
			}
			if college == "" || term == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("--college and --term are both required"))
			}
			if !laccd.IsCollege(college) {
				return usageErr(fmt.Errorf("unknown college %q; run '%s colleges' for valid codes", college, appName))
			}
			if flags.dataSource == "local" {
				return usageErr(fmt.Errorf("subjects is fetched live from the portal; --data-source local has no stored equivalent"))
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			c, err := newLaccdClient(flags)
			if err != nil {
				return err
			}
			subs, err := c.ListSubjects(ctx, college, term)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			if subs == nil {
				subs = []laccd.Subject{}
			}
			if len(subs) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: no subjects returned for %s %s (check the term code with '%s terms')\n", college, term, appName)
			}
			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				rows := make([]map[string]any, 0, len(subs))
				for _, s := range subs {
					rows = append(rows, map[string]any{"code": s.Code, "name": s.Name, "ccn": s.CCN})
				}
				return printAutoTable(cmd.OutOrStdout(), rows)
			}
			return printJSONFiltered(cmd.OutOrStdout(), subs, flags)
		},
	}
	cmd.Flags().StringVar(&college, "college", "", "College campus code (e.g. LAPC). Run 'colleges' to list.")
	cmd.Flags().StringVar(&college, "campus", "", "Alias for --college")
	_ = cmd.Flags().MarkHidden("campus")
	cmd.Flags().StringVar(&term, "term", "", "Term code (e.g. 2268). Run 'terms' to list.")
	return cmd
}
