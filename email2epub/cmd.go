package email2epub

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/bmaupin/go-epub"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gonejack/email"
	"github.com/gonejack/get"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

type EmailToEpub struct {
	Options
	DefaultCover []byte

	book *epub.Epub
}

func (c *EmailToEpub) Run() (err error) {
	_, exx := os.Stat(c.Output)
	if !os.IsNotExist(exx) {
		return fmt.Errorf("output file %s already exist", c.Output)
	}
	if len(c.EML) == 0 {
		return errors.New("no eml given")
	}
	err = c.makeBook()
	if err != nil {
		return
	}
	return c.run()
}

func (c *EmailToEpub) run() (err error) {
	for i, eml := range c.EML {
		index := i + 1

		log.Printf("adding %s", eml)

		mail, err := c.openEmail(eml)
		if err != nil {
			return err
		}

		attachments, err := c.extractAttachments(eml, mail)
		if err != nil {
			return fmt.Errorf("cannot extract attachments %s", err)
		}

		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(mail.HTML))
		if err != nil {
			return fmt.Errorf("cannot parse HTML: %s", err)
		}

		if len(mail.HTML) > 0 {
			doc = c.cleanDoc(doc)
			savedImages := c.saveImages(doc)
			doc.Find("img").Each(func(i int, img *goquery.Selection) {
				c.changeRef(img, attachments, savedImages)
			})
		} else {
			c.insertImages(eml, doc, attachments)
		}

		info := c.renderInfo(mail)
		body, err := doc.Find("body").PrependHtml(info).Html()
		if err != nil {
			return fmt.Errorf("cannot generate body: %s", err)
		}

		title := fmt.Sprintf("%d. %s", index, c.renderTitle(mail))
		filename := fmt.Sprintf("page%d.html", index)
		_, err = c.book.AddSection(body, title, filename, "")
		if err != nil {
			return fmt.Errorf("cannot add section %s", err)
		}
	}
	err = c.book.Write(c.Output)
	if err != nil {
		return fmt.Errorf("cannot write output epub: %s", err)
	}
	return
}
func (c *EmailToEpub) makeBook() (err error) {
	c.book = epub.NewEpub(c.Title)
	c.setAuthor()
	c.setDesc()
	return c.setCover()
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
	fd, err := os.Open(eml)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %s", err)
	}
	defer fd.Close()

	mail, err := email.NewEmailFromReader(fd)
	if err != nil {
		return nil, fmt.Errorf("cannot parse email: %s", err)
	}
	return mail, nil
}

func (c *EmailToEpub) saveImages(doc *goquery.Document) map[string]string {
	downloads := make(map[string]string)
	tasks := get.NewDownloadTasks()

	doc.Find("img").Each(func(i int, img *goquery.Selection) {
		src, _ := img.Attr("src")
		if !strings.HasPrefix(src, "http") {
			return
		}

		localFile, exist := downloads[src]
		if exist {
			return
		}

		uri, err := url.Parse(src)
		if err != nil {
			log.Printf("parse %s fail: %s", src, err)
			return
		}
		_ = os.MkdirAll(c.ImagesDir, 0766)
		localFile = filepath.Join(c.ImagesDir, fmt.Sprintf("%s%s", md5str(src), filepath.Ext(uri.Path)))

		tasks.Add(src, localFile)
		downloads[src] = localFile
	})

	get.Batch(tasks, 3, time.Minute*2).ForEach(func(t *get.DownloadTask) {
		if t.Err != nil {
			log.Printf("download %s fail: %s", t.Link, t.Err)
		}
	})

	return downloads
}
func (c *EmailToEpub) extractAttachments(eml string, mail *email.Email) (attachments map[string]string, err error) {
	attachments = make(map[string]string)
	for i, a := range mail.Attachments {
		if c.Verbose {
			log.Printf("extract %s", a.Filename)
		}

		saveFile := md5str(fmt.Sprintf("%s.%s.%d", filepath.Base(eml), mail.Subject, i))
		saveFile = saveFile + filepath.Ext(a.Filename)
		saveFile = filepath.Join(c.AttachmentsDir, saveFile)
		_ = os.MkdirAll(c.AttachmentsDir, 0766)
		err = os.WriteFile(saveFile, a.Content, 0766)
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
func (c *EmailToEpub) insertImages(eml string, doc *goquery.Document, attachments map[string]string) {
	var processed = make(map[string]bool)

	var index = 0
	for _, localFile := range attachments {
		_, exist := processed[localFile]
		if exist {
			continue
		} else {
			processed[localFile] = true
			index += 1
		}

		// check mime
		fmime, err := mimetype.DetectFile(localFile)
		if err != nil {
			log.Printf("cannot detect image mime of %s: %s", localFile, err)
			continue
		}
		if !strings.HasPrefix(fmime.String(), "image") {
			continue
		}

		// add image
		internalName := filepath.Base(fmt.Sprintf("%s_attachment_%d", md5str(eml), index))
		if !strings.HasSuffix(internalName, fmime.Extension()) {
			internalName += fmime.Extension()
		}
		internalRef, err := c.book.AddImage(localFile, internalName)
		if err != nil {
			log.Printf("cannot add image %s", err)
			continue
		}
		doc.Find("body").AppendHtml(fmt.Sprintf(`<img src="%s" />`, internalRef))
	}
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

func (_ *EmailToEpub) renderInfo(email *email.Email) string {
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
func (_ *EmailToEpub) renderTitle(mail *email.Email) string {
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

	wd := &mime.WordDecoder{
		CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
			enc, err := htmlindex.Get(charset)
			if err != nil {
				return nil, err
			}
			return transform.NewReader(input, enc.NewDecoder()), nil
		},
	}
	return wd.DecodeHeader(strings.Join(comps, "?"))
}
func md5str(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}
