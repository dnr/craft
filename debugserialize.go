package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var debugSerializeCmd = &cobra.Command{
	Use:   "debugserialize",
	Short: "Serialize PR JSON to source files",
	Long: `Reads a PR JSON file and writes comments into source files in a working directory.

Example:
  craft debugserialize --input debug.json --workdir /path/to/repo`,
	RunE: runDebugSerialize,
}

var debugDeserializeCmd = &cobra.Command{
	Use:   "debugdeserialize",
	Short: "Deserialize source files to PR JSON",
	Long: `Reads comments from source files and PR-STATE.txt and outputs JSON.

Example:
  craft debugdeserialize --workdir /path/to/repo --output pr.json`,
	RunE: runDebugDeserialize,
}

var (
	flagSerializeInput   string
	flagSerializeWorkdir string
	flagSerializeOutput  string
)

func init() {
	debugSerializeCmd.Flags().StringVar(&flagSerializeInput, "input", "", "Input JSON file with PR data")
	debugSerializeCmd.Flags().StringVar(&flagSerializeWorkdir, "workdir", "", "Working directory (repo root)")
	debugSerializeCmd.MarkFlagRequired("input")
	debugSerializeCmd.MarkFlagRequired("workdir")

	debugDeserializeCmd.Flags().StringVar(&flagSerializeWorkdir, "workdir", "", "Working directory (repo root)")
	debugDeserializeCmd.Flags().StringVar(&flagSerializeOutput, "output", "", "Output JSON file (default: stdout)")
	debugDeserializeCmd.MarkFlagRequired("workdir")
}

func runDebugSerialize(cmd *cobra.Command, args []string) error {
	// Load input JSON
	data, err := os.ReadFile(flagSerializeInput)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	var pr PullRequest
	if err := json.Unmarshal(data, &pr); err != nil {
		return fmt.Errorf("parsing input JSON: %w", err)
	}

	// Detect VCS for the workdir
	vcs, _ := DetectVCS(flagSerializeWorkdir)

	// Serialize to files
	opts := SerializeOptions{
		FS:  DirFS(flagSerializeWorkdir),
		VCS: vcs,
	}
	if err := Serialize(&pr, opts); err != nil {
		return fmt.Errorf("serializing: %w", err)
	}

	fmt.Println("Serialized successfully.")
	return nil
}

func runDebugDeserialize(cmd *cobra.Command, args []string) error {
	// Detect VCS for the workdir
	vcs, _ := DetectVCS(flagSerializeWorkdir)

	opts := SerializeOptions{
		FS:  DirFS(flagSerializeWorkdir),
		VCS: vcs,
	}

	pr, err := Deserialize(opts)
	if err != nil {
		return fmt.Errorf("deserializing: %w", err)
	}

	// Output JSON
	data, err := json.MarshalIndent(pr, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}

	if flagSerializeOutput != "" {
		if err := os.WriteFile(flagSerializeOutput, data, 0644); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
		fmt.Printf("Wrote %s\n", flagSerializeOutput)
	} else {
		fmt.Println(string(data))
	}

	return nil
}
