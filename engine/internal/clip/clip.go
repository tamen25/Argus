// Package clip shortens text that people read: error messages, stderr quoted
// in an error, and the tool log and submissions kept in a bench run report.
// A cut is marked with "…", so a reader knows something is missing, and it
// never splits a UTF-8 character, so the result stays valid text in a JSON
// report.
//
// Telemetry values are shortened by model.Truncate instead. That one adds no
// marker, because the value is compared and hashed, and copies the result, so
// a short value kept for a long time does not hold the payload it came from.
package clip

import "unicode/utf8"

// marker replaces what a cut removed.
const marker = "…"

// Head keeps the first n bytes of s, or fewer to end on a character boundary,
// and marks the cut. Use it where the start says what happened: stderr, a
// model's reply, a submission.
func Head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:start(s, n)] + marker
}

// Middle keeps about n bytes of s by cutting out its middle: a third from the
// start and the rest from the end. Use it where the end carries the reason: a
// backend error starts with the request URL, which repeats the query, and
// ends with why the request failed.
func Middle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head := start(s, n/3)
	tail := len(s) - (n - n/3)
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return s[:head] + marker + s[tail:]
}

// start moves i back to the first byte of the character it falls in.
func start(s string, i int) int {
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}
