package main

import (
	"os"

	"github.com/subaud-tools/kempt/internal/cli"
)

func main() { os.Exit(cli.Dispatch(os.Args[1:], os.Stdout, os.Stderr)) }
