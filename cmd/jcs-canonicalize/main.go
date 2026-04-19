// jcs-canonicalize reads JSON from stdin and writes the RFC 8785
// (JCS) canonical form to stdout. Intended for byte-stability
// diffing against chrest's canonicalizer during RFC 0001 co-design;
// not shipped with nebulous releases.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/friedenberg/nebulous/internal/0/jcs"
)

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcs-canonicalize: read stdin: %v\n", err)
		os.Exit(1)
	}
	out, err := jcs.CanonicalizeJSON(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcs-canonicalize: %v\n", err)
		os.Exit(2)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "jcs-canonicalize: write stdout: %v\n", err)
		os.Exit(3)
	}
}
