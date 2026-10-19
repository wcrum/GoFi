//go:build !linux

package main

import "os"

// terminalSize is unknown off Linux; the TUI uses its defaults.
func terminalSize(*os.File) (int, int) { return 0, 0 }

// rawInput is unavailable off Linux; keys are read line by line (press q
// then Enter, or Ctrl-C).
func rawInput(*os.File) func() { return nil }
