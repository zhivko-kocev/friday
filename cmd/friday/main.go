package main

import (
	"os"

	"github.com/zhivko-kocev/friday/internal/cli"
)

// version is stamped at build time via -ldflags "-X main.version=..."
// (see Makefile / .goreleaser.yaml) and passed into cli.Run; it defaults
// to "dev" for `go run` and unstamped builds.
var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version))
}
