// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source local

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"laccd-class-search-pp-cli/internal/laccd"
)

func newCollegesCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "colleges",
		Short:       "List the 9 LACCD colleges and their campus codes",
		Long:        "List the 9 Los Angeles Community College District colleges with the campus codes used by --college. This is static reference data and needs no network access.",
		Example:     "  laccd-class-search-pp-cli colleges",
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			if flags.dataSource == "live" {
				return usageErr(fmt.Errorf("colleges is static reference data; --data-source live has no effect"))
			}
			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				rows := make([]map[string]any, 0, len(laccd.Colleges))
				for _, c := range laccd.Colleges {
					rows = append(rows, map[string]any{"code": c.Code, "name": c.Name})
				}
				return printAutoTable(cmd.OutOrStdout(), rows)
			}
			return printJSONFiltered(cmd.OutOrStdout(), laccd.Colleges, flags)
		},
	}
	return cmd
}
