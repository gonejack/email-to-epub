# email-to-epub

Command line tool for converting emails to epub.

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
Usage:
  email-to-epub [-o output] [--title title] [--cover cover] *.eml

Flags:
  -o, --output string   output filename (default "output.epub")
      --title string    epub title (default "Email")
      --cover string    cover image
  -v, --verbose         verbose
  -h, --help            help for email-to-epub
```
