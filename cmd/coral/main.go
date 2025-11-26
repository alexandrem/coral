package main

import (
	"fmt"
	"os"

	"github.com/coral-io/coral/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err) // TODO: errcheck
		os.Exit(1)
	}
}
