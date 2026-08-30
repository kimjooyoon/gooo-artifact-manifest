# gooo-artifact-manifest

`gooo-artifact-manifest` is a read-only, deterministic manifest observer for
CI artifact directories. It records sorted relative paths, byte sizes, and
SHA-256 digests in caller-owned JSON output.

The product contract is declared in
[`examples/artifact-manifest/main.gooo`](examples/artifact-manifest/main.gooo).
The checked-in executable surface is the Go projection produced from that
contract by the pinned released Gooo compiler. The observer itself never
trusts a producer-written manifest: it re-reads every observed regular file
and computes its digest.

The only excluded relative path is exactly `manifest.json` at the input root.
For example, `producer/alpha/manifest.json` remains an entry. Paths are
normalized to `/` and sorted before JSON encoding, so shell whitespace,
newline joining, and redirection placement cannot affect the manifest.

## Usage

```text
go run ./cmd/artifact-manifest -input <directory> -output <manifest.json>
go run ./cmd/artifact-manifest -mode conformance -input <directory> -output <report.json>
```

The conformance report has a fixed twelve-cell denominator. It includes normal
and replay evidence, the exact root-output exclusion boundary, UNKNOWN records
with all six coordinates, REFUTED precedence, inventory metrics, resource
metrics, and zero repository-write authority.

`README.md` at the observed root is excluded from inventory counters but is
still treated as an ordinary manifest input file. This keeps documentation from
changing the source inventory denominator while preserving complete artifact
content evidence.

## CI and release boundary

GitHub Actions is the only validation route for this repository. CI downloads
the exact compiler release in
[`contracts/compiler-release-lock-v1.json`](contracts/compiler-release-lock-v1.json),
checks the tag object, target commit, and asset digest, resolves all twelve
Gooo activities, generates the Go projection twice, and runs the Go conformance
tests and scenario report on Go 1.27.

The repository is intentionally independent: it requires no cross-project CI
gate, performs no repository writes during observation, and does not claim an
improvement or external utility without an exact pair and evidence.
