package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "craft",
	Short:        "Code review tool for GitHub PRs",
	Long:         `craft is a tool for doing GitHub code review locally with PR comments embedded in source files.`,
	SilenceUsage: true,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(wrapCmd)
	rootCmd.AddCommand(unwrapCmd)
	rootCmd.AddCommand(debugFetchCmd)
	rootCmd.AddCommand(debugCommentCmd)
	rootCmd.AddCommand(debugSendCmd)
	rootCmd.AddCommand(debugSerializeCmd)
	rootCmd.AddCommand(debugDeserializeCmd)
}
