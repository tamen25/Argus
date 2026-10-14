package rules

import "testing"

func TestAsFloat(t *testing.T) {
	for _, c := range []struct {
		in   any
		want float64
		ok   bool
	}{
		{int(3), 3, true},
		{int64(4), 4, true},
		{2.5, 2.5, true},
		{"5", 0, false},
		{nil, 0, false},
	} {
		if got, ok := AsFloat(c.in); got != c.want || ok != c.ok {
			t.Errorf("AsFloat(%#v) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
