package main

import (
	"io"
	"testing"

	"github.com/spf13/pflag"
)

// Mimir and Loki name a service with different labels, so each backend has
// its own flag. The older single flag still works and sets both; a later flag
// on the command line overrides it.
func TestServiceLabelFlags(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		mimir, lok string
	}{
		{"defaults", nil, "job", "service_name"},
		{"one label per backend", []string{"--mimir-service-label=service", "--loki-service-label=app"}, "service", "app"},
		{"deprecated flag sets both", []string{"--service-label=svc"}, "svc", "svc"},
		{"a later flag overrides it", []string{"--service-label=svc", "--mimir-service-label=job"}, "job", "svc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg costSourceConfig
			f := pflag.NewFlagSet("cost", pflag.ContinueOnError)
			f.SetOutput(io.Discard) // the deprecation notice
			addServiceLabelFlags(f, "", &cfg)
			if err := f.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if cfg.mimirServiceLabel != tc.mimir || cfg.lokiServiceLabel != tc.lok {
				t.Errorf("mimir %q, loki %q; want %q, %q", cfg.mimirServiceLabel, cfg.lokiServiceLabel, tc.mimir, tc.lok)
			}
		})
	}
}
