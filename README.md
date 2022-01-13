# email-to-epub

This command line converts .eml to .epub file.

![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/gonejack/email-to-epub)
![Build](https://github.com/gonejack/email-to-epub/actions/workflows/go.yml/badge.svg)
[![GitHub license](https://img.shields.io/github/license/gonejack/email-to-epub.svg?color=blue)](LICENSE)

### Install
```shell
> go get github.com/gonejack/email-to-epub
```

### Usage
```shell
> email-to-epub *.eml
```
```
Flags:
  -h, --help                     Show context-sensitive help.
      --cover=STRING             Set epub cover image.
      --title="HTML"             Set epub title.
      --author="HTML to Epub"    Set epub author.
  -o, --output="output.epub"     Output filename.
  -v, --verbose                  Verbose printing.
      --about                    About.
```
