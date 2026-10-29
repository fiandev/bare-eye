package main

import (
	"os"

	"github.com/fiandev/bare-eye/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
