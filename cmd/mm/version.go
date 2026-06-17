package main

import (
	"fmt"
	"io"
	"os"
)

// Build variables — overridden via -ldflags at release build time (see
// .goreleaser.yaml). Defaults apply to `go build` / `go install` builds.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func cmdVersion(_ []string) error {
	printVersion(os.Stdout)
	return nil
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "mm version %s\n", Version)
	fmt.Fprintf(w, "commit: %s\n", Commit)
	fmt.Fprintf(w, "built:  %s\n", Date)
}
