package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObserveExcludesOnlyExactRootManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "producer", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "producer", "alpha", "manifest.json"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	observed, err := Observe(root)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Manifest.ExactFileDenom != 1 || len(observed.Manifest.Entries) != 1 {
		t.Fatalf("unexpected denominator: %#v", observed.Manifest)
	}
	if observed.Manifest.Entries[0].Path != "producer/alpha/manifest.json" {
		t.Fatalf("unexpected entries: %#v", observed.Manifest.Entries)
	}
}

func TestValidateRejectsTraversalAndCollision(t *testing.T) {
	for _, value := range []Manifest{
		{Schema: Schema, InputRoot: ".", ExcludedPaths: []string{RootManifestPath}, ExactFileDenom: 1, ManifestEntries: 1, Entries: []Entry{{Path: "../escape", Size: 0, SHA256: zeroDigest}}},
		{Schema: Schema, InputRoot: ".", ExcludedPaths: []string{RootManifestPath}, ExactFileDenom: 2, ManifestEntries: 2, Entries: []Entry{{Path: "same", Size: 0, SHA256: zeroDigest}, {Path: "same", Size: 0, SHA256: zeroDigest}}},
	} {
		if err := ValidateManifest(value); err == nil {
			t.Fatalf("unsafe manifest accepted: %#v", value)
		}
	}
}

func TestCompareChangedInputIsUnknown(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := Observe(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claim, mismatches, err := CompareManifest(root, old.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if claim.State != UnknownState || mismatches != 1 || claim.UnknownClass == nil || claim.BlockedBy == nil {
		t.Fatalf("unexpected stale claim: %#v mismatches=%d", claim, mismatches)
	}
}

func TestConformanceReceiptHasFixedDenominator(t *testing.T) {
	input := filepath.Join("..", "..", "fixtures", "conformance", "input")
	report, err := BuildConformanceReport(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Total != 12 || report.Summary.Closed != 4 || report.Summary.Unknown != 3 || report.Summary.Refuted != 5 {
		t.Fatalf("unexpected summary: %#v", report.Summary)
	}
	if report.Decision != RefutedState || report.MetaActivityBindings != 12 {
		t.Fatalf("unexpected decision or bindings: %s/%d", report.Decision, report.MetaActivityBindings)
	}
}

const zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"
