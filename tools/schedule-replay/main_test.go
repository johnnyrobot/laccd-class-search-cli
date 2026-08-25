// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

	t.Run("rejects a discovered catalog outside the manifest allowlist", func(t *testing.T) {
		fixtures := copyFixtures(t)
		addDiscoveredCatalog(t, fixtures, "999")
		if err := os.WriteFile(filepath.Join(fixtures, "sections-999.html"), []byte("unhashed catalog fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run(replayArgs(fixtures), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), `discovered catalog "999" is not in the replay manifest`) {
			t.Fatalf("run error = %v, want manifest allowlist rejection before serving 999", err)
		}
	})

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "college", args: []string{"--college", "NOT-LAMC"}},
		{name: "term", args: []string{"--term", "2266"}},
		{name: "subject", args: []string{"--subject", "CINEMA"}},
	} {
		t.Run("rejects a mismatched "+tc.name, func(t *testing.T) {
			args := replayArgs(copyFixtures(t))
			for i := 0; i < len(args); i += 2 {
				if args[i] == tc.args[0] {
					args[i+1] = tc.args[1]
				}
			}
			if err := run(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "does not match captured replay scope") {
				t.Fatalf("run error = %v, want scope mismatch", err)
			}
		})
	}
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

func addDiscoveredCatalog(t *testing.T, fixtures, catalog string) {
	t.Helper()
	fixturePath := filepath.Join(fixtures, "discovery-multimd.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var listing map[string]any
	if err := json.Unmarshal(data, &listing); err != nil {
		t.Fatal(err)
	}
	subjects := listing["subjects"].([]any)
	subject := subjects[0].(map[string]any)
	courses := subject["courses"].([]any)
	subject["courses"] = append(courses, map[string]any{"catalogNbr": catalog})
	data, err = json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(fixtures, "replay-manifest.json")
	data, err = os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest replayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.FixtureHashes["discovery-multimd.json"] = fixtureHash(t, fixturePath)
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
