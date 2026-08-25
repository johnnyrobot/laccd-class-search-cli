// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// schedule-replay verifies captured schedule fixtures against SearchScope
// without contacting either live LACCD endpoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
)

type replayManifest = laccd.ReplayManifest

type replayReport struct {
	FixtureHashes map[string]string `json:"fixture_hashes"`
	Catalogs      []string          `json:"catalogs"`
	ClassNumbers  []string          `json:"class_numbers"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "schedule replay:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("schedule-replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	fixtures := flags.String("fixtures", "", "directory containing replay-manifest.json and captured fixtures")
	college := flags.String("college", "", "college code")
	term := flags.String("term", "", "PeopleSoft term code")
	subject := flags.String("subject", "", "subject code")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*fixtures) == "" || strings.TrimSpace(*college) == "" || strings.TrimSpace(*term) == "" || strings.TrimSpace(*subject) == "" {
		return errors.New("--fixtures, --college, --term, and --subject are required")
	}

	replay, err := laccd.StartFixtureReplay(*fixtures)
	if err != nil {
		return err
	}
	defer replay.Close()
	manifest := replay.Manifest()
	if !manifest.MatchesScope(*college, *term, *subject) {
		return fmt.Errorf("requested scope %s %s %s does not match captured replay scope %s %s %s",
			strings.ToUpper(strings.TrimSpace(*college)), strings.TrimSpace(*term), strings.ToUpper(strings.TrimSpace(*subject)),
			manifest.College, manifest.Term, manifest.Subject)
	}

	client := laccd.New(replay.URL(), 0, time.Second, laccd.WithCourseDiscovery(replay.Discovery()))
	scope, err := client.SearchScope(context.Background(), *college, *term, *subject, "", 0)
	if err != nil {
		return fmt.Errorf("SearchScope replay: %w", err)
	}
	catalogs := sortedStrings(scope.Catalogs)
	classNumbers := sortedClassNumbers(scope.Sections)
	if want := sortedStrings(manifest.Catalogs); !slices.Equal(catalogs, want) {
		return fmt.Errorf("catalog oracle mismatch: got %v, want %v", catalogs, want)
	}
	if want := sortedStrings(manifest.ClassNumbers); !slices.Equal(classNumbers, want) {
		return fmt.Errorf("class-number oracle mismatch: got %v, want %v", classNumbers, want)
	}

	return json.NewEncoder(stdout).Encode(replayReport{
		FixtureHashes: replay.FixtureHashes(),
		Catalogs:      catalogs,
		ClassNumbers:  classNumbers,
	})
}

func sortedStrings(values []string) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	return values
}

func sortedClassNumbers(sections []laccd.Section) []string {
	classNumbers := make([]string, len(sections))
	for i, section := range sections {
		classNumbers[i] = section.ClassNbr
	}
	return sortedStrings(classNumbers)
}
