package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var debugFetchCmd = &cobra.Command{
	Use:   "debugfetch",
	Short: "Fetch PR state from GitHub and output as JSON",
	Long:  `Low-level command to fetch pull request state including review threads, comments, and reviews, then output as JSON.`,
	RunE:  runDebugFetch,
}

var (
	flagOwner  string
	flagRepo   string
	flagNumber int
)

func init() {
	debugFetchCmd.Flags().StringVar(&flagOwner, "owner", "", "Repository owner")
	debugFetchCmd.Flags().StringVar(&flagRepo, "repo", "", "Repository name")
	debugFetchCmd.Flags().IntVar(&flagNumber, "number", 0, "PR number")
	debugFetchCmd.MarkFlagRequired("owner")
	debugFetchCmd.MarkFlagRequired("repo")
	debugFetchCmd.MarkFlagRequired("number")
}

func runDebugFetch(cmd *cobra.Command, args []string) error {
	// Get GitHub token
	token, err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("failed to get GitHub token: %w", err)
	}

	// Create client and fetch PR
	client := NewGitHubClient(token)
	pr, err := client.FetchPullRequest(cmd.Context(), flagOwner, flagRepo, flagNumber)
	if err != nil {
		return fmt.Errorf("failed to fetch PR: %w", err)
	}

	// Output as JSON
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(pr)
}
