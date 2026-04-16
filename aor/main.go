package main

import (
	"fmt"
	"os"

	"aor/aor/cmd"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcmd := os.Args[1]
	args := os.Args[2:]

	switch subcmd {
	case "help", "--help", "-h":
		printUsage()
		return
	case "serve":
		if err := cmd.Serve(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `aor — Agent ORchestration

Usage:
  aor <command> [args]

Commands:
  serve     Start unified web server (ata + afl)

Flags:
  --port <n>        HTTP port (default: 4400)
  --addr <addr>     Listen address (default: 0.0.0.0)
  --tls-cert <path> TLS certificate file
  --tls-key <path>  TLS private key file
`)
}
