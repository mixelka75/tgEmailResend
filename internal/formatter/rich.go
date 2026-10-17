package formatter

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/mixelka/emailresend/pkg/models"
)

// Telegram rich message limits are 32768 characters and 500 blocks.
// Stay well below them: the header and closing note need room too.
const (
	richMaxBodyRunes = 24000
	richMaxBlocks    = 350
	// Bodies longer than this are partly hidden under "Читать дальше"
	richPreviewRunes  = 700
	richCollapseRunes = 3000
)

// text style flags of a run
const (
	styleBold = 1 << iota
	styleItalic
	styleUnderline
	styleStrike
	styleCode
	styleMark
	styleSup
	styleSub
)

// run is a piece of text with one set of styles
type run struct {
	text  string // raw text, not escaped
	style int
	href  string
	br    bool // line break instead of text
}

type blockKind int

const (
	blockParagraph blockKind = iota
	blockHeading
	blockListItem
	blockQuote
	blockPre
	blockRule
)

type block struct {
	kind    blockKind
	level   int // heading level
	list    int // list identity for grouping list items
	ordered bool
	runs    []run
}

func (b *block) textLen() int {
	n := 0
	for _, r := range b.runs {
		n += utf8.RuneCountInString(r.text)
	}
	return n
}

func (b *block) empty() bool {
	if b.kind == blockRule {
		return false
	}
	for _, r := range b.runs {
		if strings.TrimSpace(r.text) != "" {
			return false
		}
	}
	return true
}

var (
	spaceRegex     = regexp.MustCompile(`[\s\x{00A0}]+`)
	invisibleRegex = regexp.MustCompile(`[\x{200B}-\x{200D}\x{FEFF}\x{00AD}\x{034F}\x{061C}\x{115F}\x{1160}\x{17B4}\x{17B5}\x{180E}\x{2060}-\x{2064}\x{206A}-\x{206F}\x{FE00}-\x{FE0F}\x{FFF0}-\x{FFF8}]+`)
	boldWeight     = regexp.MustCompile(`font-weight\s*:\s*(bold|[6-9]00)`)
)

// RichEmail formats an email as HTML for sendRichMessage
func (f *TelegramFormatter) RichEmail(msg *models.EmailMessage, codes []models.DetectedCode) string {
	var sb strings.Builder

	subject := strings.TrimSpace(msg.Subject)
	if subject == "" {
		subject = "(без темы)"
	}
	subject, _ = f.truncate(subject, 300)
	sb.WriteString("<h3>" + esc(subject) + "</h3>")

	sb.WriteString("<p><b>От:</b> " + f.formatFrom(msg))
	sb.WriteString("<br><b>Дата:</b> " + formatTime(msg))
	sb.WriteString("</p>")

	if len(codes) > 0 {
		label := "Код:"
		if len(codes) > 1 {
			label = "Коды:"
		}
		sb.WriteString("<p><b>" + label + "</b>")
		for _, code := range codes {
			sb.WriteString(" <code>" + esc(code.Value) + "</code>")
		}
		sb.WriteString("</p>")
	}

	sb.WriteString("<hr/>")

	var blocks []*block
	if strings.TrimSpace(msg.BodyHTML) != "" {
		blocks = htmlToBlocks(msg.BodyHTML)
	}
	if len(blocks) == 0 {
		blocks = textToBlocks(msg.BodyText)
	}
	if len(blocks) == 0 {
		sb.WriteString("<p><i>(пустое письмо)</i></p>")
		return sb.String()
	}

	blocks, truncated := limitBlocks(blocks)

	total := 0
	for _, b := range blocks {
		total += b.textLen()
	}

	if total <= richCollapseRunes {
		sb.WriteString(renderBlocks(blocks))
	} else {
		// Show the beginning, hide the rest under a collapsible block
		split, seen := len(blocks), 0
		for i, b := range blocks {
			seen += b.textLen()
			if seen >= richPreviewRunes {
				split = i + 1
				break
			}
		}
		sb.WriteString(renderBlocks(blocks[:split]))
		if split < len(blocks) {
			sb.WriteString("<details><summary>Читать дальше</summary>")
			sb.WriteString(renderBlocks(blocks[split:]))
			sb.WriteString("</details>")
		}
	}

	if truncated {
		sb.WriteString("<p><i>… письмо слишком длинное и обрезано</i></p>")
	}

	return sb.String()
}

func (f *TelegramFormatter) formatFrom(msg *models.EmailMessage) string {
	addr := esc(msg.FromAddr)
	if msg.FromAddr != "" {
		addr = `<a href="mailto:` + esc(msg.FromAddr) + `">` + esc(msg.FromAddr) + `</a>`
	}
	name := strings.TrimSpace(msg.FromName)
	switch {
	case name != "" && msg.FromAddr != "":
		name, _ = f.truncate(name, 100)
		return esc(name) + " &lt;" + addr + "&gt;"
	case name != "":
		return esc(name)
	case msg.FromAddr != "":
		return addr
	default:
		return "(неизвестно)"
	}
}

// formatTime shows the date in each reader's own time zone; the text inside
// is the fallback for clients that do not support date_time entities
func formatTime(msg *models.EmailMessage) string {
	if msg.ReceivedAt.IsZero() {
		return "—"
	}
	return fmt.Sprintf(`<tg-time unix="%d" format="Dt">%s</tg-time>`,
		msg.ReceivedAt.Unix(), esc(msg.ReceivedAt.Format("02.01.2006 15:04 MST")))
}

// limitBlocks keeps the body within Telegram limits
func limitBlocks(blocks []*block) ([]*block, bool) {
	runes := 0
	for i, b := range blocks {
		if i >= richMaxBlocks {
			return blocks[:i], true
		}
		n := b.textLen()
		if runes+n > richMaxBodyRunes {
			// Cut this block's runs to fit
			left := richMaxBodyRunes - runes
			var kept []run
			for _, r := range b.runs {
				rn := utf8.RuneCountInString(r.text)
				if rn > left {
					r.text = string([]rune(r.text)[:max(left, 0)])
					kept = append(kept, r)
					break
				}
				kept = append(kept, r)
				left -= rn
			}
			b.runs = kept
			return blocks[:i+1], true
		}
		runes += n
	}
	return blocks, false
}

// renderBlocks renders blocks with only the tags Telegram rich HTML supports
func renderBlocks(blocks []*block) string {
	var sb strings.Builder
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		switch b.kind {
		case blockRule:
			sb.WriteString("<hr/>")
		case blockHeading:
			tag := fmt.Sprintf("h%d", b.level)
			sb.WriteString("<" + tag + ">" + renderRuns(b.runs) + "</" + tag + ">")
		case blockPre:
			var text strings.Builder
			for _, r := range b.runs {
				if r.br {
					text.WriteString("\n")
				} else {
					text.WriteString(r.text)
				}
			}
			sb.WriteString("<pre>" + esc(strings.Trim(text.String(), "\n")) + "</pre>")
		case blockQuote:
			// Merge consecutive quote paragraphs into one collapsible quote
			sb.WriteString("<blockquote expandable>")
			for j := i; j < len(blocks) && blocks[j].kind == blockQuote; j++ {
				if j > i {
					sb.WriteString("<br>")
				}
				sb.WriteString(renderRuns(blocks[j].runs))
				i = j
			}
			sb.WriteString("</blockquote>")
		case blockListItem:
			tag := "ul"
			if b.ordered {
				tag = "ol"
			}
			sb.WriteString("<" + tag + ">")
			for j := i; j < len(blocks) && blocks[j].kind == blockListItem && blocks[j].list == b.list; j++ {
				sb.WriteString("<li>" + renderRuns(blocks[j].runs) + "</li>")
				i = j
			}
			sb.WriteString("</" + tag + ">")
		default:
			sb.WriteString("<p>" + renderRuns(b.runs) + "</p>")
		}
	}
	return sb.String()
}

var styleTags = []struct {
	flag int
	tag  string
}{
	{styleBold, "b"},
	{styleItalic, "i"},
	{styleUnderline, "u"},
	{styleStrike, "s"},
	{styleMark, "mark"},
	{styleSup, "sup"},
	{styleSub, "sub"},
	{styleCode, "code"},
}

// renderRuns renders runs; merges neighbours with equal styles
func renderRuns(runs []run) string {
	// Merge adjacent runs with the same formatting
	var merged []run
	for _, r := range runs {
		if n := len(merged); n > 0 && !r.br && !merged[n-1].br &&
			merged[n-1].style == r.style && merged[n-1].href == r.href {
			merged[n-1].text += r.text
			continue
		}
		merged = append(merged, r)
	}

	var sb strings.Builder
	for _, r := range merged {
		if r.br {
			sb.WriteString("<br>")
			continue
		}
		if r.text == "" {
			continue
		}
		text := esc(r.text)
		if strings.TrimSpace(r.text) == "" {
			sb.WriteString(text)
			continue
		}
		// Keep surrounding spaces outside of the tags
		lead := r.text[:len(r.text)-len(strings.TrimLeft(r.text, " "))]
		trail := r.text[len(strings.TrimRight(r.text, " ")):]
		text = esc(strings.TrimSpace(r.text))

		style := r.style
		if style&styleCode != 0 {
			style = styleCode // code cannot contain other entities
		}
		for i := len(styleTags) - 1; i >= 0; i-- {
			if style&styleTags[i].flag != 0 {
				text = "<" + styleTags[i].tag + ">" + text + "</" + styleTags[i].tag + ">"
			}
		}
		if r.href != "" && style&styleCode == 0 {
			text = `<a href="` + esc(r.href) + `">` + text + "</a>"
		}
		sb.WriteString(lead + text + trail)
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sb.String()), "<br>"))
}

// ---- HTML email → blocks ----

type converter struct {
	blocks  []*block
	cur     *block
	lists   int
	listCtx []listCtx
	quote   int
	pre     int
	heading int
}

type listCtx struct {
	id      int
	ordered bool
}

// htmlToBlocks converts an HTML email into a flat list of blocks
func htmlToBlocks(source string) []*block {
	doc, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return nil
	}
	c := &converter{}
	c.walk(doc, 0, "", 0)
	c.flush()
	return c.blocks
}

// kind returns the block kind for text in the current context
func (c *converter) newBlock() *block {
	switch {
	case c.pre > 0:
		return &block{kind: blockPre}
	case c.heading > 0:
		return &block{kind: blockHeading, level: c.heading}
	case len(c.listCtx) > 0:
		l := c.listCtx[len(c.listCtx)-1]
		return &block{kind: blockListItem, list: l.id, ordered: l.ordered}
	case c.quote > 0:
		return &block{kind: blockQuote}
	default:
		return &block{kind: blockParagraph}
	}
}

// flush finishes the current block
func (c *converter) flush() {
	if c.cur == nil {
		return
	}
	b := c.cur
	c.cur = nil
	// Drop trailing line breaks
	for len(b.runs) > 0 && b.runs[len(b.runs)-1].br {
		b.runs = b.runs[:len(b.runs)-1]
	}
	if !b.empty() {
		c.blocks = append(c.blocks, b)
	}
}

func (c *converter) addText(text string, style int, href string) {
	if c.pre == 0 {
		text = spaceRegex.ReplaceAllString(text, " ")
	}
	text = invisibleRegex.ReplaceAllString(text, "")
	if text == "" {
		return
	}
	if c.cur == nil {
		if c.pre == 0 {
			text = strings.TrimLeft(text, " ")
			if text == "" {
				return
			}
		}
		c.cur = c.newBlock()
	} else if c.pre == 0 && strings.HasPrefix(text, " ") {
		// Avoid double spaces between runs and spaces at the start of a line
		if n := len(c.cur.runs); n == 0 || c.cur.runs[n-1].br || strings.HasSuffix(c.cur.runs[n-1].text, " ") {
			text = strings.TrimLeft(text, " ")
		}
	}
	if text == "" {
		return
	}
	c.cur.runs = append(c.cur.runs, run{text: text, style: style, href: href})
}

func (c *converter) addBreak() {
	if c.cur == nil {
		return
	}
	if n := len(c.cur.runs); n == 0 || c.cur.runs[n-1].br {
		return // no leading breaks, at most one in a row
	}
	c.cur.runs = append(c.cur.runs, run{br: true})
}

// boundary separates blocks; inside list items and quotes it is a line break
func (c *converter) boundary() {
	if c.pre > 0 {
		return
	}
	if len(c.listCtx) > 0 && c.cur != nil && c.cur.kind == blockListItem {
		c.addBreak()
		return
	}
	c.flush()
}

func isHidden(n *xhtml.Node) bool {
	for _, a := range n.Attr {
		switch a.Key {
		case "hidden":
			return true
		case "style":
			s := strings.ToLower(strings.ReplaceAll(a.Val, " ", ""))
			if strings.Contains(s, "display:none") || strings.Contains(s, "mso-hide:all") ||
				strings.Contains(s, "visibility:hidden") ||
				(strings.Contains(s, "max-height:0") && strings.Contains(s, "overflow:hidden")) {
				return true
			}
		}
	}
	return false
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// safeHref keeps only links Telegram accepts
func safeHref(href string) string {
	href = strings.TrimSpace(href)
	lower := strings.ToLower(href)
	for _, prefix := range []string{"http://", "https://", "mailto:", "tel:"} {
		if strings.HasPrefix(lower, prefix) && len(href) <= 2048 {
			return href
		}
	}
	return ""
}

func (c *converter) walk(n *xhtml.Node, style int, href string, depth int) {
	if depth > 200 {
		return // pathological nesting
	}

	switch n.Type {
	case xhtml.TextNode:
		c.addText(n.Data, style, href)
		return
	case xhtml.DocumentNode:
		c.walkChildren(n, style, href, depth)
		return
	case xhtml.ElementNode:
	default:
		return
	}

	if isHidden(n) {
		return
	}

	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Head, atom.Title, atom.Meta, atom.Link,
		atom.Noscript, atom.Template, atom.Svg, atom.Iframe, atom.Object,
		atom.Select, atom.Option, atom.Input, atom.Textarea:
		return

	case atom.Br:
		if c.pre > 0 {
			c.addText("\n", style, href)
		} else {
			c.addBreak()
		}
		return

	case atom.Hr:
		c.flush()
		if n := len(c.blocks); c.quote == 0 && len(c.listCtx) == 0 && (n == 0 || c.blocks[n-1].kind != blockRule) {
			c.blocks = append(c.blocks, &block{kind: blockRule})
		}
		return

	case atom.Img:
		// Images are mostly logos and tracking pixels. Keep the alt text of
		// linked images: it is often the only label of a button.
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" && href != "" {
			c.addText(alt, style, href)
		}
		return

	case atom.A:
		link := safeHref(attr(n, "href"))
		if link == "" {
			link = href
		}
		before := c.runCount()
		c.walkChildren(n, style, link, depth)
		if link != "" && c.runCount() == before && strings.HasPrefix(link, "http") {
			c.addText("ссылка", style, link)
		}
		return

	case atom.B, atom.Strong:
		style |= styleBold
	case atom.I, atom.Em, atom.Cite, atom.Var:
		style |= styleItalic
	case atom.U, atom.Ins:
		style |= styleUnderline
	case atom.S, atom.Strike, atom.Del:
		style |= styleStrike
	case atom.Code, atom.Tt, atom.Kbd, atom.Samp:
		style |= styleCode
	case atom.Mark:
		style |= styleMark
	case atom.Sup:
		style |= styleSup
	case atom.Sub:
		style |= styleSub

	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		c.flush()
		if c.heading == 0 && c.quote == 0 && len(c.listCtx) == 0 {
			// Email headings are big; shift them down one level
			c.heading = min(int(n.Data[1]-'0')+2, 6)
			c.walkChildren(n, style, href, depth)
			c.flush()
			c.heading = 0
		} else {
			c.walkChildren(n, style|styleBold, href, depth)
			c.boundary()
		}
		return

	case atom.Pre:
		c.flush()
		c.pre++
		c.walkChildren(n, style, href, depth)
		c.flush()
		c.pre--
		return

	case atom.Blockquote:
		c.flush()
		c.quote++
		c.walkChildren(n, style, href, depth)
		c.flush()
		c.quote--
		return

	case atom.Ul, atom.Ol:
		c.flush()
		if len(c.listCtx) > 0 {
			// Nested lists are flattened into the outer list
			c.listCtx = append(c.listCtx, c.listCtx[len(c.listCtx)-1])
		} else {
			c.lists++
			c.listCtx = append(c.listCtx, listCtx{id: c.lists, ordered: n.DataAtom == atom.Ol})
		}
		c.walkChildren(n, style, href, depth)
		c.flush()
		c.listCtx = c.listCtx[:len(c.listCtx)-1]
		return

	case atom.Li:
		c.flush()
		if len(c.listCtx) == 0 {
			// <li> outside of a list
			c.lists++
			c.listCtx = append(c.listCtx, listCtx{id: c.lists})
			defer func() { c.listCtx = c.listCtx[:len(c.listCtx)-1] }()
		}
		c.cur = c.newBlock()
		c.walkChildren(n, style, href, depth)
		c.flush()
		return

	case atom.P, atom.Div, atom.Table, atom.Tbody, atom.Thead, atom.Tfoot,
		atom.Tr, atom.Td, atom.Th, atom.Section, atom.Article, atom.Header,
		atom.Footer, atom.Main, atom.Nav, atom.Aside, atom.Center, atom.Address,
		atom.Dl, atom.Dt, atom.Dd, atom.Figure, atom.Figcaption, atom.Form,
		atom.Fieldset, atom.Body, atom.Html, atom.Caption, atom.Details, atom.Summary:
		c.boundary()
		if n.DataAtom == atom.Th {
			style |= styleBold
		}
		c.walkChildren(n, withInlineStyle(n, style), href, depth)
		c.boundary()
		return

	default:
		// span, font, label and unknown tags are transparent
		style = withInlineStyle(n, style)
	}

	c.walkChildren(n, style, href, depth)
}

func (c *converter) walkChildren(n *xhtml.Node, style int, href string, depth int) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		c.walk(child, style, href, depth+1)
	}
}

func (c *converter) runCount() int {
	n := 0
	for _, b := range c.blocks {
		n += len(b.runs)
	}
	if c.cur != nil {
		n += len(c.cur.runs)
	}
	return n
}

// withInlineStyle applies bold/italic from a style attribute
func withInlineStyle(n *xhtml.Node, style int) int {
	s := strings.ToLower(attr(n, "style"))
	if s == "" {
		return style
	}
	if boldWeight.MatchString(s) {
		style |= styleBold
	}
	if strings.Contains(s, "font-style:italic") || strings.Contains(s, "font-style: italic") {
		style |= styleItalic
	}
	return style
}

// textToBlocks converts a plain text body into paragraphs and quotes
func textToBlocks(text string) []*block {
	text = invisibleRegex.ReplaceAllString(strings.ReplaceAll(text, "\r\n", "\n"), "")
	var blocks []*block
	var cur *block

	flush := func() {
		if cur != nil && !cur.empty() {
			blocks = append(blocks, cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		kind := blockParagraph
		if strings.HasPrefix(trimmed, ">") {
			kind = blockQuote
			trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "> "))
		}
		if trimmed == "" && kind == blockParagraph {
			flush()
			continue
		}
		if cur != nil && cur.kind != kind {
			flush()
		}
		if cur == nil {
			cur = &block{kind: kind}
		} else {
			cur.runs = append(cur.runs, run{br: true})
		}
		cur.runs = append(cur.runs, run{text: spaceRegex.ReplaceAllString(trimmed, " ")})
	}
	flush()
	return blocks
}

func esc(s string) string {
	return html.EscapeString(s)
}
