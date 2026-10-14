package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// writeOutput writes a rendered report to path, or to standard output when
// path is empty. Reports are for people to read and hold no secrets, so files
// are 0644 like any document.
//
// Not cmd.Print: cobra's Print writes to stderr unless an output was set, so
// `argus cost > report.md` once produced an empty file (tests never noticed:
// they set an output, which redirects both).
func writeOutput(cmd *cobra.Command, path string, report []byte) error {
	if path == "" {
		_, err := cmd.OutOrStdout().Write(report)
		return err
	}
	return os.WriteFile(path, report, 0o644)
}

// parseWindow reads a --from/--to pair of RFC 3339 times; to must be after
// from.
func parseWindow(from, to string) (start, end time.Time, err error) {
	start, err = time.Parse(time.RFC3339, from)
	if err != nil {
		return start, end, fmt.Errorf("--from: %w", err)
	}
	end, err = time.Parse(time.RFC3339, to)
	if err != nil {
		return start, end, fmt.Errorf("--to: %w", err)
	}
	if !end.After(start) {
		return start, end, fmt.Errorf("--to must be after --from")
	}
	return start, end, nil
}
