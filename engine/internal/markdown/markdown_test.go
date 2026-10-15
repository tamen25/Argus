package markdown

import "testing"

func TestCell(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"checkout", "checkout"},
		{"a|b", `a\|b`},
		{"line\nbreak", "line break"},
		{"crlf\r\nbreak", "crlf break"},
		// A trailing backslash must not escape the "|" that closes the cell.
		{`ends\`, `ends\\`},
		{`a\|b`, `a\\\|b`},
		{"", ""},
	} {
		if got := Cell(tc.in); got != tc.want {
			t.Errorf("Cell(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
