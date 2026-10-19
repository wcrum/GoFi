//go:build race

package filter

// raceEnabled relaxes allocation-size bounds: the race detector's string
// instrumentation copies conversions that the normal build does not.
const raceEnabled = true
