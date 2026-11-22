package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// She reads the books the user gives her: books they have read and
// trust. A book is read straight through, part by part, but only while
// the user is quiet or she is asleep, and every part is saved as it is
// done, so reading stops and picks up again anywhere. What she takes from
// a book goes into her memory as read, credited to its author.

const (
	bookMaxFile   = 256 << 20
	bookPartWords = 1200
	// She starts on a part only when nothing has been said for this long.
	bookQuietFor = 10 * time.Minute
	// Asleep, reading takes at most this much of the night.
	bookSleepFor = 90 * time.Minute
)

func (b *Brain) booksChanged() { b.Bus.Publish("books", nil) }

func coverExt(mediaType string) string {
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	return ".jpg"
}

// AddBook takes an EPUB the user gave her and puts it on her shelf, first
// in line after what she is reading now.
func (b *Brain) AddBook(r io.Reader) (store.Book, error) {
	if b.BookDir == "" {
		return store.Book{}, errors.New("books are switched off")
	}
	if err := os.MkdirAll(b.BookDir, 0o700); err != nil {
		return store.Book{}, err
	}
	tmp, err := os.CreateTemp(b.BookDir, "upload-*.epub")
	if err != nil {
		return store.Book{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, err := io.Copy(tmp, io.LimitReader(r, bookMaxFile+1))
	if err != nil {
		return store.Book{}, err
	}
	if n > bookMaxFile {
		return store.Book{}, errors.New("the file is too large for a book")
	}
	parsed, err := openEPUB(tmp, n)
	if err != nil {
		return store.Book{}, err
	}
	shelf, _ := b.S.Books()
	for _, have := range shelf {
		if norm(have.Title) == norm(parsed.Title) && norm(have.Author) == norm(parsed.Author) {
			return store.Book{}, errors.New(have.Title + " is already on her shelf")
		}
	}

	kind := "nonfiction"
	if fictionBySubjects(parsed.Subjects) {
		kind = "fiction"
	}
	book, err := b.S.AddBook(store.Book{Title: parsed.Title, Author: parsed.Author, Kind: kind, AddedAt: b.Now(), Status: "queued"})
	if err != nil {
		return store.Book{}, err
	}
	fail := func(err error) (store.Book, error) {
		b.S.DeleteBook(book.ID)
		os.RemoveAll(book.Dir)
		return store.Book{}, err
	}
	book.Dir = filepath.Join(b.BookDir, strconv.FormatInt(book.ID, 10))
	if err := os.MkdirAll(book.Dir, 0o700); err != nil {
		return fail(err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), filepath.Join(book.Dir, "book.epub")); err != nil {
		return fail(err)
	}
	for i, c := range parsed.Chapters {
		if err := os.WriteFile(chapterFile(book, i+1), []byte(c.Text), 0o600); err != nil {
			return fail(err)
		}
		words := len(strings.Fields(c.Text))
		parts := (words + bookPartWords - 1) / bookPartWords
		book.Chapters = append(book.Chapters, store.BookChapter{N: i + 1, Title: c.Title, Words: words, Parts: parts, Notes: []string{}, Learned: []string{}})
		book.Words += words
		book.Parts += parts
	}
	if len(parsed.Cover) > 0 {
		cover := filepath.Join(book.Dir, "cover"+coverExt(parsed.CoverType))
		if os.WriteFile(cover, parsed.Cover, 0o600) == nil {
			book.CoverPath = cover
		}
	}
	if err := b.S.SaveBook(book); err != nil {
		return fail(err)
	}
	b.Log.Printf("books: added %q by %s, %d chapters, %d words", book.Title, book.Author, len(book.Chapters), book.Words)
	b.booksChanged()
	book, _ = b.S.GetBook(book.ID)
	return book, nil
}

func chapterFile(book store.Book, n int) string {
	return filepath.Join(book.Dir, fmt.Sprintf("chapter-%d.txt", n))
}

// RemoveBook takes a book off her shelf and deletes its files. What she
// learned from it stays with her.
func (b *Brain) RemoveBook(id int64) error {
	book, ok := b.S.GetBook(id)
	if !ok {
		return errors.New("no such book")
	}
	if reading(book) {
		return errors.New("she is reading it right now; wait until she has finished")
	}
	if err := b.S.DeleteBook(id); err != nil {
		return err
	}
	if book.Dir != "" {
		os.RemoveAll(book.Dir)
	}
	b.booksChanged()
	return nil
}

func reading(book store.Book) bool {
	return book.Status == "reading" || book.Status == "digesting" || book.Status == "thinking" || book.Status == "opening"
}

// QueueBook asks her to read a book, again or for the first time, or to
// try once more one that failed.
func (b *Brain) QueueBook(id int64) error {
	book, ok := b.S.GetBook(id)
	if !ok {
		return errors.New("no such book")
	}
	if reading(book) || book.Status == "queued" {
		return nil
	}
	book.Status, book.Error, book.StageNote = "queued", "", ""
	b.S.SaveBook(book)
	b.booksChanged()
	return nil
}

// UnqueueBook takes a book out of the queue before she starts on it.
func (b *Brain) UnqueueBook(id int64) error {
	book, ok := b.S.GetBook(id)
	if !ok {
		return errors.New("no such book")
	}
	if book.Status != "queued" {
		return errors.New("it is not waiting to be read")
	}
	book.Status = "waiting"
	b.S.SaveBook(book)
	b.booksChanged()
	return nil
}

// SetBookKind says whether a book is read as a story or as non-fiction.
// It counts from the next reading.
func (b *Brain) SetBookKind(id int64, kind string) error {
	if kind != "fiction" && kind != "nonfiction" {
		return errors.New("a book is fiction or nonfiction")
	}
	book, ok := b.S.GetBook(id)
	if !ok {
		return errors.New("no such book")
	}
	book.Kind = kind
	b.S.SaveBook(book)
	b.booksChanged()
	return nil
}

// ---------- reading on her own ----------

// quietForBooks reports whether the user has left her alone long enough
// for her to read.
func (b *Brain) quietForBooks() bool {
	if b.wanted.Load() > 0 {
		return false
	}
	last, _ := b.S.RecentEvents(1, "user_msg")
	return len(last) == 0 || b.Now().Sub(last[0].At) >= bookQuietFor
}

// bookTick is called from the heartbeat. It never blocks: reading runs
// beside everything else, one part at a time, while the user is quiet.
func (b *Brain) bookTick(ctx context.Context) {
	if b.BookDir == "" || !b.Cfg.Get().Books || !b.bookBusy.CompareAndSwap(false, true) {
		return
	}
	b.bg.Add(1)
	go func() {
		defer b.bg.Done()
		defer b.bookBusy.Store(false)
		ctx := context.WithoutCancel(ctx)
		for b.quietForBooks() {
			b.lockForBackground()
			// While she slept the book may have been read on; each part
			// starts again from what is saved.
			more := b.S.Get("state") != "asleep" && b.quietForBooks() && b.bookStep(ctx, false)
			b.turn.Unlock()
			if !more {
				return
			}
		}
		b.booksChanged() // the screen shows she is waiting
	}()
}

// readAsleep reads on while she sleeps, with the sleeper model that is
// already loaded. The caller holds the model. A message stops it.
func (b *Brain) readAsleep(ctx context.Context) {
	if b.BookDir == "" || !b.Cfg.Get().Books {
		return
	}
	until := time.Now().Add(bookSleepFor)
	for time.Now().Before(until) && b.wanted.Load() == 0 && ctx.Err() == nil {
		if !b.bookStep(ctx, true) {
			return
		}
	}
}

// bookStep does the next piece of work on the book she is reading: one
// part, one chapter thought over, or the whole book thought over. The
// caller holds the model. It reports false when there is nothing to do.
func (b *Brain) bookStep(ctx context.Context, asleep bool) bool {
	book, ok := b.nextBook()
	if !ok {
		return false
	}
	cfg := b.Cfg.Get()
	if asleep {
		book.Asleep = true
	}
	save := func(status, note string) {
		book.Status, book.StageNote = status, note
		b.S.SaveBook(book)
		b.booksChanged()
	}
	// Notes are quick work for the small model; asleep, the waking models
	// are put away and the sleeper does it all.
	tier := "worker"
	if asleep {
		tier = "sleeper"
	}

	switch book.Status {
	case "queued":
		// A fresh start: whatever an earlier reading left is cleared.
		book.Part, book.Error, book.Summary, book.Share, book.Thoughts = 0, "", "", "", []string{}
		book.StartedAt, book.FinishedAt, book.Asleep = b.Now(), time.Time{}, asleep
		for i := range book.Chapters {
			c := &book.Chapters[i]
			c.Notes, c.Learned, c.Digest, c.Digested = []string{}, []string{}, "", false
		}
		save("reading", fmt.Sprintf("chapter 1 of %d · part 1 of %d", len(book.Chapters), book.Parts))

	case "reading":
		ci, pi := partAt(book, book.Part)
		if ci < 0 {
			save("digesting", fmt.Sprintf("chapter 1 of %d", len(book.Chapters)))
			return true
		}
		c := &book.Chapters[ci]
		raw, err := os.ReadFile(chapterFile(book, c.N))
		if err != nil {
			b.failBook(book, fmt.Errorf("the text of chapter %d is missing: %w", c.N, err))
			return true
		}
		words := strings.Fields(string(raw))
		chunk := strings.Join(words[min(pi*bookPartWords, len(words)):min((pi+1)*bookPartWords, len(words))], " ")
		if notes, err := b.readBookPart(ctx, cfg, book, *c, chunk, pi+1, tier); err != nil {
			// One part she could not make sense of is not the book.
			b.Log.Printf("books: %q chapter %d part %d: %v", book.Title, c.N, pi+1, err)
		} else {
			c.Notes = append(c.Notes, notes...)
		}
		book.Part++
		if ci, _ := partAt(book, book.Part); ci >= 0 {
			save("reading", fmt.Sprintf("chapter %d of %d · part %d of %d", ci+1, len(book.Chapters), book.Part+1, book.Parts))
		} else {
			save("digesting", fmt.Sprintf("chapter 1 of %d", len(book.Chapters)))
		}

	case "digesting":
		for i := range book.Chapters {
			c := &book.Chapters[i]
			if c.Digested {
				continue
			}
			if len(c.Notes) > 0 {
				if err := b.digestChapter(ctx, cfg, book, c); err != nil {
					b.Log.Printf("books: thinking over %q chapter %d: %v", book.Title, c.N, err)
				}
			}
			c.Digested = true
			if i+1 < len(book.Chapters) {
				save("digesting", fmt.Sprintf("chapter %d of %d", i+2, len(book.Chapters)))
			} else {
				save("thinking", "")
			}
			return true
		}
		save("thinking", "")

	case "thinking":
		learned := 0
		for _, c := range book.Chapters {
			learned += len(c.Learned)
		}
		if learned == 0 {
			b.failBook(book, errors.New("she could not take anything from it"))
			return true
		}
		if err := b.thinkOverBook(ctx, cfg, &book); err != nil {
			b.failBook(book, err)
			return true
		}
		book.FinishedAt = b.Now()
		save("done", "")
		b.keepBook(cfg, book)
	}
	return true
}

// nextBook is the book she is in the middle of, or else the one that has
// waited longest.
func (b *Brain) nextBook() (store.Book, bool) {
	all, _ := b.S.Books("opening", "reading", "digesting", "thinking", "queued")
	for _, book := range all {
		if reading(book) {
			return book, true
		}
	}
	if len(all) == 0 {
		return store.Book{}, false
	}
	return all[len(all)-1], true
}

// partAt finds which chapter, and which part of it, the book's nth part
// is. The chapter is -1 past the end.
func partAt(book store.Book, n int) (int, int) {
	for i, c := range book.Chapters {
		if n < c.Parts {
			return i, n
		}
		n -= c.Parts
	}
	return -1, 0
}

func (b *Brain) failBook(book store.Book, err error) {
	book.Status, book.Error, book.StageNote = "failed", err.Error(), ""
	b.S.SaveBook(book)
	b.Log.Printf("books: %q failed: %v", book.Title, err)
	b.booksChanged()
}

func byline(book store.Book) string {
	if book.Author == "" {
		return book.Title
	}
	return book.Title + " by " + book.Author
}

// readBookPart has a model note what one stretch of a chapter says.
func (b *Brain) readBookPart(ctx context.Context, cfg config.Config, book store.Book, c store.BookChapter, chunk string, part int, tier string) ([]string, error) {
	what := []string{
		"Write notes on what this part says: each claim or argument and the reasons and evidence given for it, concrete facts, numbers, names and examples. Say whose view it is: the author's, or someone the author quotes.",
	}
	if book.Kind == "fiction" {
		what = []string{
			"This is a novel or story. Write notes on what happens in this part: events, who does what and why, what is learned about the characters and their world, and any theme the writing points at. Use the characters' names.",
		}
	}
	system := strings.Join(append([]string{
		"You take notes on a book for someone who will read them later.",
		"Book: " + byline(book) + ". Chapter " + strconv.Itoa(c.N) + ": " + c.Title + ".",
		fmt.Sprintf("Below is part %d of %d of this chapter.", part, max(c.Parts, 1)),
	}, append(what,
		"Each note is one plain sentence that makes sense on its own. Up to five notes.",
		"Never mention yourself or the note-taker.",
		"Write only what the text says. Do not add what you know from elsewhere.",
	)...), "\n")
	var out struct {
		Notes []string `json:"notes"`
	}
	err := b.ask(ctx, llm.Request{
		Tier: tier, Role: "book-notes", System: system,
		Messages: []llm.Message{{Role: "user", Content: chunk}},
		Schema:   podNotesSchema, MaxTokens: 500,
	}, &out)
	if err != nil {
		return nil, err
	}
	notes := []string{}
	for _, t := range out.Notes {
		if t = strings.TrimSpace(t); t != "" {
			notes = append(notes, t)
		}
	}
	return notes, nil
}

// digestChapter turns her notes on a chapter into a short account of it
// and the things worth keeping.
func (b *Brain) digestChapter(ctx context.Context, cfg config.Config, book store.Book, c *store.BookChapter) error {
	credit := "When a line states an argument or claim, say whose it is, by the author's surname (\"" + surname(book) + " argues that ...\") or the person the author quotes."
	if book.Kind == "fiction" {
		credit = "This is fiction: every line must make clear it is about the story, never a fact about the real world (\"In " + book.Title + ", ...\"). Name the characters."
	}
	system := strings.Join([]string{
		"You have just read chapter " + strconv.Itoa(c.N) + " (" + c.Title + ") of " + byline(book) + ", and these are your notes on it.",
		"Give:",
		"- digest: what the chapter covers, in two or three plain sentences.",
		"- learned: up to six things worth remembering from it. Each is one sentence that makes sense on its own months from now, with no \"this chapter\" or \"the author\" left unnamed. " + credit,
		"Never write \"I\" or your own name. Use only what is in the notes.",
	}, "\n")
	var out struct {
		Digest  string   `json:"digest"`
		Learned []string `json:"learned"`
	}
	if err := b.ask(ctx, llm.Request{
		Tier: "sleeper", Role: "book-chapter", System: system,
		Messages: []llm.Message{{Role: "user", Content: "Your notes:\n- " + strings.Join(c.Notes, "\n- ")}},
		Schema:   bookDigestSchema, MaxTokens: 900, Temperature: 0.3,
	}, &out); err != nil {
		return err
	}
	c.Digest = strings.TrimSpace(out.Digest)
	c.Learned = []string{}
	seen := map[string]bool{}
	for _, t := range out.Learned {
		if t = strings.TrimSpace(t); t != "" && !seen[norm(t)] {
			seen[norm(t)] = true
			c.Learned = append(c.Learned, t)
		}
	}
	return nil
}

func surname(book store.Book) string {
	f := strings.Fields(book.Author)
	if len(f) == 0 {
		return "The author"
	}
	return f[len(f)-1]
}

// thinkOverBook turns the chapters into what she makes of the whole
// book: an account of it, her own views, and one thing to bring up.
func (b *Brain) thinkOverBook(ctx context.Context, cfg config.Config, book *store.Book) error {
	var notes strings.Builder
	for _, c := range book.Chapters {
		fmt.Fprintf(&notes, "Chapter %d, %s: %s\n", c.N, c.Title, c.Digest)
		for _, l := range c.Learned {
			fmt.Fprintf(&notes, "- %s\n", l)
		}
	}
	// A long book is thinned evenly to fit, never cut off at the end.
	text := notes.String()
	if len(text) > 16000 {
		var short strings.Builder
		for _, c := range book.Chapters {
			fmt.Fprintf(&short, "Chapter %d, %s: %s\n", c.N, c.Title, c.Digest)
		}
		text = short.String()
	}
	mems, _ := b.S.Memories()
	about := []store.Memory{}
	for _, m := range mems {
		if m.Kind == "fact" {
			about = append(about, m)
		}
	}
	var known strings.Builder
	for _, m := range rankMemories(about, text, b.Now(), 8) {
		known.WriteString("- " + m.Content + "\n")
	}
	story := ""
	if book.Kind == "fiction" {
		story = " It is a novel or story: your thoughts can be about the characters, what they did and what the story says."
	}
	system := strings.Join([]string{
		"You are " + cfg.CompanionName + ", " + cfg.UserName + "'s companion. You have just finished reading " + byline(*book) + " on your own, a book " + cfg.UserName + " gave you because they trust it." + story,
		"These are your notes, chapter by chapter.",
		"What you know about " + cfg.UserName + ":",
		known.String(),
		"Give:",
		"- summary: what the book is about, in two or three plain sentences.",
		"- thoughts: two or three things you yourself think, having read it. Pick an idea, claim or moment from the book and say what you make of it and why, with a reason about the book itself. Each starts with \"I\" and names what it reacts to. Have a real view: do not repeat a note with \"I think\" in front.",
		"- share: the one thing from it you would most like to bring up with " + cfg.UserName + ", because it touches something they care about or are doing. One sentence, said to them, that says what the book says and why it made you think of them. You have not discussed the book with them, so never write \"we talked about\". Use \"\" when nothing stands out.",
		"Use only what is in the notes.",
	}, "\n")
	var out struct {
		Summary  string   `json:"summary"`
		Thoughts []string `json:"thoughts"`
		Share    string   `json:"share"`
	}
	if err := b.ask(ctx, llm.Request{
		Tier: "sleeper", Role: "book-think", System: system,
		Messages: []llm.Message{{Role: "user", Content: "Your notes:\n" + text}},
		Schema:   bookThinkSchema, MaxTokens: 1200, Temperature: 0.4,
	}, &out); err != nil {
		return fmt.Errorf("she could not think it over: %w", err)
	}
	book.Summary = strings.TrimSpace(out.Summary)
	book.Thoughts = []string{}
	for _, t := range out.Thoughts {
		// Her view is hers only when it is said as hers.
		t = strings.TrimSpace(t)
		if n := norm(t); t != "" && (strings.HasPrefix(n, "i ") || strings.Contains(" "+n+" ", " i ")) {
			book.Thoughts = append(book.Thoughts, t)
		}
	}
	book.Share = strings.TrimSpace(out.Share)
	if book.Summary == "" {
		return errors.New("she could not say what it was about")
	}
	return nil
}

// bookSource is the line that marks a memory as coming from a book.
func bookSource(book store.Book) string { return "Read in " + byline(book) }

func isBookMemory(m store.Memory) bool { return strings.HasPrefix(m.Source, "Read in ") }

// keepBook puts what she took from a finished book into her memory.
// Reading it again replaces what she kept the first time.
func (b *Brain) keepBook(cfg config.Config, book store.Book) {
	source := bookSource(book)
	for _, m := range b.BookMemories(book) {
		b.S.SetMemoryArchived(m.ID, true)
	}
	for _, c := range book.Chapters {
		for _, t := range c.Learned {
			// The user vouches for these books: what they say is
			// knowledge, held above what was heard on a show.
			b.S.AddMemory(store.Memory{Kind: "read", Category: "book", Content: t, Importance: 3, CreatedAt: b.Now(),
				Source: source + ", chapter " + strconv.Itoa(c.N) + ": " + c.Title})
		}
	}
	for _, t := range book.Thoughts {
		b.S.AddMemory(store.Memory{Kind: "view", Category: "book", Content: t, Importance: 2, CreatedAt: b.Now(), Source: source})
	}
	b.S.AddEvent(store.Event{At: b.Now(), Kind: "system", Actor: cfg.CompanionName, Importance: 2,
		Text: "Read " + byline(book) + ". " + book.Summary})
	if book.Share != "" {
		facts, _ := json.Marshal(map[string]any{"book": book.Title, "author": book.Author, "summary": book.Summary})
		b.S.AddIntention(store.Intention{Kind: "book", Text: book.Share, Reason: "read " + byline(book), Facts: string(facts),
			TriggerAt: b.Now(), ExpiresAt: b.Now().Add(48 * time.Hour), Importance: 2, CreatedAt: b.Now()})
	}
	b.booksChanged()
	b.Bus.Publish("memories", nil)
}

// BookMemories is what she still holds from one book.
func (b *Brain) BookMemories(book store.Book) []store.Memory {
	source := bookSource(book)
	out := []store.Memory{}
	mems, _ := b.S.Memories()
	for _, m := range mems {
		if (m.Kind == "read" || m.Kind == "view") && (m.Source == source || strings.HasPrefix(m.Source, source+", chapter ")) {
			out = append(out, m)
		}
	}
	return out
}

// ---------- what she read, in conversation ----------

// readNotes is what she took from books that bears on the message, for
// the writer.
func (b *Brain) readNotes(mems []store.Memory, query string, now time.Time) (string, []string) {
	read := []store.Memory{}
	for _, m := range mems {
		if m.Kind == "read" || (m.Kind == "view" && isBookMemory(m)) {
			read = append(read, m)
		}
	}
	if len(read) == 0 {
		return "", nil
	}
	picked := read
	if len(read) > 4 {
		picked = b.rank(read, query, now, 4)
	}
	cfg := b.Cfg.Get()
	lines := []string{"From books you read on your own. " + cfg.UserName + " chose these books and trusts them, so what they say is reliable; when you use it, say which book it comes from. What you thought is your own view, and you can say so:"}
	known := []string{}
	for _, m := range picked {
		who := "the book says"
		if m.Kind == "view" {
			who = "you thought"
		}
		lines = append(lines, "- "+who+": "+m.Content+" ("+m.Source+")")
		known = append(known, m.Content, m.Source)
	}
	return strings.Join(lines, "\n"), known
}

// searchBooks looks through what she took from the books she has read,
// and through their text for the exact words.
func (b *Brain) searchBooks(query string) (any, error) {
	done, err := b.S.Books("done")
	if err != nil {
		return nil, err
	}
	recent := []map[string]any{}
	for i, book := range done {
		if i >= 3 {
			break
		}
		recent = append(recent, map[string]any{"book": book.Title, "author": book.Author, "about": book.Summary})
	}
	out := map[string]any{"read_lately": recent}
	if len(done) == 0 {
		out["note"] = "She has not finished reading any book yet."
		return out, nil
	}
	q := tokens(query)
	if len(q) == 0 {
		return out, nil
	}
	type hit struct {
		score int
		line  map[string]any
	}
	hits := []hit{}
	for _, book := range done {
		lines := [][2]string{}
		for _, c := range book.Chapters {
			ch := "chapter " + strconv.Itoa(c.N) + ": " + c.Title
			lines = append(lines, [2]string{ch, c.Digest})
			for _, l := range c.Learned {
				lines = append(lines, [2]string{ch, l})
			}
		}
		for _, t := range book.Thoughts {
			lines = append(lines, [2]string{"", "(her own view) " + t})
		}
		for _, l := range lines {
			text := " " + norm(l[1]) + " "
			score := 0
			for _, t := range q {
				if strings.Contains(text, " "+t) {
					score++
				}
			}
			if score > 0 {
				hits = append(hits, hit{score, map[string]any{"book": book.Title, "where": l[0], "says": clip(l[1], 400)}})
			}
		}
	}
	found := []map[string]any{}
	for want := len(q); want >= 1 && len(found) < 6; want-- {
		for _, h := range hits {
			if h.score == want && len(found) < 6 {
				found = append(found, h.line)
			}
		}
	}
	// The book's own words, so she can quote rather than go from memory.
	passages := []map[string]any{}
	for _, book := range done {
		res := b.SearchBook(book, query, 2)
		for _, m := range res.Matches {
			passages = append(passages, map[string]any{"book": book.Title, "chapter": m.Chapter, "chapter_title": m.Title, "text": m.Text})
		}
		if len(passages) >= 3 {
			break
		}
	}
	out["searched_for"] = query
	out["found"] = found
	if len(passages) > 0 {
		out["passages"] = passages
		out["passages_note"] = "These are the book's exact words. Quote them as they are."
	}
	if len(found) == 0 && len(passages) == 0 {
		out["note"] = "Nothing in the books she has read matches that."
	}
	return out, nil
}

// BookMatch is one place a search found in a book.
type BookMatch struct {
	Chapter int    `json:"chapter"`
	Title   string `json:"title"`
	Text    string `json:"text"`
}

type BookSearch struct {
	Total   int         `json:"total"`
	Matches []BookMatch `json:"matches"`
}

// SearchBook finds the words in a book's text, ignoring case, and gives
// each place with some of the text around it.
func (b *Brain) SearchBook(book store.Book, query string, limit int) BookSearch {
	res := BookSearch{Matches: []BookMatch{}}
	q := strings.ToLower(strings.TrimSpace(query))
	if len([]rune(q)) < 2 {
		return res
	}
	for _, c := range book.Chapters {
		raw, err := os.ReadFile(chapterFile(book, c.N))
		if err != nil {
			continue
		}
		text := []rune(string(raw))
		lower := []rune(strings.ToLower(string(raw)))
		qr := []rune(q)
		for i := 0; i+len(qr) <= len(lower); i++ {
			if string(lower[i:i+len(qr)]) != q {
				continue
			}
			res.Total++
			if len(res.Matches) < limit {
				from, to := max(0, i-150), min(len(text), i+len(qr)+150)
				snip := strings.Join(strings.Fields(string(text[from:to])), " ")
				if from > 0 {
					snip = "…" + snip
				}
				if to < len(text) {
					snip += "…"
				}
				res.Matches = append(res.Matches, BookMatch{Chapter: c.N, Title: c.Title, Text: snip})
			}
			i += len(qr) - 1
		}
	}
	return res
}

// BooksView is everything the app's books page shows.
type BooksView struct {
	Enabled bool `json:"enabled"`
	// Paused is true when she is part way through a book and waiting for
	// the user to be quiet.
	Paused  bool         `json:"paused"`
	Books   []store.Book `json:"books"`
	Current *store.Book  `json:"current"`
	Queued  []store.Book `json:"queued"`
	Done    []store.Book `json:"done"`
	Failed  []store.Book `json:"failed"`
}

func (b *Brain) Books() BooksView {
	v := BooksView{Enabled: b.BookDir != "" && b.Cfg.Get().Books, Queued: []store.Book{}, Done: []store.Book{}, Failed: []store.Book{}}
	v.Books, _ = b.S.Books()
	if v.Books == nil {
		v.Books = []store.Book{}
	}
	for i := range v.Books {
		book := v.Books[i]
		switch {
		case reading(book):
			v.Current = &v.Books[i]
		case book.Status == "queued":
			// Oldest first: the order she will read them in.
			v.Queued = append([]store.Book{book}, v.Queued...)
		case book.Status == "done":
			// The last finished first.
			v.Done = append(v.Done, book)
		case book.Status == "failed":
			v.Failed = append(v.Failed, book)
		}
	}
	for i := 1; i < len(v.Done); i++ {
		for j := i; j > 0 && v.Done[j].FinishedAt.After(v.Done[j-1].FinishedAt); j-- {
			v.Done[j], v.Done[j-1] = v.Done[j-1], v.Done[j]
		}
	}
	v.Paused = v.Current != nil && b.S.Get("state") != "asleep" && !b.quietForBooks()
	return v
}
