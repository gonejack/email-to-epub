package main

import (
	_ "embed"
	"log"
	"os"

	"github.com/gonejack/email-to-epub/email2epub"
)

//go:embed cover.png
var defaultCover []byte

func init() {
	log.SetOutput(os.Stdout)
}

func main() {
	cmd := email2epub.EmailToEpub{
		Options:      email2epub.MustParseOptions(),
		DefaultCover: defaultCover,
	}
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}
}
