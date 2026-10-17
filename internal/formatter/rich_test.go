package formatter

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"

	"github.com/mixelka/emailresend/pkg/models"
)

// Tags Telegram rich HTML accepts that RichEmail may produce
var allowedRichTags = map[string]bool{
	"b": true, "i": true, "u": true, "s": true, "code": true, "mark": true, "sup": true, "sub": true,
	"a": true, "br": true, "p": true, "h3": true, "h4": true, "h5": true, "h6": true, "pre": true,
	"hr": true, "ul": true, "ol": true, "li": true, "blockquote": true, "details": true,
	"summary": true, "tg-time": true,
}

// checkRichHTML verifies tags, nesting depth and limits of rich HTML
func checkRichHTML(t *testing.T, out string) {
	t.Helper()

	if n := utf8.RuneCountInString(out); n > 32768 {
		t.Errorf("rich message is %d characters, limit is 32768", n)
	}

	z := xhtml.NewTokenizer(strings.NewReader(out))
	var stack []string
	maxDepth, blocks := 0, 0
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		name, _ := z.TagName()
		tag := string(name)
		switch tt {
		case xhtml.StartTagToken:
			if !allowedRichTags[tag] {
				t.Errorf("unsupported tag <%s>", tag)
			}
			switch tag {
			case "p", "h3", "h4", "h5", "h6", "pre", "li", "blockquote", "details", "hr", "ul", "ol":
				blocks++
			}
			if tag == "br" || tag == "hr" {
				continue
			}
			stack = append(stack, tag)
			maxDepth = max(maxDepth, len(stack))
		case xhtml.SelfClosingTagToken:
			if !allowedRichTags[tag] {
				t.Errorf("unsupported tag <%s/>", tag)
			}
			blocks++
		case xhtml.EndTagToken:
			if len(stack) == 0 || stack[len(stack)-1] != tag {
				t.Fatalf("misnested </%s>, open: %v\n%s", tag, stack, out)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		t.Errorf("unclosed tags: %v", stack)
	}
	if maxDepth > 16 {
		t.Errorf("nesting depth %d exceeds 16", maxDepth)
	}
	if blocks > 500 {
		t.Errorf("%d blocks exceed 500", blocks)
	}
}

const marketingEmail = `<!DOCTYPE html><html><head><style>.x{color:red}</style><title>t</title></head>
<body>
<div style="display:none;max-height:0;overflow:hidden">Preheader text you should not see</div>
<table width="100%"><tr><td>
  <table><tr><td><img src="https://x/logo.png" alt="Logo"></td></tr></table>
  <h1>Подтвердите <em>адрес</em></h1>
  <p>Здравствуйте, <b>Иван</b>!&nbsp;&nbsp;Ваш код:</p>
  <p style="font-size:32px;font-weight:700">482913</p>
  <p><a href="https://example.com/confirm?token=a&amp;b=c"><img src="https://x/btn.png" alt="Подтвердить"></a></p>
  <p><a href="javascript:alert(1)">evil</a> <a href="https://example.com/x"></a></p>
  <ul><li>Пункт <i>один</i><ul><li>вложенный</li></ul></li><li><p>Пункт два</p><p>вторая строка</p></li></ul>
  <blockquote>On Monday someone wrote:<br>&gt; old text</blockquote>
  <pre>  code   block
  line 2</pre>
  <p>A &lt;tag&gt; &amp; "quotes"<span><b><i><u><s><code>deep</code></s></u></i></b></span></p>
  <hr><hr>
  <script>alert(1)</script>
  <p>&#8203;&#8203;</p>
</td></tr></table>
<img src="https://track/pixel.gif" width="1" height="1">
</body></html>`

func TestRichEmailFromHTML(t *testing.T) {
	f := NewTelegramFormatter()
	msg := &models.EmailMessage{
		FromAddr:   "noreply@example.com",
		FromName:   "Shop <Best> & Co",
		Subject:    "Код <подтверждения>",
		BodyHTML:   marketingEmail,
		ReceivedAt: time.Date(2026, 10, 17, 22, 45, 0, 0, time.UTC),
	}
	out := f.RichEmail(msg, []models.DetectedCode{{Type: "otp", Value: "482913"}})
	checkRichHTML(t, out)

	for _, want := range []string{
		"<h3>Код &lt;подтверждения&gt;</h3>",
		"Shop &lt;Best&gt; &amp; Co",
		`<tg-time unix="1792277100" format="Dt">`,
		"<code>482913</code>",
		"<h3>Подтвердите <i>адрес</i></h3>",
		"<b>Иван</b>",
		`<a href="https://example.com/confirm?token=a&amp;b=c">Подтвердить</a>`,
		`<a href="https://example.com/x">ссылка</a>`,
		"<li>Пункт <i>один</i></li><li>вложенный</li>",
		"<li>Пункт два<br>вторая строка</li>",
		"<blockquote expandable>On Monday someone wrote:<br>&gt; old text</blockquote>",
		"<pre>  code   block\n  line 2</pre>",
		"A &lt;tag&gt; &amp; &#34;quotes&#34;<code>deep</code>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Preheader", "javascript", "alert", "Logo", ".x{", "<hr/><hr/>", "​"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected %q in\n%s", unwanted, out)
		}
	}
}

func TestRichEmailPlainTextAndLimits(t *testing.T) {
	f := NewTelegramFormatter()

	plain := &models.EmailMessage{
		FromAddr: "a@b.c",
		BodyText: "Hello,\nsecond line\n\n> quoted\n> more\n\nBye",
	}
	out := f.RichEmail(plain, nil)
	checkRichHTML(t, out)
	if !strings.Contains(out, "<p>Hello,<br>second line</p><blockquote expandable>quoted<br>more</blockquote><p>Bye</p>") {
		t.Errorf("plain text body not converted:\n%s", out)
	}
	if !strings.Contains(out, "(без темы)") {
		t.Error("empty subject placeholder missing")
	}

	// Huge email: many blocks and a lot of text
	var sb strings.Builder
	for i := 0; i < 3000; i++ {
		sb.WriteString("<p>Paragraph with some text that goes on and on " + strings.Repeat("я", 20) + "</p>")
	}
	huge := &models.EmailMessage{FromAddr: "a@b.c", Subject: "big", BodyHTML: sb.String()}
	out = f.RichEmail(huge, nil)
	checkRichHTML(t, out)
	if !strings.Contains(out, "<details><summary>Читать дальше</summary>") {
		t.Error("long body is not collapsed")
	}
	if !strings.Contains(out, "письмо слишком длинное и обрезано") {
		t.Error("truncation note missing")
	}

	// Pathologically deep nesting must not break the limits
	deep := strings.Repeat("<div><span><b>", 500) + "deep text" + strings.Repeat("</b></span></div>", 500)
	out = f.RichEmail(&models.EmailMessage{FromAddr: "a@b.c", BodyHTML: deep}, nil)
	checkRichHTML(t, out)

	// Broken HTML
	out = f.RichEmail(&models.EmailMessage{FromAddr: "a@b.c", BodyHTML: "<p><b>unclosed <i>tags<li>item</table></p"}, nil)
	checkRichHTML(t, out)

	// Empty body
	out = f.RichEmail(&models.EmailMessage{FromAddr: "a@b.c", BodyHTML: "<html><body><img src=x></body></html>"}, nil)
	checkRichHTML(t, out)
	if !strings.Contains(out, "(пустое письмо)") {
		t.Errorf("empty body placeholder missing:\n%s", out)
	}
}
