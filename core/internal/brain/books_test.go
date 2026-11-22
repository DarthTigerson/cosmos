package brain

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// testEPUB builds a small EPUB 3: a cover, a copyright page, two
// chapters (the second split over two pages) and acknowledgements.
func testEPUB(t *testing.T, extra map[string]string) []byte {
	t.Helper()
	page := func(body string) string {
		return `<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>x</title><style>p{}</style></head><body>` + body + `</body></html>`
	}
	long := strings.Repeat("A stock changes only through its flows, so stocks act as buffers. ", 220)
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title>Thinking in Systems</dc:title><dc:creator>Donella Meadows</dc:creator><dc:subject>Science</dc:subject><dc:subject>Nonfiction</dc:subject></metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="cover" href="images/cover.jpg" media-type="image/jpeg" properties="cover-image"/>
<item id="copy" href="text/copyright.xhtml" media-type="application/xhtml+xml"/>
<item id="c1" href="text/ch%201.xhtml" media-type="application/xhtml+xml"/>
<item id="c2a" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
<item id="c2b" href="text/ch2b.xhtml" media-type="application/xhtml+xml"/>
<item id="ack" href="text/ack.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine><itemref idref="copy"/><itemref idref="c1"/><itemref idref="c2a"/><itemref idref="c2b"/><itemref idref="ack"/></spine></package>`,
		"OEBPS/nav.xhtml":            page(`<nav epub:type="toc"><ol><li><a href="text/copyright.xhtml">Copyright</a></li><li><a href="text/ch%201.xhtml">The   Basics</a></li><li><a href="text/ch2.xhtml#top">A Brief Visit to the Systems Zoo</a></li><li><a href="text/ack.xhtml">Acknowledgments</a></li></ol></nav>`),
		"OEBPS/images/cover.jpg":     "not really a jpeg",
		"OEBPS/text/copyright.xhtml": page(`<p>Copyright 2008. All rights reserved and then some more words to pass the minimum length of a chapter here so it is only the title that leaves it out of the reading at all.</p>`),
		"OEBPS/text/ch 1.xhtml":      page(`<h1>The Basics</h1><p>` + long + `<sup>1</sup></p><script>var x = "hidden";</script>`),
		"OEBPS/text/ch2.xhtml":       page(`<h1>The Zoo</h1><p>A thermostat is a balancing feedback loop that holds a room near a goal.</p>`),
		"OEBPS/text/ch2b.xhtml":      page(`<p>Delays in a balancing loop make the system overshoot and oscillate around its goal, as anyone who has used a shower knows well enough.</p>`),
		"OEBPS/text/ack.xhtml":       page(`<p>` + strings.Repeat("Thanks to everyone. ", 40) + `</p>`),
	}
	for k, v := range extra {
		files[k] = v
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	return buf.Bytes()
}

func TestOpenEPUB(t *testing.T) {
	data := testEPUB(t, nil)
	book, err := openEPUB(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Thinking in Systems" || book.Author != "Donella Meadows" || string(book.Cover) != "not really a jpeg" {
		t.Fatalf("book = %q by %q, cover %q", book.Title, book.Author, book.Cover)
	}
	if len(book.Chapters) != 2 || book.Chapters[0].Title != "The Basics" || book.Chapters[1].Title != "A Brief Visit to the Systems Zoo" {
		t.Fatalf("chapters = %+v", book.Chapters)
	}
	two := book.Chapters[1].Text
	if !strings.Contains(two, "thermostat") || !strings.Contains(two, "oscillate") {
		t.Errorf("a chapter's second page belongs to it: %q", two)
	}
	one := book.Chapters[0].Text
	if strings.Contains(one, "hidden") || strings.Contains(one, "buffers. 1") {
		t.Errorf("scripts and footnote markers are not text: %q", one[len(one)-80:])
	}
	if fictionBySubjects([]string{"Science", "Nonfiction"}) || !fictionBySubjects([]string{"Fiction / Science Fiction"}) {
		t.Error("fiction is told from the subjects")
	}

	// Scrambled fonts are fine; scrambled text is DRM.
	fonts := `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#"><enc:EncryptedData><enc:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/></enc:EncryptedData></encryption>`
	data = testEPUB(t, map[string]string{"META-INF/encryption.xml": fonts})
	if _, err := openEPUB(bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("obfuscated fonts are not a lock: %v", err)
	}
	locked := strings.ReplaceAll(fonts, "http://www.idpf.org/2008/embedding", "http://www.w3.org/2001/04/xmlenc#aes128-cbc")
	data = testEPUB(t, map[string]string{"META-INF/encryption.xml": locked})
	if _, err := openEPUB(bytes.NewReader(data), int64(len(data))); err == nil || !strings.Contains(err.Error(), "DRM") {
		t.Fatalf("a locked book should be refused: %v", err)
	}
	if _, err := openEPUB(strings.NewReader("hello"), 5); err == nil {
		t.Fatal("that is not a book")
	}
}

func bookRig(t *testing.T) (*rig, *int) {
	r := newRig(t)
	r.b.BookDir = t.TempDir()
	parts := 0
	r.fake.Handlers["book-notes"] = func(q llm.Request) (string, error) {
		parts++
		return `{"notes":["Meadows says stocks change only through flows."]}`, nil
	}
	r.fake.Handlers["book-chapter"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.System, "Meadows argues") {
			t.Error("non-fiction lines should be credited to the author")
		}
		return `{"digest":"How stocks and flows work.","learned":["Meadows argues that stocks change only through their flows.","Meadows argues that stocks change only through their flows."]}`, nil
	}
	r.fake.Handlers["book-think"] = func(q llm.Request) (string, error) {
		return `{"summary":"A book about seeing systems.","thoughts":["I think the bathtub picture of stocks is the clearest part.","Stocks matter."],"share":"Meadows says delays make systems oscillate, which made me think of your thermostat project."}`, nil
	}
	return r, &parts
}

func TestSheReadsABookWhenItIsQuiet(t *testing.T) {
	r, parts := bookRig(t)
	book, err := r.b.AddBook(bytes.NewReader(testEPUB(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if book.Status != "queued" || len(book.Chapters) != 2 || book.Parts != 4 || !book.HasCover || book.Kind != "nonfiction" {
		t.Fatalf("book = %+v", book)
	}
	if _, err := r.b.AddBook(bytes.NewReader(testEPUB(t, nil))); err == nil {
		t.Fatal("the same book twice should be refused")
	}

	// The user has just written: she waits.
	r.b.S.AddEvent(store.Event{At: r.now.Add(-time.Minute), Kind: "user_msg", Actor: "Thomas", Text: "hi"})
	r.b.bookTick(context.Background())
	r.b.Wait()
	if *parts != 0 {
		t.Fatal("she should not read while the user is talking")
	}

	r.now = r.now.Add(time.Hour)
	r.b.bookTick(context.Background())
	r.b.Wait()
	v := r.b.Books()
	if len(v.Done) != 1 || v.Current != nil || v.Paused {
		t.Fatalf("the book should be finished: %+v", v)
	}
	done := v.Done[0]
	if *parts != 4 || done.Part != 4 || done.Asleep || done.Summary == "" || len(done.Chapters[0].Learned) != 1 {
		t.Fatalf("book = %+v (parts read: %d)", done, *parts)
	}
	if len(done.Thoughts) != 1 {
		t.Fatalf("only a view said as hers counts: %+v", done.Thoughts)
	}
	kept := r.b.BookMemories(done)
	reads := 0
	for _, m := range kept {
		if m.Kind == "read" {
			reads++
			if m.Importance != 3 || !strings.HasPrefix(m.Source, "Read in Thinking in Systems by Donella Meadows, chapter ") {
				t.Errorf("read memory = %+v", m)
			}
		}
	}
	if reads != 2 || len(kept) != 3 {
		t.Fatalf("kept = %+v", kept)
	}

	// What she read reaches the writer apart from what the user told her,
	// and apart from podcasts.
	_, notes, _ := r.b.writerParts(r.b.Cfg.Get(), r.b.Mood(), "stocks and flows")
	if !strings.Contains(notes, "From books you read on your own") || strings.Contains(notes, "From podcasts") {
		t.Fatalf("the writer was not told what she read:\n%s", notes)
	}
	open, _ := r.b.S.Intentions("open")
	if len(open) != 1 || open[0].Kind != "book" {
		t.Fatalf("she should want to bring it up: %+v", open)
	}
	if title := reachOutTitle(open[0]); title != "From Thinking in Systems" {
		t.Errorf("title = %q", title)
	}

	res, _ := r.b.searchBooks("thermostat")
	out := res.(map[string]any)
	if p, _ := out["passages"].([]map[string]any); len(p) == 0 || !strings.Contains(p[0]["text"].(string), "thermostat") {
		t.Fatalf("the book's own words should be found: %+v", out)
	}
	if s := r.b.SearchBook(done, "BUFFERS", 3); s.Total != 220 || len(s.Matches) != 3 || s.Matches[0].Chapter != 1 {
		t.Fatalf("search = %d matches, %+v", s.Total, s.Matches)
	}

	// Reading again replaces what she kept.
	if err := r.b.QueueBook(done.ID); err != nil {
		t.Fatal(err)
	}
	r.b.bookTick(context.Background())
	r.b.Wait()
	if kept := r.b.BookMemories(done); len(kept) != 3 {
		t.Fatalf("a second reading should replace the first: %d kept", len(kept))
	}
	if err := r.b.RemoveBook(done.ID); err != nil {
		t.Fatal(err)
	}
	if kept := r.b.BookMemories(done); len(kept) != 3 {
		t.Fatal("what she learned stays after the book is removed")
	}
}

func TestSheReadsInHerSleep(t *testing.T) {
	r, parts := bookRig(t)
	tiers := map[string]bool{}
	notes := r.fake.Handlers["book-notes"]
	r.fake.Handlers["book-notes"] = func(q llm.Request) (string, error) {
		tiers[q.Tier] = true
		return notes(q)
	}
	if _, err := r.b.AddBook(bytes.NewReader(testEPUB(t, nil))); err != nil {
		t.Fatal(err)
	}
	// Someone wrote a minute ago, but she is asleep: she reads anyway.
	r.b.S.AddEvent(store.Event{At: r.now.Add(-time.Minute), Kind: "user_msg", Actor: "Thomas", Text: "night"})
	r.b.readAsleep(context.Background())
	v := r.b.Books()
	if len(v.Done) != 1 || !v.Done[0].Asleep || *parts != 4 {
		t.Fatalf("she should finish the book asleep: %+v", v)
	}
	if len(tiers) != 1 || !tiers["sleeper"] {
		t.Fatalf("asleep, only the sleeper reads: %v", tiers)
	}
}

func TestFictionIsReadAsAStory(t *testing.T) {
	r, _ := bookRig(t)
	sawStory := false
	r.fake.Handlers["book-notes"] = func(q llm.Request) (string, error) {
		sawStory = strings.Contains(q.System, "novel or story")
		return `{"notes":["Grace wakes alone on a ship."]}`, nil
	}
	r.fake.Handlers["book-chapter"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.System, "never a fact about the real world") {
			t.Error("fiction must not become facts about the world")
		}
		return `{"digest":"Grace wakes up.","learned":["In Thinking in Systems, Grace wakes alone on a ship."]}`, nil
	}
	book, _ := r.b.AddBook(bytes.NewReader(testEPUB(t, nil)))
	if err := r.b.SetBookKind(book.ID, "fiction"); err != nil {
		t.Fatal(err)
	}
	if r.b.SetBookKind(book.ID, "poetry") == nil {
		t.Error("only fiction or nonfiction")
	}
	r.b.readAsleep(context.Background())
	if !sawStory {
		t.Fatal("a story should be read as one")
	}
}
