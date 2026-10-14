package bounded

import (
	"errors"
	"strings"
	"testing"
)

func TestReadAllAtAndOverTheLimit(t *testing.T) {
	if b, err := ReadAll(strings.NewReader("12345"), 5); err != nil || string(b) != "12345" {
		t.Errorf("exactly at the limit: %q, %v", b, err)
	}
	if _, err := ReadAll(strings.NewReader("123456"), 5); !errors.Is(err, ErrTooLarge) {
		t.Errorf("one byte over: err = %v, want ErrTooLarge rather than a cut prefix", err)
	}
}

func TestDecodeJSON(t *testing.T) {
	var v struct{ A int }
	if err := DecodeJSON(strings.NewReader(`{"A":1}`), 64, &v); err != nil || v.A != 1 {
		t.Errorf("v = %+v, err = %v", v, err)
	}
	// A valid but oversized document is refused, not decoded from a prefix.
	big := `{"A":1,"pad":"` + strings.Repeat("x", 100) + `"}`
	if err := DecodeJSON(strings.NewReader(big), 64, &v); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized JSON: err = %v, want ErrTooLarge", err)
	}
}

// A process that writes past the limit keeps running (Write never fails),
// and only the first Limit bytes are kept.
func TestBufferKeepsTheFirstBytes(t *testing.T) {
	b := &Buffer{Limit: 4}
	for _, chunk := range []string{"ab", "cdef", "gh"} {
		if n, err := b.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; want the whole chunk accepted", chunk, n, err)
		}
	}
	if string(b.Bytes()) != "abcd" || !b.Overflowed() {
		t.Errorf("kept %q, overflowed %v; want abcd and true", b.Bytes(), b.Overflowed())
	}
	if got := b.String(); got != "abcd… (4 more bytes not kept)" {
		t.Errorf("String() = %q", got)
	}
	if s := (&Buffer{Limit: 10}).String(); s != "" {
		t.Errorf("empty buffer = %q", s)
	}
}
