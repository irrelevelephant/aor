package main

import "fmt"

// sentinelBlock builds a consistent, end-of-prompt sentinel instruction block.
// name is the sentinel prefix (e.g. "ATA_RUNNER_STATUS"), successExample is the
// JSON template for the success case, and errorExample is for the error case.
// An optional preamble inserts prompt-specific content before the sentinel examples.
func sentinelBlock(name, successExample, errorExample, preamble string) string {
	s := ""
	if preamble != "" {
		s += preamble + "\n\n"
	}
	s += fmt.Sprintf("MANDATORY FINAL OUTPUT — Your LAST message must contain this line on its own (no markdown fences):\n\n%s:%s\n", name, successExample)
	if errorExample != "" {
		s += fmt.Sprintf("\nOn error use: %s:%s\n", name, errorExample)
	}
	s += fmt.Sprintf("\nDo NOT end your session without outputting %s.", name)
	return s
}
