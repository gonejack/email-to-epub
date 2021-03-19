package cmd

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/bmaupin/go-epub"
	"github.com/dustin/go-humanize"
	"github.com/gabriel-vasile/mimetype"
	"github.com/jordan-wright/email"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"
)

type EmailToEpub struct {
	client http.Client

	DefaultCover   []byte
	ImagesDir      string
	AttachmentsDir string

	Cover   string
	Title   string
	Author  string
	Verbose bool

	book *epub.Epub
}

func (c *EmailToEpub) Execute(emails []string, output string) (err error) {
	if len(emails) == 0 {
		return errors.New("no eml given")
	}

	err = c.mkdirs()
	if err != nil {
		return
	}

	c.book = epub.NewEpub(c.Title)
	{
		c.setAuthor()
		c.setDesc()
		err = c.setCover()
		if err != nil {
			return
		}
	}

	for i, eml := range emails {
		index := i + 1

		log.Printf("add %s", eml)

		mail, err := c.openEmail(eml)
		if err != nil {
			return err
		}

		attachments, err := c.extractAttachments(mail)
		if err != nil {
			return fmt.Errorf("cannot extract attachments %s", err)
		}

		document, err := goquery.NewDocumentFromReader(bytes.NewReader(mail.HTML))
		if err != nil {
			return fmt.Errorf("cannot parse HTML: %s", err)
		}

		document = c.cleanDoc(document)
		downloads := c.downloadImages(document)

		document.Find("img").Each(func(i int, img *goquery.Selection) {
			c.changeRef(img, attachments, downloads)
		})

		info := c.mainInfo(mail)
		body, err := document.Find("body").PrependHtml(info).Html()
		if err != nil {
			return fmt.Errorf("cannot generate body: %s", err)
		}

		title := fmt.Sprintf("%d. %s", index, c.mailTitle(mail))
		filename := fmt.Sprintf("page%d.html", index)
		_, err = c.book.AddSection(body, title, filename, "")
		if err != nil {
			return fmt.Errorf("cannot add section %s", err)
		}
	}

	err = c.book.Write(output)
	if err != nil {
		return fmt.Errorf("cannot write output epub: %s", err)
	}

	return nil
}

func (c *EmailToEpub) mkdirs() error {
	err := os.MkdirAll(c.ImagesDir, 0777)
	if err != nil {
		return fmt.Errorf("cannot make images dir %s", err)
	}
	err = os.MkdirAll(c.AttachmentsDir, 0777)
	if err != nil {
		return fmt.Errorf("cannot make attachments dir %s", err)
	}

	return nil
}
func (c *EmailToEpub) setAuthor() {
	c.book.SetAuthor(c.Author)
}
func (c *EmailToEpub) setDesc() {
	c.book.SetDescription(fmt.Sprintf("Email archive generated at %s with github.com/gonejack/email-to-epub", time.Now().Format("2006-01-02")))
}
func (c *EmailToEpub) setCover() (err error) {
	if c.Cover == "" {
		temp, err := os.CreateTemp("", "email-to-epub")
		if err != nil {
			return fmt.Errorf("cannot create tempfile: %s", err)
		}
		_, err = temp.Write(c.DefaultCover)
		if err != nil {
			return fmt.Errorf("cannot write tempfile: %s", err)
		}
		_ = temp.Close()
		c.Cover = temp.Name()
	}

	fmime, err := mimetype.DetectFile(c.Cover)
	if err != nil {
		return fmt.Errorf("cannot detect cover mime type %s", err)
	}
	coverRef, err := c.book.AddImage(c.Cover, "epub-cover"+fmime.Extension())
	if err != nil {
		return fmt.Errorf("cannot add cover %s", err)
	}
	c.book.SetCover(coverRef, "")

	return
}
func (c *EmailToEpub) openEmail(eml string) (*email.Email, error) {
	file, err := os.Open(eml)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %s", err)
	}
	defer file.Close()
	mail, err := email.NewEmailFromReader(file)
	if err != nil {
		return nil, fmt.Errorf("cannot parse email: %s", err)
	}
	return mail, nil
}

func (c *EmailToEpub) downloadImages(doc *goquery.Document) map[string]string {
	downloads := make(map[string]string)

	var group errgroup.Group
	doc.Find("img").Each(func(i int, img *goquery.Selection) {
		src, _ := img.Attr("src")
		if !strings.HasPrefix(src, "http") {
			return
		}

		localFile, exist := downloads[src]
		if exist {
			return
		}

		if c.Verbose {
			log.Printf("fetch %s", src)
		}

		uri, err := url.Parse(src)
		if err != nil {
			log.Printf("parse %s fail: %s", src, err)
			return
		}
		localFile = filepath.Join(c.ImagesDir, fmt.Sprintf("%s%s", md5str(src), filepath.Ext(uri.Path)))

		downloads[src] = localFile

		group.Go(func() error {
			err := c.download(localFile, src)
			if err != nil {
				log.Printf("download %s fail: %s", src, err)
			}
			return nil
		})
	})

	_ = group.Wait()

	return downloads
}
func (c *EmailToEpub) extractAttachments(mail *email.Email) (attachments map[string]string, err error) {
	attachments = make(map[string]string)
	for i, a := range mail.Attachments {
		if c.Verbose {
			log.Printf("extract %s", a.Filename)
		}

		saveFile := filepath.Join(c.AttachmentsDir, fmt.Sprintf("%d#%s", i, a.Filename))
		err = ioutil.WriteFile(saveFile, a.Content, 0777)
		if err != nil {
			log.Printf("cannot extact image %s", a.Filename)
			continue
		}
		cid := a.Header.Get("Content-ID")
		cid = strings.TrimPrefix(cid, "<")
		cid = strings.TrimSuffix(cid, ">")
		attachments[cid] = saveFile
		attachments[a.Filename] = saveFile
	}
	return
}
func (c *EmailToEpub) changeRef(img *goquery.Selection, attachments, downloads map[string]string) {
	img.RemoveAttr("loading")
	img.RemoveAttr("srcset")

	src, _ := img.Attr("src")

	switch {
	case strings.HasPrefix(src, "http"):
		localFile := downloads[src]

		if c.Verbose {
			log.Printf("replace %s as %s", src, localFile)
		}

		// check mime
		fmime, err := mimetype.DetectFile(localFile)
		if err != nil {
			log.Printf("cannot detect image mime of %s: %s", src, err)
			return
		}
		if !strings.HasPrefix(fmime.String(), "image") {
			img.Remove()
			log.Printf("mime of %s is %s instead of images", src, fmime.String())
			return
		}

		// add image
		internalName := filepath.Base(localFile)
		if !strings.HasSuffix(internalName, fmime.Extension()) {
			internalName += fmime.Extension()
		}
		internalRef, err := c.book.AddImage(localFile, internalName)
		if err != nil {
			log.Printf("cannot add image %s", err)
			return
		}

		img.SetAttr("src", internalRef)
	case strings.HasPrefix(src, "cid:"):
		contentId := strings.TrimPrefix(src, "cid:")

		localFile, exist := attachments[contentId]
		if !exist {
			log.Printf("content id %s not found", contentId)
			return
		}

		// check mime
		fmime, err := mimetype.DetectFile(localFile)
		if err != nil {
			log.Printf("cannot detect image mime of %s: %s", src, err)
			return
		}

		if c.Verbose {
			log.Printf("replace %s as %s", src, localFile)
		}

		// add image
		internalName := filepath.Base(fmt.Sprintf("attachment_%s", contentId))
		if !strings.HasSuffix(internalName, fmime.Extension()) {
			internalName += fmime.Extension()
		}
		internalRef, err := c.book.AddImage(localFile, internalName)
		if err != nil {
			log.Printf("cannot add image %s", err)
			return
		}

		img.SetAttr("src", internalRef)
	default:
		log.Printf("unsupported image reference[src=%s]", src)
	}
}
func (c *EmailToEpub) download(path string, src string) (err error) {
	timeout, cancel := context.WithTimeout(context.TODO(), time.Minute*2)
	defer cancel()

	info, err := os.Stat(path)
	if err == nil {
		headReq, headErr := http.NewRequestWithContext(timeout, http.MethodHead, src, nil)
		if headErr != nil {
			return headErr
		}
		resp, headErr := c.client.Do(headReq)
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
	response, err := c.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()

	var written int64
	if c.Verbose {
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

func (_ *EmailToEpub) mainInfo(email *email.Email) string {
	var header = func(label, text string) string {
		text, _ = decodeRFC2047(text)
		label, text = html.EscapeString(label), html.EscapeString(text)
		return fmt.Sprintf(`<p style="color:#999; margin: 8px;">%s:&nbsp;<span style="color:#666; text-decoration:none;">%s</span></p>`, label, text)
	}

	var headers []string
	headers = append(headers, header("From", email.From))
	headers = append(headers, header("To", strings.Join(email.To, ", ")))

	if len(email.ReplyTo) > 0 {
		headers = append(headers, header("ReplyTo", strings.Join(email.ReplyTo, ", ")))
	}
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
func (_ *EmailToEpub) mailTitle(mail *email.Email) string {
	title := mail.Subject
	decoded, err := decodeRFC2047(title)
	if err == nil {
		title = decoded
	}
	return title
}
func (_ *EmailToEpub) cleanDoc(doc *goquery.Document) *goquery.Document {
	// remove inoreader ads
	doc.Find("body").Find(`div:contains("ads from inoreader")`).Closest("center").Remove()

	return doc
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
func md5str(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}
