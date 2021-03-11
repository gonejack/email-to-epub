# email-to-epub

Command line tool for converting emails to epub.

### Install
```shell
> go get github.com/gonejack/email-to-epub
```

### Usage
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
