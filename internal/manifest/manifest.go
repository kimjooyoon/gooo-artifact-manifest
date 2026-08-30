package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	Schema              = "gooo/artifact-manifest/v1"
	RootManifestPath    = "manifest.json"
	RootReadmePath      = "README.md"
	UnknownState        = "UNKNOWN"
	RefutedState        = "REFUTED"
	ClosedState         = "CLOSED"
)

// Entry is one observed regular file. Path is always a slash-separated path
// relative to the observed input root.
type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest is canonical JSON: entries are sorted by Path, and the only
// excluded relative path is the exact root manifest path.
type Manifest struct {
	Schema          string   `json:"schema"`
	InputRoot       string   `json:"input_root"`
	ExcludedPaths   []string `json:"excluded_paths"`
	ExactFileDenom  int      `json:"exact_file_denominator"`
	ManifestEntries int      `json:"manifest_entries"`
	Entries         []Entry  `json:"entries"`
}

// Metrics records both the manifest denominator and the source inventory.
// README.md at the observed root is intentionally absent from inventory
// counters, while it remains a normal manifest entry.
type Metrics struct {
	ExactFileDenominator int   `json:"exact_file_denominator"`
	ManifestEntries      int   `json:"manifest_entries"`
	Mismatches           int   `json:"mismatches"`
	WallMS               int64 `json:"wall_ms"`
	PeakRSSKiB           int64 `json:"peak_rss_kib"`
	InputFiles           int   `json:"input_files"`
	InputDirectories     int   `json:"input_directories"`
	PhysicalLines        int   `json:"physical_lines"`
	GoFiles              int   `json:"go_files"`
	GoLines              int   `json:"go_lines"`
	GoooFiles            int   `json:"gooo_files"`
	GoooLines            int   `json:"gooo_lines"`
	RootReadmeExcluded   bool  `json:"root_readme_excluded"`
}

// Observation is the result of observing one input root. Observe never
// writes to Root; output is written only by ObserveTo after the walk ends.
type Observation struct {
	Manifest Manifest `json:"manifest"`
	Metrics  Metrics  `json:"metrics"`
}

// Claim is the common state tuple. UNKNOWN claims must populate all six
// coordinates: Stage, Step, Reason, UnknownClass, NextOperation, BlockedBy.
type Claim struct {
	State        string   `json:"state"`
	Stage        *string  `json:"stage"`
	Step         *string  `json:"step"`
	Reason       string   `json:"reason"`
	UnknownClass *string  `json:"unknown_class"`
	NextOperation string  `json:"next_operation"`
	BlockedBy    []string `json:"blocked_by"`
}

// BoundaryError is a structured observation boundary failure. Missing input
// is UNKNOWN; malformed, unsafe, or untrusted input is REFUTED.
type BoundaryError struct {
	Claim Claim
}

func (e *BoundaryError) Error() string {
	return fmt.Sprintf("artifact manifest boundary: %s (%s)", e.Claim.State, e.Claim.Reason)
}

func closedClaim(reason string) Claim {
	return Claim{State: ClosedState, Reason: reason, NextOperation: "NONE", BlockedBy: []string{}}
}

func unknownClaim(stage, step, reason, class, next string, blockedBy ...string) Claim {
	stageValue, stepValue, classValue := stage, step, class
	return Claim{
		State: UnknownState, Stage: &stageValue, Step: &stepValue, Reason: reason,
		UnknownClass: &classValue, NextOperation: next, BlockedBy: append([]string{}, blockedBy...),
	}
}

func refutedClaim(stage, step, reason, next string) Claim {
	stageValue, stepValue := stage, step
	return Claim{
		State: RefutedState, Stage: &stageValue, Step: &stepValue, Reason: reason,
		NextOperation: next, BlockedBy: []string{},
	}
}

func boundary(claim Claim) *BoundaryError { return &BoundaryError{Claim: claim} }

// Observe walks root without following symbolic links and hashes each regular
// file. It never treats any existing manifest as authoritative.
func Observe(root string) (Observation, error) {
	result := Observation{Manifest: Manifest{
		Schema: Schema, InputRoot: ".", ExcludedPaths: []string{RootManifestPath}, Entries: []Entry{},
	}, Metrics: Metrics{RootReadmeExcluded: true}}

	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return result, boundary(unknownClaim("INPUT", "OBSERVE_INPUT_DIRECTORY", "INPUT_DIRECTORY_NOT_FOUND", "DIRECT_MISSING", "PROVIDE_INPUT_DIRECTORY", "input_directory"))
	}
	if err != nil {
		return result, boundary(refutedClaim("INPUT", "OBSERVE_INPUT_DIRECTORY", "INPUT_DIRECTORY_UNREADABLE", "REPAIR_INPUT_ACCESS"))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return result, boundary(refutedClaim("INPUT", "VALIDATE_INPUT_DIRECTORY", "INPUT_DIRECTORY_SYMLINK", "SELECT_REAL_INPUT_DIRECTORY"))
	}
	if !info.IsDir() {
		return result, boundary(refutedClaim("INPUT", "VALIDATE_INPUT_DIRECTORY", "INPUT_NOT_DIRECTORY", "SELECT_INPUT_DIRECTORY"))
	}

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative, err = safeRelativePath(relative)
		if err != nil {
			return boundary(refutedClaim("INPUT", "NORMALIZE_RELATIVE_PATH", "PATH_TRAVERSAL_REFUTED", "REMOVE_UNSAFE_PATH"))
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return boundary(refutedClaim("INPUT", "WALK_INPUT_TREE", "SYMBOLIC_LINK_REFUTED", "REMOVE_SYMBOLIC_LINK"))
		}
		if entry.IsDir() {
			result.Metrics.InputDirectories++
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return boundary(refutedClaim("INPUT", "VALIDATE_REGULAR_FILE", "NON_REGULAR_FILE_REFUTED", "REMOVE_NON_REGULAR_FILE"))
		}

		if relative != RootManifestPath {
			digest, size, err := digestFile(path)
			if err != nil {
				return err
			}
			result.Manifest.Entries = append(result.Manifest.Entries, Entry{Path: relative, Size: size, SHA256: digest})
		}

		if relative == RootReadmePath {
			return nil
		}
		result.Metrics.InputFiles++
		lines, err := physicalLines(path)
		if err != nil {
			return err
		}
		result.Metrics.PhysicalLines += lines
		switch strings.ToLower(filepath.Ext(relative)) {
		case ".go":
			result.Metrics.GoFiles++
			result.Metrics.GoLines += lines
		case ".gooo":
			result.Metrics.GoooFiles++
			result.Metrics.GoooLines += lines
		}
		return nil
	})
	if walkErr != nil {
		var boundaryError *BoundaryError
		if errors.As(walkErr, &boundaryError) {
			return result, boundaryError
		}
		return result, boundary(refutedClaim("INPUT", "WALK_INPUT_TREE", "INPUT_TRAVERSAL_FAILED", "REPAIR_INPUT_ACCESS"))
	}

	sort.Slice(result.Manifest.Entries, func(left, right int) bool {
		return result.Manifest.Entries[left].Path < result.Manifest.Entries[right].Path
	})
	result.Manifest.ExactFileDenom = len(result.Manifest.Entries)
	result.Manifest.ManifestEntries = len(result.Manifest.Entries)
	result.Metrics.ExactFileDenominator = len(result.Manifest.Entries)
	result.Metrics.ManifestEntries = len(result.Manifest.Entries)
	if err := ValidateManifest(result.Manifest); err != nil {
		return result, err
	}
	return result, nil
}

// ObserveTo observes root before opening output. output is caller-owned; no
// parent directories are invented and no input file is used as a manifest
// source. A root output named ./manifest.json is excluded by exact path.
func ObserveTo(root, output string) (Observation, error) {
	result, err := Observe(root)
	if err != nil {
		return result, err
	}
	if output == "" {
		return result, nil
	}
	if err := WriteManifest(output, result.Manifest); err != nil {
		return result, err
	}
	return result, nil
}

// WriteManifest emits stable, indented JSON with a terminal newline.
func WriteManifest(output string, value Manifest) error {
	if err := ValidateManifest(value); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open caller-owned output: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return fmt.Errorf("write caller-owned output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close caller-owned output: %w", err)
	}
	return nil
}

// ValidateManifest rejects unsafe paths, duplicate/colliding entries, and
// non-canonical ordering. It does not read any file content.
func ValidateManifest(value Manifest) error {
	if value.Schema != Schema {
		return boundary(refutedClaim("MANIFEST", "VALIDATE_SCHEMA", "MANIFEST_SCHEMA_REFUTED", "PROVIDE_CANONICAL_MANIFEST"))
	}
	if len(value.ExcludedPaths) != 1 || value.ExcludedPaths[0] != RootManifestPath {
		return boundary(refutedClaim("MANIFEST", "VALIDATE_EXCLUSIONS", "EXCLUSION_SET_REFUTED", "USE_EXACT_ROOT_MANIFEST_EXCLUSION"))
	}
	if value.ExactFileDenom != len(value.Entries) || value.ManifestEntries != len(value.Entries) {
		return boundary(refutedClaim("MANIFEST", "VALIDATE_DENOMINATOR", "MANIFEST_DENOMINATOR_REFUTED", "REGENERATE_CANONICAL_MANIFEST"))
	}
	previous := ""
	for index, entry := range value.Entries {
		if _, err := safeRelativePath(entry.Path); err != nil || entry.Path == RootManifestPath {
			return boundary(refutedClaim("MANIFEST", "VALIDATE_ENTRY_PATH", "UNSAFE_MANIFEST_PATH_REFUTED", "REGENERATE_CANONICAL_MANIFEST"))
		}
		if index > 0 && entry.Path <= previous {
			return boundary(refutedClaim("MANIFEST", "VALIDATE_ENTRY_ORDER", "DUPLICATE_OR_COLLISION_REFUTED", "REGENERATE_SORTED_MANIFEST"))
		}
		if entry.Size < 0 || len(entry.SHA256) != sha256.Size*2 {
			return boundary(refutedClaim("MANIFEST", "VALIDATE_ENTRY_DIGEST", "MANIFEST_ENTRY_DIGEST_REFUTED", "REGENERATE_CANONICAL_MANIFEST"))
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return boundary(refutedClaim("MANIFEST", "VALIDATE_ENTRY_DIGEST", "MANIFEST_ENTRY_DIGEST_REFUTED", "REGENERATE_CANONICAL_MANIFEST"))
		}
		previous = entry.Path
	}
	return nil
}

// CompareManifest compares a claimed manifest with a fresh read-only
// observation. Missing or changed observed files are UNKNOWN (stale input);
// extra claimed files are REFUTED as laundering because they were not
// observed at all.
func CompareManifest(root string, claimed Manifest) (Claim, int, error) {
	if err := ValidateManifest(claimed); err != nil {
		var boundaryError *BoundaryError
		if errors.As(err, &boundaryError) {
			return boundaryError.Claim, 0, nil
		}
		return Claim{}, 0, err
	}
	observed, err := Observe(root)
	if err != nil {
		var boundaryError *BoundaryError
		if errors.As(err, &boundaryError) {
			return boundaryError.Claim, 0, nil
		}
		return Claim{}, 0, err
	}
	observedByPath := make(map[string]Entry, len(observed.Manifest.Entries))
	for _, entry := range observed.Manifest.Entries {
		observedByPath[entry.Path] = entry
	}
	claimedByPath := make(map[string]Entry, len(claimed.Entries))
	for _, entry := range claimed.Entries {
		claimedByPath[entry.Path] = entry
	}

	mismatches := 0
	for _, entry := range claimed.Entries {
		current, ok := observedByPath[entry.Path]
		if !ok {
			return refutedClaim("EVIDENCE", "COMPARE_MANIFEST_ENTRIES", "MANIFEST_LAUNDERING_REFUTED", "REMOVE_UNOBSERVED_ENTRY"), mismatches + 1, nil
		}
		if current.Size != entry.Size || current.SHA256 != entry.SHA256 {
			mismatches++
		}
	}
	for _, entry := range observed.Manifest.Entries {
		if _, ok := claimedByPath[entry.Path]; !ok {
			mismatches++
		}
	}
	if mismatches > 0 {
		return unknownClaim("EVIDENCE", "COMPARE_MANIFEST_ENTRIES", "STALE_INPUT_EVIDENCE", "STALE_INPUT", "REFRESH_INPUT_AND_MANIFEST", "input_snapshot", "manifest_snapshot"), mismatches, nil
	}
	return closedClaim("MANIFEST_MATCHED_OBSERVED_INPUT"), 0, nil
}

func safeRelativePath(value string) (string, error) {
	if value == "" || filepath.IsAbs(value) {
		return "", errors.New("manifest path is empty or absolute")
	}
	if strings.Contains(value, "\x00") {
		return "", errors.New("manifest path contains NUL")
	}
	slashed := filepath.ToSlash(value)
	for _, part := range strings.Split(slashed, "/") {
		if part == ".." {
			return "", errors.New("manifest path escapes input root")
		}
	}
	value = filepath.ToSlash(filepath.Clean(value))
	if value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return "", errors.New("manifest path escapes input root")
	}
	return value, nil
}

func digestFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat %q: %w", path, err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", 0, fmt.Errorf("hash %q: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func physicalLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open lines %q: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return 0, fmt.Errorf("read lines %q: %w", path, err)
	}
	if len(data) == 0 {
		return 0, nil
	}
	lines := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines, nil
}
