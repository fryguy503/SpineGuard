package epub

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

// This proof lexer does not rewrite CSS. It recognizes possible ID dependencies
// and refuses unsupported syntax rather than treating an unreadable sheet as safe.
func cssIDDependency(src []byte, id string) (bool, string) {
	if !utf8.Valid(src) {
		return true, "A stylesheet is not readable UTF-8; identifier dependencies cannot be verified."
	}
	braces, parens := 0, 0
	for i := 0; i < len(src); {
		if cssWhitespace(src[i]) {
			i++
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return true, "A stylesheet has an unterminated comment."
			}
			i += end + 4
			continue
		}
		if src[i] == '\'' || src[i] == '"' {
			value, next, ok := cssReadString(src, i)
			if !ok {
				return true, "A stylesheet string could not be verified."
			}
			if u, e := url.Parse(strings.TrimSpace(value)); e == nil && u.Fragment == id {
				return true, "A CSS URL or string refers to this identifier."
			}
			i = next
			continue
		}
		switch src[i] {
		case '#':
			name, next, ok := cssReadName(src, i+1)
			if !ok || next == i+1 {
				return true, "A stylesheet hash reference could not be verified."
			}
			if name == id {
				return true, "A CSS selector, color-like token, or URL fragment uses this identifier."
			}
			i = next
			continue
		case '[':
			local, next, ok := cssReadAttributeSelector(src, i)
			if !ok {
				return true, "A stylesheet attribute selector could not be verified."
			}
			if strings.EqualFold(local, "id") {
				return true, "A CSS attribute selector depends on identifier values."
			}
			i = next
			continue
		case ']':
			return true, "A stylesheet has an unmatched attribute-selector bracket."
		case '@':
			name, next, ok := cssReadName(src, i+1)
			if !ok || next == i+1 {
				return true, "A stylesheet at-rule could not be verified."
			}
			name = strings.ToLower(name)
			if name == "import" {
				return true, "Imported stylesheets require manual dependency review."
			}
			if !tokenHas("media supports layer container scope keyframes -webkit-keyframes font-face page charset namespace property starting-style", name) {
				return true, "An unsupported CSS at-rule requires manual dependency review."
			}
			i = next
			continue
		case '{':
			braces++
		case '}':
			braces--
			if braces < 0 {
				return true, "A stylesheet has unbalanced braces."
			}
		case '(':
			parens++
		case ')':
			parens--
			if parens < 0 {
				return true, "A stylesheet has unbalanced parentheses."
			}
		}
		if cssNameStart(src[i]) {
			name, next, ok := cssReadName(src, i)
			if !ok {
				return true, "A CSS escape could not be verified."
			}
			j, ok := cssSkipSpace(src, next)
			if !ok {
				return true, "A CSS comment could not be verified."
			}
			if strings.EqualFold(name, "attr") && j < len(src) && src[j] == '(' {
				return true, "CSS attr() can expose identifier spelling and requires manual review."
			}
			i = next
			continue
		}
		if src[i] == '\\' {
			return true, "A CSS escape could not be verified."
		}
		i++
	}
	if braces != 0 || parens != 0 {
		return true, "A stylesheet has unbalanced structure."
	}
	return false, ""
}

func cssWhitespace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }
func cssNameStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '\\' || c >= 128
}
func cssHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
func cssEscape(src []byte, i int) (rune, int, bool) {
	if i >= len(src) || src[i] != '\\' || i+1 >= len(src) {
		return 0, i, false
	}
	i++
	if cssHex(src[i]) >= 0 {
		value := 0
		n := 0
		for i < len(src) && n < 6 && cssHex(src[i]) >= 0 {
			value = value*16 + cssHex(src[i])
			i++
			n++
		}
		if i < len(src) && cssWhitespace(src[i]) {
			if src[i] == '\r' && i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			i++
		}
		if value == 0 || value > utf8.MaxRune || value >= 0xd800 && value <= 0xdfff {
			return 0, i, false
		}
		return rune(value), i, true
	}
	if src[i] == '\n' || src[i] == '\r' || src[i] == '\f' {
		return 0, i, false
	}
	r, n := utf8.DecodeRune(src[i:])
	if r == utf8.RuneError && n == 1 {
		return 0, i, false
	}
	return r, i + n, true
}
func cssReadName(src []byte, i int) (string, int, bool) {
	var result strings.Builder
	for i < len(src) && cssNameStart(src[i]) {
		if src[i] == '\\' {
			r, next, ok := cssEscape(src, i)
			if !ok {
				return "", i, false
			}
			result.WriteRune(r)
			i = next
		} else {
			r, n := utf8.DecodeRune(src[i:])
			if r == utf8.RuneError && n == 1 {
				return "", i, false
			}
			result.WriteRune(r)
			i += n
		}
	}
	return result.String(), i, true
}
func cssReadString(src []byte, i int) (string, int, bool) {
	quote := src[i]
	i++
	var result strings.Builder
	for i < len(src) {
		if src[i] == quote {
			return result.String(), i + 1, true
		}
		if src[i] == '\n' || src[i] == '\r' || src[i] == '\f' {
			return "", i, false
		}
		if src[i] == '\\' {
			if i+1 < len(src) && (src[i+1] == '\n' || src[i+1] == '\r' || src[i+1] == '\f') {
				i += 2
				if src[i-1] == '\r' && i < len(src) && src[i] == '\n' {
					i++
				}
				continue
			}
			r, next, ok := cssEscape(src, i)
			if !ok {
				return "", i, false
			}
			result.WriteRune(r)
			i = next
		} else {
			r, n := utf8.DecodeRune(src[i:])
			if r == utf8.RuneError && n == 1 {
				return "", i, false
			}
			result.WriteRune(r)
			i += n
		}
	}
	return "", i, false
}
func cssSkipSpace(src []byte, i int) (int, bool) {
	for i < len(src) {
		if cssWhitespace(src[i]) {
			i++
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return i, false
			}
			i += end + 4
			continue
		}
		break
	}
	return i, true
}
func cssReadAttributeSelector(src []byte, i int) (string, int, bool) {
	i, ok := cssSkipSpace(src, i+1)
	if !ok || i >= len(src) {
		return "", i, false
	}
	local := ""
	if src[i] == '|' {
		i++
	} else {
		if src[i] == '*' {
			local = "*"
			i++
		} else {
			var good bool
			local, i, good = cssReadName(src, i)
			if !good || local == "" {
				return "", i, false
			}
		}
		if i < len(src) && src[i] == '|' && (i+1 >= len(src) || src[i+1] != '=') {
			i++
			local = ""
		} else {
			goto attributeNameRead
		}
	}
	local, i, ok = cssReadName(src, i)
	if !ok || local == "" {
		return "", i, false
	}
attributeNameRead:
	i, ok = cssSkipSpace(src, i)
	if !ok || i >= len(src) {
		return "", i, false
	}
	if src[i] == ']' {
		return local, i + 1, true
	}
	if src[i] != '=' {
		if !strings.ContainsRune("~|^$*", rune(src[i])) || i+1 >= len(src) || src[i+1] != '=' {
			return "", i, false
		}
		i++
	}
	i++
	i, ok = cssSkipSpace(src, i)
	if !ok || i >= len(src) {
		return "", i, false
	}
	if src[i] == '\'' || src[i] == '"' {
		_, i, ok = cssReadString(src, i)
	} else {
		var value string
		value, i, ok = cssReadName(src, i)
		if value == "" {
			ok = false
		}
	}
	if !ok {
		return "", i, false
	}
	i, ok = cssSkipSpace(src, i)
	if !ok || i >= len(src) {
		return "", i, false
	}
	if src[i] != ']' {
		flag, next, good := cssReadName(src, i)
		if !good || (!strings.EqualFold(flag, "i") && !strings.EqualFold(flag, "s")) {
			return "", i, false
		}
		i, ok = cssSkipSpace(src, next)
		if !ok || i >= len(src) {
			return "", i, false
		}
	}
	return local, i + 1, src[i] == ']'
}

func repairStyleText(ctx context.Context, src []byte, el xmlElementSpan) ([]byte, bool) {
	d := xml.NewDecoder(bytes.NewReader(src[el.Start:el.End]))
	depth := 0
	var out bytes.Buffer
	for {
		if ctx.Err() != nil {
			return nil, false
		}
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth > 1 {
				return nil, false
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 1 {
				out.Write(t)
			}
		}
	}
	return out.Bytes(), true
}
