package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-github/v74/github"
	"golang.org/x/oauth2"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "get":
		handleGet(os.Args[2:])
	case "send":
		handleSend(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("craft - GitHub code review tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  craft get [<pr#>]    Get PR for review")
	fmt.Println("  craft send [--go]    Send review comments")
}

func handleGet(args []string) {
	fmt.Println("get command - not implemented yet")
	if len(args) > 0 {
		fmt.Printf("PR number: %s\n", args[0])
	}
}

func handleSend(args []string) {
	fmt.Println("send command - not implemented yet")
	for _, arg := range args {
		if arg == "--go" {
			fmt.Println("--go flag detected")
		}
	}
}

func createGitHubClient() *github.Client {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: GITHUB_TOKEN environment variable is required")
		fmt.Fprintln(os.Stderr, "Create a personal access token at: https://github.com/settings/tokens")
		fmt.Fprintln(os.Stderr, "Then export GITHUB_TOKEN=your_token")
		os.Exit(1)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	
	return github.NewClient(tc)
}