package brain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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

// WebPage is the readable text of one page.
type WebPage struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

// WebSource is how she looks things up online. It is the only part of
// her that sends anything the user said to another computer, so it sits
// behind a setting and can be swapped out in tests.
type WebSource interface {
	Search(ctx context.Context, query string) ([]WebResult, error)
	Read(ctx context.Context, address string) (WebPage, error)
}

const (
	webResults  = 5
	webPageText = 2400
	webMaxBytes = 2 << 20
	webAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
)

// DuckDuckGo searches through DuckDuckGo's plain HTML page, which needs
// no key and no account.
type DuckDuckGo struct {
	Client *http.Client
	// Endpoint is the search page; empty means DuckDuckGo's own.
	Endpoint string
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

func (d *DuckDuckGo) get(ctx context.Context, address string) (*html.Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", webAgent)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en")
	res, err := d.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the page answered %s", res.Status)
	}
	if kind := res.Header.Get("Content-Type"); kind != "" && !strings.Contains(kind, "html") && !strings.Contains(kind, "text/plain") {
		return nil, fmt.Errorf("that is not a page of text (%s)", kind)
	}
	return html.Parse(io.LimitReader(res.Body, webMaxBytes))
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
	doc, err := d.get(ctx, u.String())
	if err != nil {
		return WebPage{}, fmt.Errorf("the page could not be read: %w", err)
	}
	title, text := readable(doc)
	if text == "" {
		return WebPage{}, errors.New("the page had no text that could be read")
	}
	return WebPage{Title: title, URL: u.String(), Text: text}, nil
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
				r.Snippet = clip(textOf(c), 320)
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

// readable pulls the title and the body text out of a page, leaving out
// menus, scripts and the like.
func readable(doc *html.Node) (title, text string) {
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
		// Short scraps are labels and links, not prose.
		if t := textOf(n); len(t) >= 40 || (n.Data[0] == 'h' && len(t) > 0) {
			parts = append(parts, t)
			size += len(t)
		}
		return false
	})
	return title, clip(strings.Join(parts, "\n"), webPageText)
}

// siteOf is the short name of the site an address is on: "bbc.co.uk".
func siteOf(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}
