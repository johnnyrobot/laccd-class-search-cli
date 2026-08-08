// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// pp:data-source local

package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/johnnyrobot/laccd-class-search-cli/internal/store"
)

type statusFlip struct {
	ClassNbr string `json:"class_nbr"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type changedResult struct {
	Since         string       `json:"since"`
	Added         []string     `json:"added_sections"`
	Dropped       []string     `json:"dropped_sections"`
	StatusChanges []statusFlip `json:"status_changes"`
	Note          string       `json:"note,omitempty"`
}

func newNovelChangedCmd(flags *rootFlags) *cobra.Command {
	var since, college, term, subject, dbPath string

	cmd := &cobra.Command{
		Use:   "changed",
		Short: "Diff the current mirror against the previous sync to list new sections, dropped sections, and open-to-closed flips.",
		Long: strings.Trim(`
Compare the most recent observation of each section against the one closest to
--since ago, listing sections that newly appeared, sections that are no longer
seen, and sections whose status flipped (e.g. open -> closed). This needs at
least two syncs spanning the window; on a fresh mirror everything reads as new.
Optionally scope to a college/term/subject.`, "\n"),
		Example: strings.Trim(`
  laccd-class-search-pp-cli changed --since 24h
  laccd-class-search-pp-cli changed --since 7d --college LAPC --subject MATH --agent`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			cutoff, err := parseSinceDuration(orDefault(since, "24h"))
			if err != nil {
				return usageErr(fmt.Errorf("invalid --since %q: %w", since, err))
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

			snaps, err := st.SnapshotsFiltered(college, term, subject)
			if err != nil {
				return err
			}
			result := diffSnapshots(snaps, cutoff)
			result.Since = since
			if result.Since == "" {
				result.Since = "24h"
			}
			if len(snaps) == 0 {
				result.Note = "no observations recorded yet; run 'search' or 'sync' over time to build history"
			}

			if wantsHumanTable(cmd.OutOrStdout(), flags) {
				printChangedHuman(cmd, result)
				return nil
			}
			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}
	cmd.Flags().StringVar(&since, "since", "24h", "Window to look back (e.g. 24h, 7d, 1w)")
	cmd.Flags().StringVar(&college, "college", "", "Scope to a college code")
	cmd.Flags().StringVar(&term, "term", "", "Scope to a term code")
	cmd.Flags().StringVar(&subject, "subject", "", "Scope to a subject code")
	cmd.Flags().StringVar(&dbPath, "db", "", "Local database path")
	return cmd
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// diffSnapshots classifies sections as added/dropped/status-changed by comparing
// each section's latest observation to its latest observation on/before cutoff.
func diffSnapshots(snaps []store.SeatSnapshot, cutoff time.Time) changedResult {
	byClass := map[string][]store.SeatSnapshot{}
	var order []string
	for _, s := range snaps {
		if _, seen := byClass[s.ClassNbr]; !seen {
			order = append(order, s.ClassNbr)
		}
		byClass[s.ClassNbr] = append(byClass[s.ClassNbr], s)
	}
	result := changedResult{Added: []string{}, Dropped: []string{}, StatusChanges: []statusFlip{}}
	for _, nbr := range order {
		series := byClass[nbr] // ordered by observed_at from the query
		latest := series[len(series)-1]
		var lastBefore *store.SeatSnapshot
		for i := range series {
			if !series[i].ObservedAt.After(cutoff) {
				lastBefore = &series[i]
			}
		}
		earliest := series[0]
		switch {
		case !latest.ObservedAt.After(cutoff):
			// Not observed since the cutoff — dropped from recent data.
			result.Dropped = append(result.Dropped, nbr)
		case earliest.ObservedAt.After(cutoff):
			// First seen after the cutoff — newly appeared.
			result.Added = append(result.Added, nbr)
		case lastBefore != nil && !strings.EqualFold(lastBefore.Status, latest.Status):
			result.StatusChanges = append(result.StatusChanges, statusFlip{ClassNbr: nbr, From: lastBefore.Status, To: latest.Status})
		}
	}
	return result
}

func printChangedHuman(cmd *cobra.Command, r changedResult) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Changes in the last %s:\n", r.Since)
	fmt.Fprintf(w, "  new sections:     %d\n", len(r.Added))
	fmt.Fprintf(w, "  dropped sections: %d\n", len(r.Dropped))
	fmt.Fprintf(w, "  status changes:   %d\n", len(r.StatusChanges))
	for _, f := range r.StatusChanges {
		fmt.Fprintf(w, "    %s: %s -> %s\n", f.ClassNbr, f.From, f.To)
	}
	if r.Note != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), r.Note)
	}
}
