// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

// schedule-replay verifies captured schedule fixtures against SearchScope
// without contacting either live LACCD endpoint.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/laccd"
)

type replayManifest struct {
	FixtureHashes map[string]string `json:"fixture_hashes"`
	Catalogs      []string          `json:"catalogs"`
	ClassNumbers  []string          `json:"class_numbers"`
}

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

	manifest, hashes, err := loadAndVerifyManifest(*fixtures)
	if err != nil {
		return err
	}
	replay, err := laccd.StartFixtureReplay(*fixtures)
	if err != nil {
		return err
	}
	defer replay.Close()

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
		FixtureHashes: hashes,
		Catalogs:      catalogs,
		ClassNumbers:  classNumbers,
	})
}

func loadAndVerifyManifest(fixtures string) (replayManifest, map[string]string, error) {
	fixtures = filepath.Clean(fixtures)
	body, err := os.ReadFile(filepath.Join(fixtures, "replay-manifest.json"))
	if err != nil {
		return replayManifest{}, nil, fmt.Errorf("read replay manifest: %w", err)
	}
	var manifest replayManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return replayManifest{}, nil, fmt.Errorf("decode replay manifest: %w", err)
	}
	if len(manifest.FixtureHashes) == 0 || len(manifest.Catalogs) == 0 || len(manifest.ClassNumbers) == 0 {
		return replayManifest{}, nil, errors.New("replay manifest must include fixture hashes, catalogs, and class numbers")
	}
	if _, ok := manifest.FixtureHashes["discovery-multimd.json"]; !ok {
		return replayManifest{}, nil, errors.New("replay manifest is missing discovery-multimd.json hash")
	}
	for _, catalog := range manifest.Catalogs {
		name := "sections-" + catalog + ".html"
		if _, ok := manifest.FixtureHashes[name]; !ok {
			return replayManifest{}, nil, fmt.Errorf("replay manifest is missing %s hash", name)
		}
	}

	hashes := make(map[string]string, len(manifest.FixtureHashes))
	for name, want := range manifest.FixtureHashes {
		if filepath.Base(name) != name {
			return replayManifest{}, nil, fmt.Errorf("invalid fixture name %q", name)
		}
		data, err := os.ReadFile(filepath.Join(fixtures, name))
		if err != nil {
			return replayManifest{}, nil, fmt.Errorf("read fixture %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, want) {
			return replayManifest{}, nil, fmt.Errorf("fixture %s SHA-256 = %s, want %s", name, got, want)
		}
		hashes[name] = got
	}
	return manifest, hashes, nil
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
