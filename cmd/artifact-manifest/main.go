package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/kimjooyoon/gooo-artifact-manifest/internal/manifest"
)

func main() {
	mode := flag.String("mode", "manifest", "manifest or conformance")
	input := flag.String("input", "", "input directory to observe")
	output := flag.String("output", "", "caller-owned output file")
	flag.Parse()
	if *input == "" || *output == "" {
		fail("-input and -output are required")
	}

	switch *mode {
	case "manifest":
		if _, err := manifest.ObserveTo(*input, *output); err != nil {
			failError(err)
		}
	case "conformance":
		report, err := manifest.BuildConformanceReport(*input)
		if err != nil {
			failError(err)
		}
		if err := manifest.WriteReport(*output, report); err != nil {
			failError(err)
		}
	default:
		fail(fmt.Sprintf("unknown -mode %q", *mode))
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}

func failError(err error) {
	var boundaryError *manifest.BoundaryError
	if errors.As(err, &boundaryError) {
		fmt.Fprintf(os.Stderr, "%s: %s\n", boundaryError.Claim.State, boundaryError.Claim.Reason)
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(1)
}
