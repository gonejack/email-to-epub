package main

import (
	"fmt"
	"log"
	"os"

	_ "embed"

	"github.com/gonejack/email-to-epub/cmd"
	"github.com/spf13/cobra"
)

var (
	//go:embed cover.png
	defaultCover []byte

	cover   *string
	title   *string
	author  *string
	output  *string
	verbose = false

	prog = &cobra.Command{
		Use:   "email-to-epub [-o output] [--title title] [--cover cover] *.eml",
		Short: "Command line tool for converting emails to epub.",
		Run: func(c *cobra.Command, args []string) {
			err := run(c, args)
			if err != nil {
				log.Fatal(err)
			}
		},
	}
)

func init() {
	log.SetOutput(os.Stdout)

	prog.Flags().SortFlags = false
	prog.PersistentFlags().SortFlags = false

	cover = prog.PersistentFlags().StringP(
		"cover",
		"",
		"",
		"epub cover image",
	)
	title = prog.PersistentFlags().StringP(
		"title",
		"",
		"Emails",
		"epub title",
	)
	author = prog.PersistentFlags().StringP(
		"author",
		"",
		"Email to Epub",
		"epub author",
	)
	output = prog.PersistentFlags().StringP(
		"output",
		"o",
		"output.epub",
		"output filename",
	)
	prog.PersistentFlags().BoolVarP(
		&verbose,
		"verbose",
		"v",
		false,
		"verbose",
	)
}

func run(c *cobra.Command, args []string) error {
	_, err := os.Stat(*output)
	if !os.IsNotExist(err) {
		return fmt.Errorf("output file %s already exist", *output)
	}

	exec := cmd.EmailToEpub{
		DefaultCover:   defaultCover,
		ImagesDir:      "images",
		AttachmentsDir: "attachments",

		Cover:   *cover,
		Title:   *title,
		Author:  *author,
		Verbose: verbose,
	}

	return exec.Execute(args, *output)
}

func main() {
	_ = prog.Execute()
}
