package brain

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// An EPUB is a zip of web pages. Its package file lists the pages in
// reading order (the spine) and its table of contents names the
// chapters. Everything is read here, on this computer.

const (
	epubMaxEntry = 32 << 20
	// A chapter shorter than this is a title page, a part divider or an
	// epigraph, not something to read.
	epubMinWords = 40
)

type epubChapter struct {
	Title string
	Text  string
}

type epubBook struct {
	Title     string
	Author    string
	Subjects  []string
	Chapters  []epubChapter
	Cover     []byte
	CoverType string // media type of Cover
}

type epubFiles map[string]*zip.File

func (f epubFiles) read(name string) ([]byte, error) {
	z, ok := f[name]
	if !ok {
		return nil, fmt.Errorf("the book has no %s", name)
	}
	r, err := z.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, epubMaxEntry+1))
	if err != nil {
		return nil, err
	}
	if len(data) > epubMaxEntry {
		return nil, fmt.Errorf("%s is too large", name)
	}
	return data, nil
}

// Fonts are often scrambled so they cannot be lifted from the book; that
// is not a lock on the text.
var fontObfuscation = map[string]bool{
	"http://www.idpf.org/2008/embedding": true,
	"http://ns.adobe.com/pdf/enc#RC":     true,
}

// locked reports whether the book's text is encrypted, as a bought book
// with DRM is.
func (f epubFiles) locked() bool {
	raw, err := f.read("META-INF/encryption.xml")
	if err != nil {
		return false
	}
	var enc struct {
		Data []struct {
			Method struct {
				Algorithm string `xml:"Algorithm,attr"`
			} `xml:"EncryptionMethod"`
		} `xml:"EncryptedData"`
	}
	if xml.Unmarshal(raw, &enc) != nil {
		return true
	}
	for _, d := range enc.Data {
		if !fontObfuscation[d.Method.Algorithm] {
			return true
		}
	}
	return false
}

type opfPackage struct {
	Metadata struct {
		Title   []string `xml:"title"`
		Creator []string `xml:"creator"`
		Subject []string `xml:"subject"`
		Meta    []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		Toc   string `xml:"toc,attr"`
		Items []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// resolve turns a link inside the book into the name of a file in the
// zip: relative to the page it is on, unescaped, without a #fragment.
func resolve(from, href string) string {
	href, _, _ = strings.Cut(href, "#")
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	if href == "" {
		return ""
	}
	return path.Clean(path.Join(path.Dir(from), href))
}

// openEPUB reads a book: what it is called, who wrote it, and its
// chapters as plain text, front and back matter left out.
func openEPUB(r io.ReaderAt, size int64) (epubBook, error) {
	var book epubBook
	z, err := zip.NewReader(r, size)
	if err != nil {
		return book, errors.New("this is not an EPUB file")
	}
	files := epubFiles{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	if files.locked() {
		return book, errors.New("this book is locked with DRM, so she cannot read it. Only DRM-free EPUBs work")
	}

	raw, err := files.read("META-INF/container.xml")
	if err != nil {
		return book, errors.New("this is not an EPUB file")
	}
	var container struct {
		Rootfiles []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(raw, &container); err != nil || len(container.Rootfiles) == 0 {
		return book, errors.New("the book's contents list could not be read")
	}
	opfPath := container.Rootfiles[0].Path
	raw, err = files.read(opfPath)
	if err != nil {
		return book, err
	}
	var opf opfPackage
	if err := xml.Unmarshal(raw, &opf); err != nil {
		return book, errors.New("the book's contents list could not be read")
	}
	if len(opf.Metadata.Title) > 0 {
		book.Title = strings.TrimSpace(opf.Metadata.Title[0])
	}
	if len(opf.Metadata.Creator) > 0 {
		book.Author = strings.TrimSpace(opf.Metadata.Creator[0])
	}
	book.Subjects = opf.Metadata.Subject

	byID := map[string]int{}
	for i, it := range opf.Manifest {
		byID[it.ID] = i
	}
	file := func(i int) string { return resolve(opfPath, opf.Manifest[i].Href) }

	// The cover: marked as such in EPUB 3, named in a meta in EPUB 2.
	coverAt := -1
	for i, it := range opf.Manifest {
		if strings.Contains(" "+it.Properties+" ", " cover-image ") {
			coverAt = i
		}
	}
	for _, m := range opf.Metadata.Meta {
		if i, ok := byID[m.Content]; ok && m.Name == "cover" && coverAt < 0 {
			coverAt = i
		}
	}
	if coverAt >= 0 && strings.HasPrefix(opf.Manifest[coverAt].MediaType, "image/") {
		if data, err := files.read(file(coverAt)); err == nil {
			book.Cover, book.CoverType = data, opf.Manifest[coverAt].MediaType
		}
	}

	// The table of contents names chapters by the page they start on.
	toc := []tocEntry{}
	for i, it := range opf.Manifest {
		if strings.Contains(" "+it.Properties+" ", " nav ") {
			if data, err := files.read(file(i)); err == nil {
				toc = navTOC(file(i), data)
			}
		}
	}
	if i, ok := byID[opf.Spine.Toc]; ok && len(toc) == 0 {
		if data, err := files.read(file(i)); err == nil {
			toc = ncxTOC(file(i), data)
		}
	}
	inSpine := map[string]bool{}
	for _, ref := range opf.Spine.Items {
		if i, ok := byID[ref.IDRef]; ok && ref.Linear != "no" {
			inSpine[file(i)] = true
		}
	}
	named := map[string]string{}
	for _, e := range toc {
		if _, seen := named[e.File]; !seen && e.Title != "" && inSpine[e.File] {
			named[e.File] = e.Title
		}
	}
	// A contents list that names only a page or two of a long book is no
	// guide to its chapters: each page is taken as one instead.
	if len(named) < 3 && len(inSpine) > 5 {
		named = map[string]string{}
	}

	type page struct{ text, heading string }
	pages := []page{}
	names := []string{}
	for _, ref := range opf.Spine.Items {
		i, ok := byID[ref.IDRef]
		if !ok || ref.Linear == "no" {
			continue
		}
		data, err := files.read(file(i))
		if err != nil {
			continue
		}
		text, heading := pageText(data)
		pages = append(pages, page{text, heading})
		names = append(names, file(i))
	}

	// Pages are read in spine order. A page the contents name starts a
	// chapter; the pages after it, until the next named one, belong to it.
	chapters := []epubChapter{}
	if len(named) == 0 {
		// Without contents, a chapter's title is its heading, or else the
		// short lines it opens with, less those nearly every page opens
		// with (a running header, the book's name).
		common := map[string]int{}
		for _, p := range pages {
			for _, l := range leadLines(p.text) {
				common[l]++
			}
		}
		for _, p := range pages {
			if len(strings.Fields(p.text)) < 150 {
				continue
			}
			// A heading here is often the book's name over each chapter,
			// so the lines nearest the text win: "One: Hungry Like a Wolf".
			own := []string{}
			for _, l := range leadLines(p.text) {
				if common[l]*2 <= len(pages) {
					own = append(own, l)
				}
			}
			title := strings.Join(own[max(0, len(own)-2):], ": ")
			if title == "" {
				title = p.heading
			}
			if title == "" {
				title = fmt.Sprintf("Section %d", len(chapters)+1)
			}
			chapters = append(chapters, epubChapter{Title: title, Text: p.text})
		}
	}
	for i, p := range pages {
		if len(named) == 0 {
			break
		}
		title, starts := named[names[i]]
		if starts || len(chapters) == 0 {
			chapters = append(chapters, epubChapter{Title: title, Text: p.text})
		} else {
			last := &chapters[len(chapters)-1]
			last.Text = strings.TrimSpace(last.Text + "\n\n" + p.text)
		}
	}
	for _, c := range chapters {
		// What comes before the first named chapter is the cover and
		// title pages, unless it is long enough to be the book itself.
		if c.Title == "" && len(strings.Fields(c.Text)) < 300 {
			continue
		}
		if c.Title == "" {
			c.Title = "Opening"
		}
		if notReading(c.Title) || len(strings.Fields(c.Text)) < epubMinWords {
			continue
		}
		book.Chapters = append(book.Chapters, c)
	}
	if len(book.Chapters) == 0 {
		return book, errors.New("she could not find any chapters with text in this book")
	}
	if book.Title == "" {
		book.Title = "Untitled book"
	}
	return book, nil
}

type tocEntry struct{ Title, File string }

// navTOC reads an EPUB 3 contents page: the links in its toc nav.
func navTOC(name string, data []byte) []tocEntry {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	out := []tocEntry{}
	var walk func(n *html.Node, inTOC bool)
	walk = func(n *html.Node, inTOC bool) {
		if n.Type == html.ElementNode && n.Data == "nav" {
			for _, a := range n.Attr {
				if a.Key == "epub:type" || a.Key == "type" || a.Key == "role" {
					inTOC = strings.Contains(a.Val, "toc")
				}
			}
		}
		if inTOC && n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					out = append(out, tocEntry{Title: oneLine(textOf(n)), File: resolve(name, a.Val)})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inTOC)
		}
	}
	walk(doc, false)
	return out
}

// ncxTOC reads an EPUB 2 contents file: each navPoint's label and the
// page it points at, in document order.
func ncxTOC(name string, data []byte) []tocEntry {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	out := []tocEntry{}
	label, inText := "", false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "navLabel":
				label = ""
			case "text":
				inText = true
			case "content":
				for _, a := range t.Attr {
					if a.Name.Local == "src" {
						out = append(out, tocEntry{Title: oneLine(label), File: resolve(name, a.Value)})
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "text" {
				inText = false
			}
		case xml.CharData:
			if inText {
				label += string(t)
			}
		}
	}
	return out
}

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "blockquote": true, "section": true, "article": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "pre": true, "dd": true, "dt": true, "figcaption": true, "hr": true,
}

// pageText turns one page of the book into plain text, a paragraph a
// line, and gives its first heading.
func pageText(data []byte) (string, string) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", ""
	}
	var sb strings.Builder
	heading := ""
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "head", "sup", "nav":
				// Footnote markers are noise in the middle of a sentence.
				return
			case "h1", "h2", "h3":
				if heading == "" {
					heading = oneLine(textOf(n))
				}
			}
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockTags[n.Data] {
			sb.WriteString("\n")
		}
	}
	walk(doc)
	lines := []string{}
	for _, l := range strings.Split(sb.String(), "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n"), heading
}

// leadLines are the short lines a page opens with, before its first
// real paragraph: where a chapter's number and name usually are.
func leadLines(text string) []string {
	out := []string{}
	for _, l := range strings.Split(text, "\n") {
		if len([]rune(l)) > 70 || len(out) == 5 {
			break
		}
		out = append(out, l)
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Pages that are about the book rather than the book itself.
var (
	notReadingExact  = map[string]bool{"cover": true, "title page": true, "title": true, "copyright": true, "contents": true, "table of contents": true, "dedication": true, "epigraph": true, "index": true, "notes": true, "endnotes": true, "bibliography": true, "references": true, "further reading": true, "credits": true, "permissions": true, "colophon": true, "half title": true, "imprint": true}
	notReadingPrefix = []string{"acknowledg", "about the author", "about the publisher", "also by", "praise for", "other books by", "more from", "copyright", "sign up", "newsletter", "a note on the author", "select bibliography"}
)

func notReading(title string) bool {
	t := norm(title)
	if notReadingExact[t] {
		return true
	}
	for _, p := range notReadingPrefix {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// fictionBySubjects guesses from the book's own subjects whether it is a
// story. Without a clear answer it is read as non-fiction; the user can
// change it.
func fictionBySubjects(subjects []string) bool {
	fiction, non := 0, 0
	for _, s := range subjects {
		s = strings.ToLower(s)
		switch {
		case strings.Contains(s, "nonfiction"), strings.Contains(s, "non-fiction"):
			non++
		case strings.Contains(s, "fiction"), strings.Contains(s, "novel"):
			fiction++
		}
	}
	return fiction > non
}
