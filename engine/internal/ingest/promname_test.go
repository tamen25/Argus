package ingest

import (
	"regexp"
	"testing"
)

// The __name__ pattern matches the series an OTLP metric is stored as, with
// or without the unit and type suffixes, and nothing else. The first two
// cases are names this project's dev cluster stores.
func TestPromNameRegexp(t *testing.T) {
	for _, tc := range []struct {
		name, unit, typ string
		match, noMatch  []string
	}{
		{
			"http.server.duration", "ms", "histogram",
			[]string{"http_server_duration_milliseconds_bucket", "http_server_duration_milliseconds_count", "http_server_duration_bucket"},
			[]string{"http_server_duration_milliseconds", "http_server_duration_seconds_bucket", "http_server_duration_milliseconds_bucket_x"},
		},
		{
			"app.frontend.requests", "1", "sum",
			[]string{"app_frontend_requests_total", "app_frontend_requests"},
			[]string{"app_frontend_requests_ratio", "app_frontend_requests_total_total"},
		},
		{
			// A counter named "...total" keeps one "total", at the end.
			"jobs.total.processed", "{job}", "sum",
			[]string{"jobs_processed_total", "jobs_total_processed"},
			[]string{"jobs_total_processed_total"},
		},
		{
			"process.cpu.utilization", "1", "gauge",
			[]string{"process_cpu_utilization_ratio", "process_cpu_utilization"},
			nil,
		},
		{
			"network.io", "By/s", "gauge",
			[]string{"network_io_bytes_per_second", "network_io"},
			[]string{"network_io_bytes"},
		},
		{
			// A unit already in the name is not repeated.
			"request.duration.seconds", "s", "histogram",
			[]string{"request_duration_seconds_bucket"},
			[]string{"request_duration_seconds_seconds_bucket"},
		},
		{
			// A leading digit gets "_" in front; "." and "-" become "_".
			"2xx-responses.count", "", "gauge",
			[]string{"_2xx_responses_count"},
			[]string{"2xx_responses_count"},
		},
	} {
		re := regexp.MustCompile("^(?:" + promNameRegexp(tc.name, tc.unit, tc.typ) + ")$")
		for _, n := range tc.match {
			if !re.MatchString(n) {
				t.Errorf("%s (%s, %s): %q does not match %s", tc.name, tc.unit, tc.typ, n, re)
			}
		}
		for _, n := range tc.noMatch {
			if re.MatchString(n) {
				t.Errorf("%s (%s, %s): %q matches %s", tc.name, tc.unit, tc.typ, n, re)
			}
		}
	}
}

func TestPromLabelName(t *testing.T) {
	for in, want := range map[string]string{
		"http.route":        "http_route",
		"service.name":      "service_name",
		"k8s.pod.name":      "k8s_pod_name",
		"user-agent":        "user_agent",
		"2fa":               "key_2fa",
		"_private":          "key_private",
		"__reserved":        "__reserved",
		"already_prom_safe": "already_prom_safe",
	} {
		if got := promLabelName(in); got != want {
			t.Errorf("promLabelName(%q) = %q, want %q", in, got, want)
		}
	}
}
