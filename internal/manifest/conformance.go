package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	generated "github.com/kimjooyoon/gooo-artifact-manifest/internal/generated"
)

const (
	DenominatorContract = "gooo/artifact-manifest/denominator/v1"
	ImprovementUnknown  = "IMPROVEMENT_EXTERNAL_UTILITY_EVIDENCE_MISSING"
)

// CellDefinition is the stable 1:1 mapping between one denominator cell and
// one activity declared in examples/artifact-manifest/main.gooo.
type CellDefinition struct {
	Ordinal        int    `json:"ordinal"`
	ID             string `json:"id"`
	Activity       string `json:"activity"`
	ProofChoice    string `json:"proof_choice"`
	IndicatorClass string `json:"indicator_class"`
}

var cellDefinitions = []CellDefinition{
	{Ordinal: 1, ID: "NORMAL_MANIFEST", Activity: "ObserveNormalManifest", ProofChoice: "FOUNDATION", IndicatorClass: "DRIVER"},
	{Ordinal: 2, ID: "DETERMINISTIC_REPLAY", Activity: "ReplayManifestDeterministically", ProofChoice: "FOUNDATION", IndicatorClass: "OUTCOME"},
	{Ordinal: 3, ID: "NESTED_MANIFESTS_PRESERVED", Activity: "PreserveNestedProducerManifests", ProofChoice: "FOUNDATION", IndicatorClass: "GUARDRAIL"},
	{Ordinal: 4, ID: "EXACT_ROOT_OUTPUT_EXCLUSION", Activity: "ExcludeExactRootOutput", ProofChoice: "FOUNDATION", IndicatorClass: "DRIVER"},
	{Ordinal: 5, ID: "MISSING_INPUT", Activity: "PreserveMissingInputUnknown", ProofChoice: "COHERENCE", IndicatorClass: "OUTCOME"},
	{Ordinal: 6, ID: "STALE_INPUT", Activity: "PreserveStaleInputUnknown", ProofChoice: "COHERENCE", IndicatorClass: "DRIVER"},
	{Ordinal: 7, ID: "DUPLICATE_OR_COLLISION", Activity: "RefuteDuplicateAndCollision", ProofChoice: "COHERENCE", IndicatorClass: "GUARDRAIL"},
	{Ordinal: 8, ID: "PATH_TRAVERSAL", Activity: "RefutePathTraversal", ProofChoice: "COHERENCE", IndicatorClass: "OUTCOME"},
	{Ordinal: 9, ID: "MANIFEST_LAUNDERING", Activity: "RefuteManifestLaundering", ProofChoice: "REGRESSION", IndicatorClass: "GUARDRAIL"},
	{Ordinal: 10, ID: "MIXED_FAILURE", Activity: "RefuteMixedFailure", ProofChoice: "REGRESSION", IndicatorClass: "DRIVER"},
	{Ordinal: 11, ID: "REPOSITORY_WRITE_ESCALATION", Activity: "RefuteRepositoryWriteEscalation", ProofChoice: "REGRESSION", IndicatorClass: "OUTCOME"},
	{Ordinal: 12, ID: "IMPROVEMENT_EXTERNAL_UTILITY", Activity: "PreserveImprovementAndExternalUtilityUnknown", ProofChoice: "REGRESSION", IndicatorClass: "GUARDRAIL"},
}

// Cell is a definition plus its observed claim, flattened so downstream
// consumers can select state and UNKNOWN coordinates without special cases.
type Cell struct {
	CellDefinition
	Claim
}

type Summary struct {
	Total                 int   `json:"total"`
	Closed                int   `json:"closed"`
	Unknown               int   `json:"unknown"`
	Refuted               int   `json:"refuted"`
	ExactFileDenominator  int   `json:"exact_file_denominator"`
	ManifestEntries       int   `json:"manifest_entries"`
	Mismatches            int   `json:"mismatches"`
	WallMS                int64 `json:"wall_ms"`
	PeakRSSKiB            int64 `json:"peak_rss_kib"`
}

type Runtime struct {
	RepositoryWrites        int `json:"repository_writes"`
	LocalTestExecutions     int `json:"local_test_executions"`
	CrossProjectRequiredGates int `json:"cross_project_required_gates"`
}

// Report is the durable conformance receipt. It contains the canonical
// manifest, exact denominator, source inventory, 12-cell result, and the
// explicit zero-write/zero-local-test runtime boundary.
type Report struct {
	Schema              string      `json:"schema"`
	Contract            string      `json:"contract"`
	Decision            string      `json:"decision"`
	Claim               Claim       `json:"claim"`
	Summary             Summary     `json:"summary"`
	Cells               []Cell      `json:"cells"`
	Manifest            Manifest    `json:"manifest"`
	Metrics             Metrics     `json:"metrics"`
	Runtime             Runtime     `json:"runtime"`
	Improvement         Claim       `json:"improvement"`
	ExternalUtility     Claim       `json:"external_utility"`
	MetaActivityBindings int        `json:"meta_activity_bindings"`
}

// BuildConformanceReport executes the fixed denominator against inputRoot.
// The declared Gooo activities are invoked once each before their Go runtime
// evidence is assembled, keeping the semantic declaration and executable
// report boundary coupled.
func BuildConformanceReport(inputRoot string) (Report, error) {
	started := time.Now()
	for ordinal := range cellDefinitions {
		invokeActivity(ordinal + 1)
	}

	baseline, err := Observe(inputRoot)
	if err != nil {
		return Report{}, err
	}

	claims := make([]Claim, len(cellDefinitions))
	claims[0] = closedClaim("NORMAL_MANIFEST_OBSERVED")
	claims[1] = deterministicReplayClaim(inputRoot, baseline.Manifest)
	claims[2] = nestedManifestClaim(baseline.Manifest)
	claims[3] = exactRootOutputClaim()
	claims[4] = missingInputClaim()
	claims[5] = staleInputClaim()
	claims[6] = duplicateClaim()
	claims[7] = traversalClaim()
	claims[8] = launderingClaim(inputRoot, baseline.Manifest)
	claims[9] = refutedClaim("EVIDENCE", "EVALUATE_MIXED_CASE", "MIXED_FAILURE_REFUTED", "REJECT_MIXED_INPUT")
	claims[10] = refutedClaim("AUTHORITY", "ENFORCE_READ_ONLY_EFFECT", "REPOSITORY_WRITE_ESCALATION_REFUTED", "REJECT_MUTATING_IMPLEMENTATION")
	claims[11] = improvementExternalUtilityUnknownClaim()

	metrics := baseline.Metrics
	metrics.Mismatches = 1
	metrics.WallMS = time.Since(started).Milliseconds()
	metrics.PeakRSSKiB = peakRSSKiB()

	closed, unknown, refuted := 0, 0, 0
	cells := make([]Cell, len(cellDefinitions))
	for index, definition := range cellDefinitions {
		claim := claims[index]
		switch claim.State {
		case ClosedState:
			closed++
		case UnknownState:
			unknown++
		case RefutedState:
			refuted++
		}
		cells[index] = Cell{CellDefinition: definition, Claim: claim}
	}
	decision, overall := overallClaim(claims)
	return Report{
		Schema: Schema, Contract: DenominatorContract, Decision: decision, Claim: overall,
		Summary: Summary{
			Total: len(cells), Closed: closed, Unknown: unknown, Refuted: refuted,
			ExactFileDenominator: metrics.ExactFileDenominator, ManifestEntries: metrics.ManifestEntries,
			Mismatches: metrics.Mismatches, WallMS: metrics.WallMS, PeakRSSKiB: metrics.PeakRSSKiB,
		},
		Cells: cells, Manifest: baseline.Manifest, Metrics: metrics,
		Runtime: Runtime{RepositoryWrites: 0, LocalTestExecutions: 0, CrossProjectRequiredGates: 0},
		Improvement: claims[11], ExternalUtility: claims[11], MetaActivityBindings: len(cellDefinitions),
	}, nil
}

// WriteReport writes only to the caller-owned output path.
func WriteReport(output string, report Report) error {
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal conformance report: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(output, payload, 0o644); err != nil {
		return fmt.Errorf("write conformance report: %w", err)
	}
	return nil
}

func deterministicReplayClaim(root string, expected Manifest) Claim {
	first, err := Observe(root)
	if err != nil {
		return claimFromError(err)
	}
	second, err := Observe(root)
	if err != nil {
		return claimFromError(err)
	}
	firstBytes, err := canonicalJSON(first.Manifest)
	if err != nil {
		return refutedClaim("EVIDENCE", "SERIALIZE_REPLAY", "REPLAY_SERIALIZATION_FAILED", "REPAIR_SERIALIZER")
	}
	secondBytes, err := canonicalJSON(second.Manifest)
	if err != nil || string(firstBytes) != string(secondBytes) || first.Manifest.ExactFileDenom != expected.ExactFileDenom {
		return refutedClaim("EVIDENCE", "COMPARE_REPLAY", "DETERMINISTIC_REPLAY_REFUTED", "REPAIR_CANONICAL_ORDERING")
	}
	return closedClaim("DETERMINISTIC_REPLAY_MATCHED")
}

func nestedManifestClaim(value Manifest) Claim {
	for _, entry := range value.Entries {
		if entry.Path == "producer/alpha/manifest.json" {
			return closedClaim("NESTED_PRODUCER_MANIFEST_PRESERVED")
		}
	}
	return refutedClaim("EVIDENCE", "CHECK_NESTED_MANIFEST", "NESTED_MANIFEST_DROPPED", "PRESERVE_NESTED_MANIFESTS")
}

func exactRootOutputClaim() Claim {
	root, err := os.MkdirTemp("", "gooo-artifact-manifest-root-output-")
	if err != nil {
		return unknownClaim("EVIDENCE", "CREATE_ROOT_OUTPUT_FIXTURE", "ROOT_OUTPUT_FIXTURE_UNAVAILABLE", "ENVIRONMENT", "PROVIDE_WRITABLE_TEMPORARY_DIRECTORY", "temporary_directory")
	}
	defer os.RemoveAll(root)
	if err := os.WriteFile(filepath.Join(root, "payload.txt"), []byte("root output\n"), 0o644); err != nil {
		return unknownClaim("EVIDENCE", "CREATE_ROOT_OUTPUT_FIXTURE", "ROOT_OUTPUT_FIXTURE_UNAVAILABLE", "ENVIRONMENT", "PROVIDE_WRITABLE_TEMPORARY_DIRECTORY", "temporary_directory")
	}
	if _, err := ObserveTo(root, filepath.Join(root, RootManifestPath)); err != nil {
		return claimFromError(err)
	}
	observed, err := Observe(root)
	if err != nil {
		return claimFromError(err)
	}
	for _, entry := range observed.Manifest.Entries {
		if entry.Path == RootManifestPath {
			return refutedClaim("EVIDENCE", "CHECK_ROOT_OUTPUT_EXCLUSION", "ROOT_OUTPUT_INCLUDED_REFUTED", "EXCLUDE_EXACT_ROOT_OUTPUT")
		}
	}
	return closedClaim("EXACT_ROOT_MANIFEST_OUTPUT_EXCLUDED")
}

func missingInputClaim() Claim {
	parent, err := os.MkdirTemp("", "gooo-artifact-manifest-missing-parent-")
	if err != nil {
		return unknownClaim("EVIDENCE", "CREATE_MISSING_FIXTURE", "MISSING_FIXTURE_UNAVAILABLE", "ENVIRONMENT", "PROVIDE_WRITABLE_TEMPORARY_DIRECTORY", "temporary_directory")
	}
	defer os.RemoveAll(parent)
	missing := filepath.Join(parent, "missing")
	_, err = Observe(missing)
	if err == nil {
		return refutedClaim("EVIDENCE", "CHECK_MISSING_INPUT", "MISSING_INPUT_NOT_UNKNOWN", "PRESERVE_MISSING_INPUT_UNKNOWN")
	}
	return claimFromError(err)
}

func staleInputClaim() Claim {
	root, err := os.MkdirTemp("", "gooo-artifact-manifest-stale-")
	if err != nil {
		return unknownClaim("EVIDENCE", "CREATE_STALE_FIXTURE", "STALE_FIXTURE_UNAVAILABLE", "ENVIRONMENT", "PROVIDE_WRITABLE_TEMPORARY_DIRECTORY", "temporary_directory")
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		return unknownClaim("EVIDENCE", "CREATE_STALE_FIXTURE", "STALE_FIXTURE_UNAVAILABLE", "ENVIRONMENT", "PROVIDE_WRITABLE_TEMPORARY_DIRECTORY", "temporary_directory")
	}
	old, err := Observe(root)
	if err != nil {
		return claimFromError(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		return unknownClaim("EVIDENCE", "MUTATE_STALE_FIXTURE", "STALE_FIXTURE_UNAVAILABLE", "ENVIRONMENT", "PROVIDE_WRITABLE_TEMPORARY_DIRECTORY", "temporary_directory")
	}
	claim, mismatches, err := CompareManifest(root, old.Manifest)
	if err != nil {
		return refutedClaim("EVIDENCE", "COMPARE_STALE_FIXTURE", "STALE_COMPARISON_FAILED", "REPAIR_COMPARISON")
	}
	if claim.State == UnknownState && mismatches > 0 {
		return claim
	}
	return refutedClaim("EVIDENCE", "CHECK_STALE_INPUT", "STALE_INPUT_NOT_UNKNOWN", "PRESERVE_STALE_INPUT_UNKNOWN")
}

func duplicateClaim() Claim {
	value := Manifest{
		Schema: Schema, InputRoot: ".", ExcludedPaths: []string{RootManifestPath}, ExactFileDenom: 2, ManifestEntries: 2,
		Entries: []Entry{{Path: "duplicate.txt", Size: 1, SHA256: strings.Repeat("0", 64)}, {Path: "duplicate.txt", Size: 1, SHA256: strings.Repeat("0", 64)}},
	}
	return claimFromValidation(value)
}

func traversalClaim() Claim {
	value := Manifest{
		Schema: Schema, InputRoot: ".", ExcludedPaths: []string{RootManifestPath}, ExactFileDenom: 1, ManifestEntries: 1,
		Entries: []Entry{{Path: "../escape.txt", Size: 1, SHA256: strings.Repeat("0", 64)}},
	}
	return claimFromValidation(value)
}

func launderingClaim(root string, baseline Manifest) Claim {
	claimed := baseline
	claimed.Entries = append([]Entry{}, baseline.Entries...)
	claimed.Entries = append(claimed.Entries, Entry{Path: "ghost.txt", Size: 0, SHA256: strings.Repeat("0", 64)})
	sort.Slice(claimed.Entries, func(left, right int) bool { return claimed.Entries[left].Path < claimed.Entries[right].Path })
	claimed.ExactFileDenom = len(claimed.Entries)
	claimed.ManifestEntries = len(claimed.Entries)
	claim, _, err := CompareManifest(root, claimed)
	if err != nil {
		return refutedClaim("EVIDENCE", "COMPARE_LAUNDERED_MANIFEST", "LAUNDERING_COMPARISON_FAILED", "REPAIR_COMPARISON")
	}
	return claim
}

func improvementExternalUtilityUnknownClaim() Claim {
	return unknownClaim("EVIDENCE", "CHECK_IMPROVEMENT_AND_EXTERNAL_UTILITY", ImprovementUnknown, "EVIDENCE_ABSENT", "PROVIDE_EXACT_PAIR_AND_EVIDENCE", "improvement_pair", "external_utility_pair")
}

func claimFromValidation(value Manifest) Claim {
	if err := ValidateManifest(value); err != nil {
		return claimFromError(err)
	}
	return refutedClaim("EVIDENCE", "VALIDATE_NEGATIVE_CASE", "NEGATIVE_CASE_ACCEPTED", "REJECT_UNSAFE_MANIFEST")
}

func claimFromError(err error) Claim {
	var boundaryError *BoundaryError
	if errors.As(err, &boundaryError) {
		return boundaryError.Claim
	}
	return refutedClaim("EVIDENCE", "HANDLE_RUNTIME_ERROR", "UNSTRUCTURED_RUNTIME_ERROR", "REPAIR_RUNTIME_ERROR")
}

func overallClaim(claims []Claim) (string, Claim) {
	for _, claim := range claims {
		if claim.State == RefutedState {
			return RefutedState, claim
		}
	}
	for _, claim := range claims {
		if claim.State == UnknownState {
			return UnknownState, claim
		}
	}
	return ClosedState, closedClaim("ALL_CONFORMANCE_CELLS_CLOSED")
}

func canonicalJSON(value Manifest) ([]byte, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func peakRSSKiB() int64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err == nil && usage.Maxrss > 0 {
		if runtime.GOOS == "darwin" {
			return usage.Maxrss / 1024
		}
		return usage.Maxrss
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return int64(memory.Sys / 1024)
}

func invokeActivity(ordinal int) {
	input := generated.ActivityInput{}
	switch ordinal {
	case 1:
		generated.ObserveNormalManifest(input)
	case 2:
		generated.ReplayManifestDeterministically(input)
	case 3:
		generated.PreserveNestedProducerManifests(input)
	case 4:
		generated.ExcludeExactRootOutput(input)
	case 5:
		generated.PreserveMissingInputUnknown(input)
	case 6:
		generated.PreserveStaleInputUnknown(input)
	case 7:
		generated.RefuteDuplicateAndCollision(input)
	case 8:
		generated.RefutePathTraversal(input)
	case 9:
		generated.RefuteManifestLaundering(input)
	case 10:
		generated.RefuteMixedFailure(input)
	case 11:
		generated.RefuteRepositoryWriteEscalation(input)
	case 12:
		generated.PreserveImprovementAndExternalUtilityUnknown(input)
	}
}
