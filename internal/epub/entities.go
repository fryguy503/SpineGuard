package epub

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
)

var knownXHTMLDoctype = regexp.MustCompile(`(?s)^DOCTYPE\s+html\s+PUBLIC\s+(?:"([^"]+)"|'([^']+)')\s+(?:"([^"]+)"|'([^']+)')\s*$`)

// Recognized external XHTML DTDs define the standard HTML4 named characters.
// No DTD is fetched, and any internal subset/custom declarations disable this
// assistance so an external definition cannot be silently reinterpreted.
func recognizedDoctypeEntities(src []byte) map[string]string {
	if len(src) > maxTextBytes {
		return nil
	}
	d := xml.NewDecoder(bytes.NewReader(src))
	d.CharsetReader = charset.NewReaderLabel
	var accepted map[string]string
	for n := 0; n < 1024; n++ {
		t, err := d.Token()
		if err != nil {
			return nil
		}
		switch token := t.(type) {
		case xml.StartElement:
			if token.Name.Local == "html" && token.Name.Space == xhtmlNS {
				return accepted
			}
			return nil
		case xml.Directive:
			declaration := strings.TrimSpace(string(token))
			if !strings.HasPrefix(declaration, "DOCTYPE") {
				if strings.Contains(declaration, "ENTITY") {
					return nil
				}
				continue
			}
			if accepted != nil {
				return nil
			}
			if strings.ContainsAny(declaration, "[]") {
				return nil
			}
			m := knownXHTMLDoctype.FindStringSubmatch(declaration)
			if m == nil {
				return nil
			}
			public, system := m[1]+m[2], m[3]+m[4]
			var expected string
			switch public {
			case "-//W3C//DTD XHTML 1.1//EN":
				expected = "www.w3.org/TR/xhtml11/DTD/xhtml11.dtd"
			case "-//W3C//DTD XHTML 1.0 Strict//EN":
				expected = "www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd"
			case "-//W3C//DTD XHTML 1.0 Transitional//EN":
				expected = "www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd"
			case "-//W3C//DTD XHTML 1.0 Frameset//EN":
				expected = "www.w3.org/TR/xhtml1/DTD/xhtml1-frameset.dtd"
			default:
				return nil
			}
			if system != "http://"+expected && system != "https://"+expected {
				return nil
			}
			accepted = xml.HTMLEntity
			d.Entity = accepted
		}
	}
	return nil
}

// normalizeStandardEntities uses original token offsets. Comments, DTDs, CDATA,
// processing instructions and predefined XML entities are never rewritten.
func normalizeStandardEntities(ctx context.Context, src []byte) ([]byte, int, error) {
	entities := recognizedDoctypeEntities(src)
	if entities == nil {
		return src, 0, nil
	}
	if !utf8.Valid(src) {
		return src, 0, fmt.Errorf("entity normalization requires UTF-8 XML")
	}
	if _, err := parseEditableXML(ctx, src); err != nil {
		return src, 0, err
	}
	d := xml.NewDecoder(bytes.NewReader(src))
	d.Entity = entities
	var edits []xmlEdit
	count := 0
	addReferences := func(start, end int) {
		for i := start; i < end; i++ {
			if src[i] != '&' {
				continue
			}
			j := i + 1
			for j < end && ((src[j] >= 'a' && src[j] <= 'z') || (src[j] >= 'A' && src[j] <= 'Z') || (src[j] >= '0' && src[j] <= '9')) {
				j++
			}
			if j >= end || src[j] != ';' || j == i+1 {
				continue
			}
			name := string(src[i+1 : j])
			if name == "amp" || name == "lt" || name == "gt" || name == "apos" || name == "quot" {
				i = j
				continue
			}
			value, ok := entities[name]
			if !ok {
				continue
			}
			var replacement strings.Builder
			for _, r := range value {
				fmt.Fprintf(&replacement, "&#x%X;", r)
			}
			edits = append(edits, xmlEdit{Start: i, End: j + 1, Replacement: []byte(replacement.String())})
			count++
			i = j
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return src, 0, err
		}
		start := int(d.InputOffset())
		token, err := d.Token()
		end := int(d.InputOffset())
		if err == io.EOF {
			break
		}
		if err != nil {
			return src, 0, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			attrs, _, err := lexXMLStart(src, start, end, t.Attr)
			if err != nil {
				return src, 0, err
			}
			for _, at := range attrs {
				addReferences(at.Start, at.End)
			}
		case xml.CharData:
			if start < end && src[start] != '<' {
				addReferences(start, end)
			}
		}
	}
	if count == 0 {
		return src, 0, nil
	}
	result, err := applyXMLEdits(src, edits)
	return result, count, err
}
