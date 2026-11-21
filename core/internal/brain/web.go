package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

// WebResult is one search result: enough to answer from, or to decide
// which page is worth reading.
type WebResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebPage is the readable text of one page, or the newest entries of a
// feed.
type WebPage struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
	// Feeds are the RSS or Atom feeds the page offers. A page names its
	// feed in its head or behind a small "RSS" link, and neither is prose.
	Feeds []string `json:"feeds,omitempty"`
	// IsFeed is true when the address itself is a working feed.
	IsFeed bool `json:"is_feed,omitempty"`
}

// PodcastShow is one show from a podcast directory.
type PodcastShow struct {
	Name     string `json:"name"`
	By       string `json:"by"`
	Feed     string `json:"feed"`
	Episodes int    `json:"episodes"`
	Latest   string `json:"latest"`
	Genre    string `json:"genre"`
}

// WebSource is how she looks things up online. It is the only part of
// her that sends anything the user said to another computer, so it sits
// behind a setting and can be swapped out in tests.
type WebSource interface {
	Search(ctx context.Context, query string) ([]WebResult, error)
	Read(ctx context.Context, address string) (WebPage, error)
	// Podcasts looks a show up by name and gives its feed address.
	Podcasts(ctx context.Context, name string) ([]PodcastShow, error)
}

const (
	webResults  = 8
	webSnippet  = 400
	webPageText = 7000
	webFeeds    = 5
	webEntries  = 10
	webShows    = 5
	webMaxBytes = 2 << 20
	webAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
)

// DuckDuckGo searches through DuckDuckGo's plain HTML page, which needs
// no key and no account.
type DuckDuckGo struct {
	Client *http.Client
	// Endpoint is the search page; empty means DuckDuckGo's own.
	Endpoint string
	// Directory is the podcast search; empty means Apple's, which needs
	// no key and lists each show's own feed.
	Directory string
	// AllowLocal lets Read fetch from this machine. Tests use it.
	AllowLocal bool
}

func (d *DuckDuckGo) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	if !d.AllowLocal {
		// A page address comes from a model or from a stranger's search
		// result. It must never be a way to reach this machine or the
		// home network, whatever the name resolves to.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return errors.New("that address is not on the public internet")
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:   12 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 6 * time.Second},
	}
}

// fetch downloads one address. It gives back the body, its type and
// where it ended up after any redirects, which links on it are relative to.
func (d *DuckDuckGo) fetch(ctx context.Context, address, accept string) ([]byte, string, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", nil, err
	}
	req.Header.Set("User-Agent", webAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en")
	res, err := d.client().Do(req)
	if err != nil {
		return nil, "", nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", nil, fmt.Errorf("the page answered %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, webMaxBytes))
	if err != nil {
		return nil, "", nil, err
	}
	return body, res.Header.Get("Content-Type"), res.Request.URL, nil
}

func (d *DuckDuckGo) get(ctx context.Context, address string) (*html.Node, error) {
	body, kind, _, err := d.fetch(ctx, address, "text/html")
	if err != nil {
		return nil, err
	}
	if kind != "" && !strings.Contains(kind, "html") && !strings.Contains(kind, "text/plain") {
		return nil, fmt.Errorf("that is not a page of text (%s)", kind)
	}
	return html.Parse(bytes.NewReader(body))
}

func (d *DuckDuckGo) Search(ctx context.Context, query string) ([]WebResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("nothing to search for")
	}
	endpoint := d.Endpoint
	if endpoint == "" {
		endpoint = "https://html.duckduckgo.com/html/"
	}
	doc, err := d.get(ctx, endpoint+"?"+url.Values{"q": {query}}.Encode())
	if err != nil {
		return nil, fmt.Errorf("the search did not work: %w", err)
	}
	return parseResults(doc), nil
}

func (d *DuckDuckGo) Read(ctx context.Context, address string) (WebPage, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return WebPage{}, fmt.Errorf("%q is not a web address", address)
	}
	body, kind, base, err := d.fetch(ctx, u.String(), "text/html, application/rss+xml, application/atom+xml, application/xml;q=0.9, text/plain;q=0.8")
	if err != nil {
		return WebPage{}, fmt.Errorf("the page could not be read: %w", err)
	}
	// A feed is read too, so she can see that an address she was given
	// really is one before passing it on.
	if looksLikeFeed(kind, body) {
		title, text, err := readFeed(body)
		if err != nil {
			return WebPage{}, err
		}
		return WebPage{Title: title, URL: u.String(), Text: text, IsFeed: true}, nil
	}
	if kind != "" && !strings.Contains(kind, "html") && !strings.Contains(kind, "text/plain") {
		return WebPage{}, fmt.Errorf("the page could not be read: that is not a page of text (%s)", kind)
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return WebPage{}, fmt.Errorf("the page could not be read: %w", err)
	}
	title, text := readable(doc, base)
	feeds := feedLinks(doc, base)
	if text == "" && len(feeds) == 0 {
		return WebPage{}, errors.New("the page had no text that could be read")
	}
	return WebPage{Title: title, URL: u.String(), Text: text, Feeds: feeds}, nil
}

// Podcasts asks Apple's podcast directory for shows by name.
func (d *DuckDuckGo) Podcasts(ctx context.Context, name string) ([]PodcastShow, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("no show to look for")
	}
	endpoint := d.Directory
	if endpoint == "" {
		endpoint = "https://itunes.apple.com/search"
	}
	q := url.Values{"media": {"podcast"}, "entity": {"podcast"}, "limit": {fmt.Sprint(webShows)}, "term": {name}}
	body, _, _, err := d.fetch(ctx, endpoint+"?"+q.Encode(), "application/json")
	if err != nil {
		return nil, fmt.Errorf("the podcast directory did not answer: %w", err)
	}
	var found struct {
		Results []struct {
			CollectionName   string `json:"collectionName"`
			ArtistName       string `json:"artistName"`
			FeedURL          string `json:"feedUrl"`
			TrackCount       int    `json:"trackCount"`
			ReleaseDate      string `json:"releaseDate"`
			PrimaryGenreName string `json:"primaryGenreName"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &found); err != nil {
		return nil, fmt.Errorf("the podcast directory answered with something unreadable: %w", err)
	}
	shows := []PodcastShow{}
	for _, r := range found.Results {
		// A show without a public feed cannot be followed from here.
		if r.FeedURL == "" {
			continue
		}
		s := PodcastShow{Name: r.CollectionName, By: r.ArtistName, Feed: r.FeedURL, Episodes: r.TrackCount, Genre: r.PrimaryGenreName}
		if t, err := time.Parse(time.RFC3339, r.ReleaseDate); err == nil {
			s.Latest = t.Format("2006-01-02")
		}
		shows = append(shows, s)
	}
	return shows, nil
}

// ---------- reading a feed ----------

// looksLikeFeed reports whether a download is RSS or Atom. Many servers
// send feeds as text/xml, or even text/html, so the start of the body
// decides as much as the type does.
func looksLikeFeed(kind string, body []byte) bool {
	if strings.Contains(kind, "rss") || strings.Contains(kind, "atom") {
		return true
	}
	head := strings.ToLower(string(body[:min(len(body), 1024)]))
	head = strings.TrimSpace(strings.TrimPrefix(head, "\ufeff"))
	if strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html") {
		return false
	}
	return strings.Contains(head, "<rss") || strings.Contains(head, "<feed") || strings.Contains(head, "<rdf:rdf")
}

// readFeed turns RSS or Atom into a title and a short list of its newest
// entries. It reads entry by entry and stops once it has enough: a big
// podcast feed is larger than a download is allowed to be, and the end
// that gets cut off holds only the oldest episodes.
func readFeed(body []byte) (title, text string, err error) {
	type entry struct{ when, title, link string }
	var item struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		PubDate string `xml:"pubDate"`
	}
	var atom struct {
		Title     string `xml:"title"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
		Links     []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	}
	entries := []entry{}
	root := ""
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	for len(entries) < webEntries {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if root == "" {
			root = strings.ToLower(start.Name.Local)
			if root != "rss" && root != "feed" && root != "rdf" {
				return "", "", errors.New("that does not look like a feed")
			}
			continue
		}
		switch start.Name.Local {
		case "title":
			// Titles inside an entry are read with the entry, so this is
			// the feed's own. A channel's image has a title too, later.
			var t string
			if dec.DecodeElement(&t, &start) == nil && title == "" {
				title = strings.TrimSpace(t)
			}
		case "item":
			item.Title, item.Link, item.PubDate = "", "", ""
			if dec.DecodeElement(&item, &start) != nil {
				continue
			}
			when := ""
			if t, err := mail.ParseDate(strings.TrimSpace(item.PubDate)); err == nil {
				when = t.Format("2006-01-02")
			}
			entries = append(entries, entry{when, item.Title, item.Link})
		case "entry":
			atom.Title, atom.Published, atom.Updated, atom.Links = "", "", "", nil
			if dec.DecodeElement(&atom, &start) != nil {
				continue
			}
			link := ""
			for _, l := range atom.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			when := atom.Published
			if when == "" {
				when = atom.Updated
			}
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(when)); err == nil {
				when = t.Format("2006-01-02")
			} else {
				when = ""
			}
			entries = append(entries, entry{when, atom.Title, link})
		}
	}
	if root == "" || (title == "" && len(entries) == 0) {
		return "", "", errors.New("that does not look like a feed")
	}
	lines := []string{"This address is a working feed."}
	if len(entries) > 0 {
		lines = append(lines, "Newest entries:")
	}
	for _, e := range entries {
		line := "- "
		if e.when != "" {
			line += e.when + ": "
		}
		line += strings.Join(strings.Fields(e.title), " ")
		if l := strings.TrimSpace(e.link); l != "" {
			line += " (" + l + ")"
		}
		lines = append(lines, line)
	}
	return title, strings.Join(lines, "\n"), nil
}

// ---------- reading HTML ----------

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// walk visits every node under n. visit returns false to skip a node's
// children.
func walk(n *html.Node, visit func(*html.Node) bool) {
	if !visit(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

// textOf is the text inside a node, with runs of whitespace made single
// spaces.
func textOf(n *html.Node) string {
	var sb strings.Builder
	walk(n, func(c *html.Node) bool {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
			sb.WriteString(" ")
		}
		return true
	})
	return strings.Join(strings.Fields(sb.String()), " ")
}

// parseResults reads DuckDuckGo's result page: each result is a block
// holding a title link and a snippet.
func parseResults(doc *html.Node) []WebResult {
	out := []WebResult{}
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || !hasClass(n, "result") {
			return true
		}
		// Sponsored results are not answers.
		if hasClass(n, "result--ad") {
			return false
		}
		r := WebResult{}
		walk(n, func(c *html.Node) bool {
			if c.Type != html.ElementNode || c.Data != "a" {
				return true
			}
			switch {
			case hasClass(c, "result__a"):
				r.Title = textOf(c)
				r.URL = resultAddress(attr(c, "href"))
			case hasClass(c, "result__snippet"):
				r.Snippet = clip(textOf(c), webSnippet)
			}
			return true
		})
		if r.Title != "" && r.URL != "" && len(out) < webResults {
			out = append(out, r)
		}
		return false
	})
	return out
}

// resultAddress turns a result link into the page's own address.
// DuckDuckGo sometimes wraps it in a redirect that carries the real one
// in its uddg parameter.
func resultAddress(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if strings.HasSuffix(u.Host, "duckduckgo.com") {
		target := u.Query().Get("uddg")
		if target == "" {
			return "" // an advert or one of DuckDuckGo's own pages
		}
		return target
	}
	return u.String()
}

// linkTo resolves a link on a page into a full web address, or "" when
// it leads nowhere worth naming: a script, an email, a spot on the same page.
func linkTo(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

// blockText is the text inside a node with each link's address written
// after its words, "RSS feed (https://...)". Without the addresses she
// can say a page has a link but never what it is. plain is the same text
// without them.
func blockText(n *html.Node, base *url.URL) (text, plain string, links int) {
	var sb, pb strings.Builder
	walk(n, func(c *html.Node) bool {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data + " ")
			pb.WriteString(c.Data + " ")
			return true
		}
		if c.Type != html.ElementNode || c.Data != "a" {
			return true
		}
		words := textOf(c)
		target := linkTo(base, attr(c, "href"))
		sb.WriteString(words + " ")
		pb.WriteString(words + " ")
		if target != "" && words != "" && words != target {
			sb.WriteString("(" + target + ") ")
			links++
		}
		return false
	})
	return strings.Join(strings.Fields(sb.String()), " "), strings.Join(strings.Fields(pb.String()), " "), links
}

// readable pulls the title and the body text out of a page, leaving out
// menus, scripts and the like.
func readable(doc *html.Node, base *url.URL) (title, text string) {
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "nav": true, "header": true, "footer": true,
		"aside": true, "form": true, "svg": true, "iframe": true, "button": true, "select": true, "template": true}
	blocks := map[string]bool{"p": true, "li": true, "h1": true, "h2": true, "h3": true, "h4": true, "td": true, "blockquote": true, "pre": true, "dd": true}
	parts := []string{}
	size := 0
	// An article or main element, when there is one, is the page's
	// content; everything outside it is furniture.
	root := doc
	walk(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "title" && title == "" {
			title = textOf(n)
		}
		if n.Type == html.ElementNode && (n.Data == "article" || n.Data == "main") && root == doc {
			root = n
		}
		return true
	})
	walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		if skip[n.Data] || size >= webPageText {
			return false
		}
		if !blocks[n.Data] {
			return true
		}
		// Short scraps are labels, not prose, unless they are a link:
		// "Subscribe via RSS" is short and exactly what was looked for.
		t, plain, links := blockText(n, base)
		if len(plain) >= 40 || links > 0 || (n.Data[0] == 'h' && len(plain) > 0) {
			parts = append(parts, t)
			size += len(t)
		}
		return false
	})
	return title, clip(strings.Join(parts, "\n"), webPageText)
}

// feedLinks finds the feeds a page offers: the ones its head announces,
// and links anywhere on it, footer included, that look like a feed.
func feedLinks(doc *html.Node, base *url.URL) []string {
	out := []string{}
	add := func(href string) {
		target := linkTo(base, href)
		if target == "" || len(out) >= webFeeds {
			return
		}
		// "Subscribe in this app" links carry the real feed inside them,
		// and the real one is listed on its own.
		if u, err := url.Parse(target); err == nil && strings.Contains(strings.ToLower(u.RawQuery+u.Path), "http") {
			return
		}
		for _, f := range out {
			if f == target {
				return
			}
		}
		out = append(out, target)
	}
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		kind := strings.ToLower(attr(n, "type"))
		switch {
		case n.Data == "link" && strings.Contains(strings.ToLower(attr(n, "rel")), "alternate") && (strings.Contains(kind, "rss") || strings.Contains(kind, "atom")):
			add(attr(n, "href"))
		case n.Data == "a":
			href := strings.ToLower(attr(n, "href"))
			words := strings.ToLower(textOf(n))
			if strings.Contains(kind, "rss") || strings.Contains(kind, "atom") || strings.HasPrefix(href, "feed:") ||
				strings.Contains(href, "/rss") || strings.Contains(href, "rss.") || strings.HasSuffix(href, "/feed") || strings.HasSuffix(href, "/feed/") ||
				strings.HasSuffix(href, ".rss") || strings.Contains(href, "feeds.") || strings.Contains(href, "/feeds/") || words == "rss" || strings.Contains(words, "rss feed") {
				add(attr(n, "href"))
			}
		}
		return true
	})
	return out
}

// siteOf is the short name of the site an address is on: "bbc.co.uk".
func siteOf(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}
