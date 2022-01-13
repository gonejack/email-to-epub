package email2epub

import (
	"path/filepath"

	"github.com/alecthomas/kong"
)

type Options struct {
	Cover   string `help:"Set epub cover image."`
	Title   string `default:"HTML" help:"Set epub title."`
	Author  string `default:"HTML to Epub" help:"Set epub author."`
	Output  string `short:"o" default:"output.epub" help:"Output filename."`
	Verbose bool   `short:"v" help:"Verbose printing."`
	About   bool   `help:"About."`

	ImagesDir      string `hidden:"" default:"images"`
	AttachmentsDir string `hidden:"" default:"attachments"`

	EML []string `name:".eml" arg:"" optional:"" help:"list of .eml files"`
}

func MustParseOptions() (opts Options) {
	kong.Parse(&opts,
		kong.Name("email-to-epub"),
		kong.Description("This command line converts .eml to .epub with images embed"),
		kong.UsageOnError(),
	)
	if len(opts.EML) == 0 || opts.EML[0] == "*.eml" {
		opts.EML, _ = filepath.Glob("*.eml")
	}
	return
}
