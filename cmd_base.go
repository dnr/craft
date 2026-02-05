package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var baseCmd = &cobra.Command{
	Use:   "base",
	Short: "Print the base commit for the current PR",
	Long: `Prints the base commit OID for the current PR.

This reads from PR-STATE.txt which is populated by 'craft get'.
The output can be used with vim-fugitive and vim-gitgutter to set the
diff base for code review.

Example usage in vim:
  :let g:craft_base = system('craft base')`,
	RunE: runBase,
	Args: cobra.NoArgs,
}

func init() {
	rootCmd.AddCommand(baseCmd)
}

func runBase(cmd *cobra.Command, args []string) error {
	// Detect VCS to find repo root
	vcs, err := DetectVCS(".")
	if err != nil {
		return err
	}

	// Deserialize PR state
	opts := SerializeOptions{FS: DirFS(vcs.Root()), VCS: vcs}
	pr, err := Deserialize(opts)
	if err != nil {
		return fmt.Errorf("reading PR state: %w", err)
	}

	if pr.BaseRefOID == "" {
		fmt.Fprintln(os.Stderr, "warning: no base commit in PR-STATE.txt, run 'craft get' to refresh")
		return fmt.Errorf("no base commit found")
	}

	fmt.Println(pr.BaseRefOID)
	return nil
}
