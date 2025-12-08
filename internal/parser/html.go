package parser

import (
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// HTMLParser parses HTML emails to plain text
type HTMLParser struct {
	whitespaceRegex  *regexp.Regexp
	newlineRegex     *regexp.Regexp
	invisibleRegex   *regexp.Regexp
	emptyLineRegex   *regexp.Regexp
}

// NewHTMLParser creates a new HTML parser
func NewHTMLParser() *HTMLParser {
	return &HTMLParser{
		whitespaceRegex:  regexp.MustCompile(`[^\S\n]+`),
		newlineRegex:     regexp.MustCompile(`\n{3,}`),
		// Remove invisible Unicode characters (zero-width spaces, etc.)
		invisibleRegex:   regexp.MustCompile(`[\x{200B}-\x{200D}\x{FEFF}\x{00AD}\x{034F}\x{061C}\x{115F}\x{1160}\x{17B4}\x{17B5}\x{180E}\x{2060}-\x{2064}\x{206A}-\x{206F}\x{FE00}-\x{FE0F}\x{FFF0}-\x{FFF8}]+`),
		// Remove lines that only contain whitespace or invisible chars
		emptyLineRegex:   regexp.MustCompile(`(?m)^\s*$`),
	}
}

// Parse converts HTML to clean plain text
func (p *HTMLParser) Parse(html string) (string, error) {
	if html == "" {
		return "", nil
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}

	// Remove script and style elements
	doc.Find("script, style, head, meta, link").Remove()

	// Add newlines before block elements
	doc.Find("p, div, br, h1, h2, h3, h4, h5, h6, li, tr").Each(func(i int, s *goquery.Selection) {
		s.PrependHtml("\n")
	})

	// Get text content
	text := doc.Text()

	// Remove invisible Unicode characters first
	text = p.invisibleRegex.ReplaceAllString(text, "")

	// Clean up whitespace (but preserve newlines)
	text = p.whitespaceRegex.ReplaceAllString(text, " ")

	// Trim leading/trailing whitespace from each line and filter empty lines
	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}
	text = strings.Join(cleanLines, "\n")

	// Normalize newlines (max 2 consecutive)
	text = p.newlineRegex.ReplaceAllString(text, "\n\n")

	// Final trim
	text = strings.TrimSpace(text)

	return text, nil
}
