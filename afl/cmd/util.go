package cmd

import (
	"encoding/json"
	"flag"
	"os"
	"strings"
)

func outputJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// splitFlagsAndPositional separates flag arguments from positional arguments.
// flagsWithValue is a set of flag names (without --) that take a value argument.
func splitFlagsAndPositional(args []string, flagsWithValue map[string]bool) (flags, positional []string) {
	flags, positional, _ = SplitArgs(args, flagsWithValue)
	return
}

// SplitArgs is the exported form of splitFlagsAndPositional that also returns
// the index in args of each positional value. Callers that need to mutate
// args in place (e.g. the remote client rewriting file paths to upload
// placeholders) use positionalIdx to locate the Nth positional's slot.
func SplitArgs(args []string, flagsWithValue map[string]bool) (flags, positional []string, positionalIdx []int) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for j := i + 1; j < len(args); j++ {
				positional = append(positional, args[j])
				positionalIdx = append(positionalIdx, j)
			}
			break
		}
		if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			name := strings.TrimLeft(arg, "-")
			if strings.Contains(name, "=") {
				flags = append(flags, arg)
				continue
			}
			flags = append(flags, arg)
			if flagsWithValue[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, arg)
			positionalIdx = append(positionalIdx, i)
		}
	}
	return
}
