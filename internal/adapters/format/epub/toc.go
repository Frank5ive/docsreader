package epub

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/frank5ive/docsreader/internal/domain"
)

// buildTOC reads the EPUB3 nav document, falling back to the NCX file.
func (d *doc) buildTOC(ncxID string, ids map[string]string) {
	spineIdx := map[string]int{}
	for i, p := range d.spine {
		spineIdx[p] = i
	}
	for p, it := range d.manifest {
		if strings.Contains(it.props, "nav") {
			if data, err := d.read(p); err == nil {
				d.toc = parseNav(data, p, spineIdx)
			}
		}
	}
	if len(d.toc) == 0 && ncxID != "" {
		if p, ok := ids[ncxID]; ok {
			if data, err := d.read(p); err == nil {
				d.toc = parseNCX(data, p, spineIdx)
			}
		}
	}
	if len(d.toc) == 0 {
		for i := range d.spine {
			d.toc = append(d.toc, domain.Chapter{Title: "Section " + itoa(i+1), Section: i})
		}
	}
}

func itoa(i int) string {
	var b [20]byte
	n := len(b)
	for {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
		if i == 0 {
			return string(b[n:])
		}
	}
}

func parseNav(data []byte, navPath string, spineIdx map[string]int) []domain.Chapter {
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	base := dirOf(navPath)
	var out []domain.Chapter
	var walk func(n *html.Node, depth int, inToc bool)
	walk = func(n *html.Node, depth int, inToc bool) {
		if n.Type == html.ElementNode {
			if n.DataAtom == atom.Nav {
				inToc = false
				for _, a := range n.Attr {
					if strings.HasSuffix(a.Key, "type") && a.Val == "toc" {
						inToc = true
					}
				}
			}
			if inToc && n.DataAtom == atom.Ol && n.Parent != nil && n.Parent.DataAtom != atom.Nav {
				depth++
			}
			if inToc && n.DataAtom == atom.A {
				href := ""
				for _, a := range n.Attr {
					if a.Key == "href" {
						href = a.Val
					}
				}
				if i, ok := spineIdx[resolve(base, href)]; ok {
					out = append(out, domain.Chapter{Title: strings.TrimSpace(text(n)), Section: i, Depth: depth})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth, inToc)
		}
	}
	walk(root, 0, false)
	return out
}

func parseNCX(data []byte, ncxPath string, spineIdx map[string]int) []domain.Chapter {
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	base := dirOf(ncxPath)
	var out []domain.Chapter
	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		if n.Type == html.ElementNode && n.Data == "navpoint" {
			var title, src string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				switch c.Data {
				case "navlabel":
					title = strings.TrimSpace(text(c))
				case "content":
					for _, a := range c.Attr {
						if a.Key == "src" {
							src = a.Val
						}
					}
				}
			}
			if i, ok := spineIdx[resolve(base, src)]; ok {
				out = append(out, domain.Chapter{Title: title, Section: i, Depth: depth})
			}
			depth++
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth)
		}
	}
	walk(root, 0)
	return out
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}

func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(text(c))
	}
	return b.String()
}
