// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source live

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"laccd-class-search-pp-cli/internal/laccd"
)

func newTermsCmd(flags *rootFlags) *cobra.Command {
	var college string
	cmd := &cobra.Command{
		Use:   "terms",
		Short: "List the currently-active term codes",
		Long: "List the term (STRM) codes the portal currently accepts, e.g. 2268 = Fall 2026. " +
			"Only currently-registerable terms are exposed; the list is district-wide. " +
			"--college is accepted for convenience but does not change the result.",
		Example:     "  laccd-class-search-pp-cli terms",
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			if flags.dataSource == "local" {
				return usageErr(fmt.Errorf("terms is fetched live from the portal; --data-source local has no stored equivalent"))
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			c, err := newLaccdClient(flags)
			if err != nil {
				return err
			}
			terms, err := c.ListTerms(ctx)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			if terms == nil {
				terms = []laccd.Term{}
			}
			if len(terms) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: the portal returned no active terms")
			}
			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				rows := make([]map[string]any, 0, len(terms))
				for _, t := range terms {
					rows = append(rows, map[string]any{"code": t.Code, "label": t.Label})
				}
				return printAutoTable(cmd.OutOrStdout(), rows)
			}
			return printJSONFiltered(cmd.OutOrStdout(), terms, flags)
		},
	}
	cmd.Flags().StringVar(&college, "college", "", "College campus code (accepted for convenience; term list is district-wide)")
	cmd.Flags().StringVar(&college, "campus", "", "Alias for --college")
	_ = cmd.Flags().MarkHidden("campus")
	return cmd
}
