package clip

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHead(t *testing.T) {
	for _, tc := range []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"abcdefghij", 4, "abcd…"},
		// "é" is two bytes; a cut at byte 2 would fall inside it.
		{"aébc", 2, "a…"},
		{"日本語", 4, "日…"},
		{"", 0, ""},
	} {
		if got := Head(tc.s, tc.n); got != tc.want {
			t.Errorf("Head(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestMiddleKeepsBothEnds(t *testing.T) {
	long := "backend http://mimir/api/v1/query?query=" + strings.Repeat("x", 1000) + ": HTTP 400: parse error"
	got := Middle(long, 120)
	if !strings.HasPrefix(got, "backend http://mimir") || !strings.HasSuffix(got, "parse error") {
		t.Errorf("Middle lost an end: %q", got)
	}
	if len(got) > 120+len(marker) {
		t.Errorf("Middle kept %d bytes, want at most %d", len(got), 120+len(marker))
	}
	if Middle("short", 10) != "short" {
		t.Error("a short string must be kept whole")
	}
}

// Whatever the cut point, the result is valid UTF-8: a cut inside a
// character is moved to its boundary.
func TestCutsNeverSplitACharacter(t *testing.T) {
	s := strings.Repeat("日本語ü", 20)
	for n := range len(s) {
		for name, got := range map[string]string{"Head": Head(s, n), "Middle": Middle(s, n)} {
			if !utf8.ValidString(got) {
				t.Fatalf("%s(_, %d) = %q is not valid UTF-8", name, n, got)
			}
			if len(got) > n+len(marker) {
				t.Fatalf("%s(_, %d) kept %d bytes", name, n, len(got))
			}
		}
	}
}
