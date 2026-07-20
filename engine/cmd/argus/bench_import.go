package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tamen25/Argus/engine/internal/bench/itbench"
)

func newBenchImportITBenchCmd() *cobra.Command {
	var in, out string
	var skipInvalid bool

	cmd := &cobra.Command{
		Use:   "import-itbench",
		Short: "Convert ITBench SRE scenario definitions into Argus bench scenarios",
		Long: `Converts ITBench scenario index files (github.com/itbench-hub/ITBench,
Apache-2.0) into Argus scenarios so results are comparable with published
ITBench baselines.

Imported scenarios are SCORE-ONLY. ITBench executes its faults with its own
tooling against a fixed fault catalogue; Argus cannot reproduce those
injections, and pretending it could would make the comparability claim false.
So: stage the environment with ITBench, then score the agent with

    argus bench run --scenario <imported>.yaml --inject=none ...

The emitted inject step names a script Argus deliberately cannot execute, so
running an imported scenario any other way fails loudly instead of silently
measuring an un-faulted environment.

Ground truth is derived from each injection's args.kubernetesObject — the object
the fault was applied to. Waiter objects (restarted or rescaled workloads) are
collateral and are never treated as root cause. A scenario whose ground truth
cannot be derived is refused, not emitted with an empty answer key.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			files, err := itbenchInputs(in)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(out, 0o750); err != nil {
				return err
			}

			var written, skipped int
			for _, f := range files {
				sc, err := itbench.ConvertFile(f)
				if err != nil {
					if skipInvalid {
						skipped++
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "skipped: %v\n", err)
						continue
					}
					return err
				}
				b, err := yaml.Marshal(sc)
				if err != nil {
					return err
				}
				dst := filepath.Join(out, sc.Metadata.Name+".yaml")
				header := fmt.Sprintf("# Imported from %s (ITBench, Apache-2.0).\n"+
					"# SCORE-ONLY: stage the environment with ITBench, then run with --inject=none.\n",
					sc.Metadata.Source)
				if err := os.WriteFile(dst, append([]byte(header), b...), 0o600); err != nil {
					return err
				}
				written++
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"imported %d ITBench scenario(s) into %s (%d skipped)\n", written, out, skipped)
			return err
		},
	}

	cmd.Flags().StringVar(&in, "in", "", "ITBench scenario file, or a directory of them")
	cmd.Flags().StringVar(&out, "out", "scenarios/itbench", "directory to write Argus scenarios into")
	cmd.Flags().BoolVar(&skipInvalid, "skip-invalid", false,
		"skip scenarios that cannot be converted (e.g. no derivable ground truth) instead of failing")
	_ = cmd.MarkFlagRequired("in")
	return cmd
}

// itbenchInputs resolves --in to a sorted list of JSON files, so a directory
// import is deterministic.
func itbenchInputs(in string) ([]string, error) {
	info, err := os.Stat(in)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{in}, nil
	}
	entries, err := os.ReadDir(in)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		files = append(files, filepath.Join(in, e.Name()))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .json scenario files in %s", in)
	}
	sort.Strings(files)
	return files, nil
}
