package main

import "testing"

// R27: the scheme of OTEL_EXPORTER_OTLP_ENDPOINT chooses the transport.
// https:// used to be stripped like http://, so a TLS endpoint got plaintext.
func TestOTLPEndpointFromEnv(t *testing.T) {
	cases := []struct {
		env, forceInsecure string
		endpoint           string
		insecure           bool
	}{
		{"https://otlp.example.com:4317", "", "otlp.example.com:4317", false},
		{"https://otlp.example.com:4317/", "", "otlp.example.com:4317", false},
		{"https://otlp.example.com:4317", "true", "otlp.example.com:4317", true},
		{"http://127.0.0.1:4319", "", "127.0.0.1:4319", true}, // the CI dogfood gate
		{"argus.argus.svc:4317", "", "argus.argus.svc:4317", true},
	}
	for _, c := range cases {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", c.env)
		t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", c.forceInsecure)
		ep, insecure := otlpEndpointFromEnv()
		if ep != c.endpoint || insecure != c.insecure {
			t.Errorf("%q: endpoint %q insecure %v, want %q %v", c.env, ep, insecure, c.endpoint, c.insecure)
		}
	}
}
