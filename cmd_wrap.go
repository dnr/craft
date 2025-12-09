package main

import (
	"io"
	"math"
	"os"

	"github.com/spf13/cobra"
	"rsc.io/markdown"
)

var wrapCmd = &cobra.Command{
	Use:   "wrap",
	Short: "Wrap markdown text from stdin at specified width",
	Long:  `Reads markdown from stdin, wraps at the specified width, and writes to stdout.`,
	RunE:  runWrap,
	Args:  cobra.NoArgs,
}

var unwrapCmd = &cobra.Command{
	Use:   "unwrap",
	Short: "Unwrap markdown text from stdin",
	Long:  `Reads markdown from stdin, removes soft wrapping, and writes to stdout.`,
	RunE:  runUnwrap,
	Args:  cobra.NoArgs,
}

var flagWrapWidth int

func init() {
	wrapCmd.Flags().IntVarP(&flagWrapWidth, "width", "w", 80, "Line width for wrapping")
}

func runWrap(cmd *cobra.Command, args []string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}

	p := markdown.Parser{}
	doc := p.Parse(string(input))
	wrapped := Wrap(doc, flagWrapWidth)
	os.Stdout.WriteString(markdown.Format(wrapped))
	return nil
}

func runUnwrap(cmd *cobra.Command, args []string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}

	p := markdown.Parser{}
	doc := p.Parse(string(input))
	unwrapped := Wrap(doc, math.MaxInt)
	os.Stdout.WriteString(markdown.Format(unwrapped))
	return nil
}
