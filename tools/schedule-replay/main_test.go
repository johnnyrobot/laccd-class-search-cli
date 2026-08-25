// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunReplaysManifestAndRejectsInvalidFixtureInputs(t *testing.T) {
	t.Run("prints the manifest oracle", func(t *testing.T) {
		fixtures := copyFixtures(t)
		var stdout bytes.Buffer
		if err := run(replayArgs(fixtures), &stdout); err != nil {
			t.Fatal(err)
		}
		var got replayReport
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("decode report: %v", err)
		}
		if want := []string{"100", "110", "125"}; !slices.Equal(got.Catalogs, want) {
			t.Fatalf("catalogs = %v, want %v", got.Catalogs, want)
		}
		if want := []string{"17300", "17310", "17490", "17491"}; !slices.Equal(got.ClassNumbers, want) {
			t.Fatalf("class numbers = %v, want %v", got.ClassNumbers, want)
		}
		if len(got.FixtureHashes) != 4 {
			t.Fatalf("fixture hashes = %v, want four entries", got.FixtureHashes)
		}
	})

	t.Run("rejects a fixture hash mismatch", func(t *testing.T) {
		fixtures := copyFixtures(t)
		path := filepath.Join(fixtures, "sections-100.html")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run(replayArgs(fixtures), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "SHA-256") {
			t.Fatalf("run error = %v, want SHA-256 mismatch", err)
		}
	})

	t.Run("rejects missing catalog HTML", func(t *testing.T) {
		fixtures := copyFixtures(t)
		if err := os.Remove(filepath.Join(fixtures, "sections-125.html")); err != nil {
			t.Fatal(err)
		}
		if err := run(replayArgs(fixtures), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "sections-125.html") {
			t.Fatalf("run error = %v, want missing catalog fixture", err)
		}
	})

	t.Run("rejects an oracle mismatch", func(t *testing.T) {
		fixtures := copyFixtures(t)
		path := filepath.Join(fixtures, "replay-manifest.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(data), "17300", "00000", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run(replayArgs(fixtures), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "class-number oracle mismatch") {
			t.Fatalf("run error = %v, want class-number oracle mismatch", err)
		}
	})
}

func replayArgs(fixtures string) []string {
	return []string{"--fixtures", fixtures, "--college", "LAMC", "--term", "2268", "--subject", "MULTIMD"}
}

func copyFixtures(t *testing.T) string {
	t.Helper()
	source := filepath.Join("..", "..", "internal", "laccd", "testdata", "completeness")
	destination := filepath.Join(t.TempDir(), "completeness")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"discovery-multimd.json",
		"replay-manifest.json",
		"sections-100.html",
		"sections-110.html",
		"sections-125.html",
	} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatalf("read source fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(destination, name), data, 0o600); err != nil {
			t.Fatalf("copy fixture %s: %v", name, err)
		}
	}
	return destination
}
