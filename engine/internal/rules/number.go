package rules

// AsFloat reads a number held in a rule parameter or an evidence attribute.
// Those are untyped (any) and arrive as int, int64 or float64 depending on
// where they came from: YAML decoding, CEL evaluation, or Go code. Anything
// else is not a number, and ok is false.
func AsFloat(v any) (f float64, ok bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	default:
		return 0, false
	}
}
