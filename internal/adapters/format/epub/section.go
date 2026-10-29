package epub

import (
	"bytes"
	"path"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/frank5ive/docsreader/internal/domain"
)

// ResPrefix is the URL prefix the web layer maps to Document.Resource.
const ResPrefix = "res/"

var blockTags = map[atom.Atom]bool{
	atom.P: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Li: true, atom.Blockquote: true, atom.Pre: true, atom.Figcaption: true,
	atom.Dt: true, atom.Dd: true, atom.Td: true,
}

var dropTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Iframe: true, atom.Object: true, atom.Embed: true,
	atom.Form: true, atom.Link: true, atom.Meta: true, atom.Title: true, atom.Head: true,
}

// Section returns sanitised body HTML. Every block element gets a data-b
// attribute (its index within the section) so positions can be anchored
// independently of layout. Resource URLs are rewritten to ResPrefix paths.
func (d *doc) Section(i int) (string, error) {
	if i < 0 || i >= len(d.spine) {
		return "", domainErr("section out of range")
	}
	name := d.spine[i]
	raw, err := d.read(name)
	if err != nil {
		return "", err
	}
	root, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	body := findBody(root)
	if body == nil {
		return "", nil
	}
	block := 0
	sanitise(body, path.Dir(name), &block)
	var out bytes.Buffer
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&out, c); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}

type domainErr string

func (e domainErr) Error() string { return string(e) }

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

func sanitise(n *html.Node, base string, block *int) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			if dropTags[c.DataAtom] {
				n.RemoveChild(c)
				c = next
				continue
			}
			cleanAttrs(c, base)
			if blockTags[c.DataAtom] {
				c.Attr = append(c.Attr, html.Attribute{Key: "data-b", Val: strconv.Itoa(*block)})
				*block++
			}
		} else if c.Type == html.CommentNode {
			n.RemoveChild(c)
			c = next
			continue
		}
		sanitise(c, base, block)
		c = next
	}
}

func cleanAttrs(n *html.Node, base string) {
	keep := n.Attr[:0]
	for _, a := range n.Attr {
		k := strings.ToLower(a.Key)
		switch {
		case strings.HasPrefix(k, "on"), k == "style", k == "srcset":
			continue
		case k == "src" || k == "xlink:href" || (k == "href" && n.DataAtom == atom.Image):
			if isExternal(a.Val) {
				continue
			}
			a.Val = ResPrefix + resolveRel(base, a.Val)
		case k == "href":
			if !isExternal(a.Val) && !strings.HasPrefix(a.Val, "#") {
				a.Val = "#" + a.Val // internal cross-links handled client-side
			} else if strings.HasPrefix(strings.ToLower(a.Val), "javascript:") {
				continue
			}
		}
		keep = append(keep, a)
	}
	n.Attr = keep
}

func isExternal(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return strings.Contains(v, "://") || strings.HasPrefix(v, "data:") || strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "//")
}

func resolveRel(base, href string) string { return resolve(base, href) }

// Compile-time check that Chapter is used by the toc builder.
var _ = domain.Chapter{}

// Blocks returns the text of each data-b block in section i, in block order.
func (d *doc) Blocks(i int) ([]string, error) {
	h, err := d.Section(i)
	if err != nil {
		return nil, err
	}
	root, err := html.Parse(strings.NewReader(h))
	if err != nil {
		return nil, err
	}
	var out []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "data-b" {
					if idx, err := strconv.Atoi(a.Val); err == nil {
						for len(out) <= idx {
							out = append(out, "")
						}
						out[idx] = strings.Join(strings.Fields(text(n)), " ")
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out, nil
}
