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
	imagesCacheDirectory = "images"

	//go:embed cover.png
	defaultCover []byte

	title  *string
	cover  *string
	output *string

	flagVerbose = false

	client = http.Client{}
	cmd    = &cobra.Command{
		Use:   "email-to-epub [-o output] [-title title] [-cover cover] *.eml",
		Short: "Command line tool to convert email(s) to epub.",
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
	output = cmd.PersistentFlags().StringP(
		"output",
		"o",
		"output.epub",
		"output filename",
	)
	title = cmd.PersistentFlags().StringP(
		"title",
		"",
		"Email",
		"epub title",
	)
	cover = cmd.PersistentFlags().StringP(
		"cover",
		"",
		"",
		"cover image",
	)
	cmd.PersistentFlags().BoolVarP(
		&flagVerbose,
		"verbose",
		"v",
		false,
		"verbose",
	)
}

func run(c *cobra.Command, args []string) error {
	if len(args) == 0 {
		return errors.New("no eml given")
	}

	err := os.MkdirAll(imagesCacheDirectory, 0777)
	if err != nil {
		return fmt.Errorf("cannot make cache dir %s", err)
	}

	_, err = os.Stat(*output)
	if !os.IsNotExist(err) {
		return fmt.Errorf("output target %s exist", *output)
	}

	book := epub.NewEpub(*title)
	{
		book.SetAuthor("Email Epub")
		book.SetDescription(fmt.Sprintf("email archive generated at %s", time.Now().Format("2006-01-02")))
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
		mime, err := mimetype.DetectFile(*cover)
		if err != nil {
			return fmt.Errorf("cannot detect cover mime type %s", err)
		}
		coverRef, err := book.AddImage(*cover, "epubCover"+mime.Extension())
		if err != nil {
			return fmt.Errorf("cannot add cover %s", err)
		}
		book.SetCover(coverRef, "")
	}

	// download images
	images := make(map[string]string)
	for _, eml := range args {
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

			_, exist = images[src]
			if !exist {
				if flagVerbose {
					log.Printf("save %s", src)
				}

				// download
				target := filepath.Join(imagesCacheDirectory, fmt.Sprintf("%s%s", md5str(src), filepath.Ext(src)))
				err := download(target, src)
				if err != nil {
					log.Printf("download %s fail: %s", src, err)
					return
				}

				// check mime
				imime, err := mimetype.DetectFile(target)
				if err != nil {
					log.Printf("cannot detect image mime of %s: %s", src, err)
					return
				}
				if !strings.HasPrefix(imime.String(), "image") {
					log.Printf("mime of %s is %s instead of images", src, imime.String())
					return
				}

				// add image
				localName := filepath.Base(target)
				if !strings.HasSuffix(localName, imime.Extension()) {
					localName += imime.Extension()
				}
				localRef, err := book.AddImage(target, localName)
				if err != nil {
					log.Printf("cannot add image %s", err)
					return
				}

				images[src] = localRef
			}
			selection.SetAttr("src", images[src])
		})

		body, err := doc.Find("body").PrependHtml(info(mail)).Html()
		if err != nil {
			return fmt.Errorf("cannot generate body: %s", err)
		}

		// decode subject
		subject := mail.Subject
		decoded, err := decodeWord(subject)
		if err == nil {
			subject = decoded
		}

		_, err = book.AddSection(body, subject, "", "")
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
	var header = func(label, body string) string {
		body, _ = decodeWord(body)
		label, body = html.EscapeString(label), html.EscapeString(body)
		return fmt.Sprintf(`<p style="color:#999; margin: 8px;">%s:&nbsp;<span style="color:#666; text-decoration:none;">%s</span></p>`, label, body)
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

	req, err := http.NewRequestWithContext(timeout, http.MethodGet, src, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("response status code %d invalid", resp.StatusCode)
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return
	}
	defer file.Close()

	var written int64
	if flagVerbose {
		bar := progressbar.NewOptions64(resp.ContentLength,
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
		written, err = io.Copy(io.MultiWriter(file, bar), resp.Body)
	} else {
		written, err = io.Copy(file, resp.Body)
	}

	if err == nil && written < resp.ContentLength {
		err = fmt.Errorf("expected %s but downloaded %s", humanize.Bytes(uint64(resp.ContentLength)), humanize.Bytes(uint64(written)))
	}

	return
}

// decode RFC 2047
func decodeWord(word string) (string, error) {
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

	if comps[2] == "B" {
		b64s := strings.TrimRight(comps[3], "=")
		text, _ := base64.RawURLEncoding.DecodeString(b64s)
		comps[3] = base64.StdEncoding.EncodeToString(text)
	}

	return new(mime.WordDecoder).DecodeHeader(strings.Join(comps, "?"))
}

func md5str(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

func main() {
	_ = cmd.Execute()
}
