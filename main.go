package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "embed"

	"github.com/PuerkitoBio/goquery"
	"github.com/bmaupin/go-epub"
	"github.com/dustin/go-humanize"
	"github.com/gabriel-vasile/mimetype"
	"github.com/jordan-wright/email"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var (
	imageCacheDir = "images"

	//go:embed cover.png
	defaultCover []byte

	cover  *string
	title  *string
	author *string
	output *string

	flagVerbose = false

	client = http.Client{}
	cmd    = &cobra.Command{
		Use:   "email-to-epub [-o output] [--title title] [--cover cover] *.eml",
		Short: "Command line tool for converting emails to epub.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := run(cmd, args); err != nil {
				log.Fatal(err)
			}
		},
	}
)

func init() {
	cmd.Flags().SortFlags = false
	cmd.PersistentFlags().SortFlags = false

	cover = cmd.PersistentFlags().StringP(
		"cover",
		"",
		"",
		"epub cover image",
	)
	title = cmd.PersistentFlags().StringP(
		"title",
		"",
		"Emails",
		"epub title",
	)
	author = cmd.PersistentFlags().StringP(
		"author",
		"",
		"Email to Epub",
		"epub author",
	)
	output = cmd.PersistentFlags().StringP(
		"output",
		"o",
		"output.epub",
		"output filename",
	)
	cmd.PersistentFlags().BoolVarP(
		&flagVerbose,
		"verbose",
		"v",
		false,
		"verbose",
	)
	log.SetOutput(os.Stdout)
}

func run(c *cobra.Command, args []string) error {
	if len(args) == 0 {
		return errors.New("no eml given")
	}

	err := os.MkdirAll(imageCacheDir, 0777)
	if err != nil {
		return fmt.Errorf("cannot make cache dir %s", err)
	}

	_, err = os.Stat(*output)
	if !os.IsNotExist(err) {
		return fmt.Errorf("output file %s exist", *output)
	}

	book := epub.NewEpub(*title)
	{
		book.SetAuthor(*author)
		book.SetDescription(fmt.Sprintf("Email archive generated at %s with github.com/gonejack/email-to-epub", time.Now().Format("2006-01-02")))
	}

	// set cover
	{
		if *cover == "" {
			temp, err := os.CreateTemp("", "email-to-epub")
			if err != nil {
				return fmt.Errorf("cannot create tempfile: %s", err)
			}
			_, err = temp.Write(defaultCover)
			if err != nil {
				return fmt.Errorf("cannot write tempfile: %s", err)
			}
			_ = temp.Close()
			*cover = temp.Name()
		}
		cmime, err := mimetype.DetectFile(*cover)
		if err != nil {
			return fmt.Errorf("cannot detect cover mime type %s", err)
		}
		coverRef, err := book.AddImage(*cover, "epub-cover"+cmime.Extension())
		if err != nil {
			return fmt.Errorf("cannot add cover %s", err)
		}
		book.SetCover(coverRef, "")
	}

	// download images
	cache := make(map[string]string)
	for index, eml := range args {
		index = index + 1

		log.Printf("add %s", eml)

		file, err := os.Open(eml)
		if err != nil {
			return fmt.Errorf("cannot open file: %s", err)
		}

		mail, err := email.NewEmailFromReader(file)
		if err != nil {
			return fmt.Errorf("cannot parse email: %s", err)
		}

		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(mail.HTML))
		if err != nil {
			return fmt.Errorf("cannot parse HTML: %s", err)
		}

		doc = cleanDocument(doc)
		doc.Find("img").Each(func(i int, selection *goquery.Selection) {
			selection.RemoveAttr("loading")
			selection.RemoveAttr("srcset")

			src, exist := selection.Attr("src")
			if !exist {
				return
			}

			if !strings.HasPrefix(src, "http") {
				return
			}

			_, exist = cache[src]
			if !exist {
				if flagVerbose {
					log.Printf("save %s", src)
				}

				// download
				u, err := url.Parse(src)
				if err != nil {
					log.Printf("parse %s fail: %s", src, err)
					return
				}
				downloadFile := filepath.Join(imageCacheDir, fmt.Sprintf("%s%s", md5str(src), filepath.Ext(u.Path)))
				err = download(downloadFile, src)
				if err != nil {
					log.Printf("download %s fail: %s", src, err)
					return
				}

				// check mime
				imime, err := mimetype.DetectFile(downloadFile)
				if err != nil {
					log.Printf("cannot detect image mime of %s: %s", src, err)
					return
				}
				if !strings.HasPrefix(imime.String(), "image") {
					selection.Remove()
					log.Printf("mime of %s is %s instead of images", src, imime.String())
					return
				}

				// add image
				internalName := filepath.Base(downloadFile)
				if !strings.HasSuffix(internalName, imime.Extension()) {
					internalName += imime.Extension()
				}
				internalRef, err := book.AddImage(downloadFile, internalName)
				if err != nil {
					log.Printf("cannot add image %s", err)
					return
				}

				cache[src] = internalRef
			}
			selection.SetAttr("src", cache[src])
		})

		body, err := doc.Find("body").PrependHtml(info(mail)).Html()
		if err != nil {
			return fmt.Errorf("cannot generate body: %s", err)
		}

		// decode title
		title := mail.Subject
		decoded, err := decodeRFC2047(title)
		if err == nil {
			title = decoded
		}
		title = fmt.Sprintf("%d. %s", index, title)
		filename := fmt.Sprintf("page%d.html", index)

		_, err = book.AddSection(body, title, filename, "")
		if err != nil {
			return fmt.Errorf("cannot add section %s", err)
		}
	}

	err = book.Write(*output)
	if err != nil {
		return fmt.Errorf("cannot write output epub: %s", err)
	}

	return nil
}

func info(email *email.Email) string {
	var headers []string
	var header = func(label, text string) string {
		text, _ = decodeRFC2047(text)
		label, text = html.EscapeString(label), html.EscapeString(text)
		return fmt.Sprintf(`<p style="color:#999; margin: 8px;">%s:&nbsp;<span style="color:#666; text-decoration:none;">%s</span></p>`, label, text)
	}
	if len(email.ReplyTo) > 0 {
		headers = append(headers, header("ReplyTo", strings.Join(email.ReplyTo, ", ")))
	}
	headers = append(headers, header("From", email.From))
	headers = append(headers, header("To", strings.Join(email.To, ", ")))
	if len(email.Bcc) > 0 {
		headers = append(headers, header("Bcc", strings.Join(email.Bcc, ", ")))
	}
	if len(email.Cc) > 0 {
		headers = append(headers, header("Cc", strings.Join(email.Cc, ", ")))
	}
	headers = append(headers, header("Subject", email.Subject))

	if date := email.Headers.Get("Date"); date != "" {
		headers = append(headers, header("Date", date))
	}

	var box = `<div style="padding: 8px;">%s</div>`

	return fmt.Sprintf(box, strings.Join(headers, ""))
}

func download(path string, src string) (err error) {
	timeout, cancel := context.WithTimeout(context.TODO(), time.Minute*2)
	defer cancel()

	info, err := os.Stat(path)
	if err == nil {
		headReq, headErr := http.NewRequestWithContext(timeout, http.MethodHead, src, nil)
		if headErr != nil {
			return headErr
		}
		resp, headErr := client.Do(headReq)
		if headErr == nil && info.Size() == resp.ContentLength {
			return // skip download
		}
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return
	}
	defer file.Close()

	request, err := http.NewRequestWithContext(timeout, http.MethodGet, src, nil)
	if err != nil {
		return
	}
	response, err := client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()

	var written int64
	if flagVerbose {
		bar := progressbar.NewOptions64(response.ContentLength,
			progressbar.OptionSetTheme(progressbar.Theme{Saucer: "=", SaucerPadding: ".", BarStart: "|", BarEnd: "|"}),
			progressbar.OptionSetWidth(10),
			progressbar.OptionSpinnerType(11),
			progressbar.OptionShowBytes(true),
			progressbar.OptionShowCount(),
			progressbar.OptionSetPredictTime(false),
			progressbar.OptionSetDescription(filepath.Base(src)),
			progressbar.OptionSetRenderBlankState(true),
			progressbar.OptionClearOnFinish(),
		)
		defer bar.Clear()
		written, err = io.Copy(io.MultiWriter(file, bar), response.Body)
	} else {
		written, err = io.Copy(file, response.Body)
	}

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("response status code %d invalid", response.StatusCode)
	}

	if err == nil && written < response.ContentLength {
		err = fmt.Errorf("expected %s but downloaded %s", humanize.Bytes(uint64(response.ContentLength)), humanize.Bytes(uint64(written)))
	}

	return
}

func decodeRFC2047(word string) (string, error) {
	isRFC2047 := strings.HasPrefix(word, "=?") && strings.Contains(word, "?=")
	if isRFC2047 {
		isRFC2047 = strings.Contains(word, "?Q?") || strings.Contains(word, "?B?")
	}
	if !isRFC2047 {
		return word, nil
	}

	comps := strings.Split(word, "?")
	if len(comps) < 5 {
		return word, nil
	}

	if comps[2] == "B" && strings.HasSuffix(comps[3], "=") {
		b64s := strings.TrimRight(comps[3], "=")
		text, _ := base64.RawURLEncoding.DecodeString(b64s)
		comps[3] = base64.StdEncoding.EncodeToString(text)
	}

	return new(mime.WordDecoder).DecodeHeader(strings.Join(comps, "?"))
}

func cleanDocument(doc *goquery.Document) *goquery.Document {
	// remove inoreader ads
	doc.Find("body").Find(`div:contains("ads from inoreader")`).Closest("center").Remove()

	return doc
}

func md5str(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

func main() {
	_ = cmd.Execute()
}
