package main

import "os"

var (
	version   = "0.1.0"
	revision  = "dirty"
	buildDate = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	root := newRootCommand()
	root.SetArgs(arguments)

	return execute(root)
}
