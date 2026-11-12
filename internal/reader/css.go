package reader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

const maxCSSBytes = 4 << 20

// SanitizeCSS preserves static publisher styling while rewriting every resource
// reference through the reader session. Malformed stylesheets are discarded.
// The original EPUB is never changed.
func SanitizeCSS(ctx context.Context, src []byte, resolve Resolver) ([]byte, error) {
	return sanitizeCSS(ctx, src, resolve, false)
}

func sanitizeCSS(ctx context.Context, src []byte, resolve Resolver, inline bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(src) > maxCSSBytes {
		return nil, errors.New("reader stylesheet exceeds size limit")
	}
	p := css.NewParser(parse.NewInput(bytes.NewReader(src)), inline)
	var out strings.Builder
	// A false stack entry suppresses the complete body of an unsupported at-rule.
	var stack []bool
	blocked := 0
	for steps := 0; ; steps++ {
		if steps > 500000 {
			return nil, errors.New("reader stylesheet exceeds token limit")
		}
		if steps%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		gt, _, data := p.Next()
		if gt == css.ErrorGrammar {
			if p.HasParseError() || p.Err() != io.EOF {
				return []byte{}, nil
			}
			break
		}
		switch gt {
		case css.BeginAtRuleGrammar, css.BeginRulesetGrammar:
			allowed := blocked == 0
			name := strings.ToLower(cssUnescape(string(data)))
			if gt == css.BeginAtRuleGrammar && !safeAtRule(name) {
				allowed = false
			}
			values, ok := safeCSSTokens(p.Values(), resolve, "asset", false)
			allowed = allowed && ok
			stack = append(stack, allowed)
			if !allowed {
				blocked++
				continue
			}
			out.Write(data)
			out.WriteString(values)
			out.WriteByte('{')
		case css.EndAtRuleGrammar, css.EndRulesetGrammar:
			if len(stack) == 0 {
				return []byte{}, nil
			}
			allowed := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if allowed {
				out.WriteByte('}')
			} else {
				blocked--
			}
		case css.AtRuleGrammar:
			if blocked != 0 || inline {
				continue
			}
			name := strings.ToLower(cssUnescape(string(data)))
			if name == "@import" {
				values, ok := safeCSSTokens(p.Values(), resolve, "stylesheet", true)
				if ok {
					out.WriteString("@import ")
					out.WriteString(values)
					out.WriteByte(';')
				}
			} else if name == "@layer" {
				values, ok := safeCSSTokens(p.Values(), resolve, "asset", false)
				if ok {
					out.WriteString("@layer ")
					out.WriteString(values)
					out.WriteByte(';')
				}
			}
		case css.DeclarationGrammar, css.CustomPropertyGrammar:
			if blocked != 0 {
				continue
			}
			property := strings.ToLower(cssUnescape(string(data)))
			if !safeCSSProperty(property) {
				continue
			}
			role := "asset"
			if property == "src" {
				role = "font"
			}
			values, ok := safeCSSTokens(p.Values(), resolve, role, false)
			if !ok {
				continue
			}
			// Preserve spelling/escaping. Custom-property names are case-sensitive,
			// and decoding an escaped delimiter into output could change syntax.
			out.Write(data)
			out.WriteByte(':')
			out.WriteString(values)
			out.WriteByte(';')
		case css.CommentGrammar:
			// Comments, including sourceMappingURL, are unnecessary in a preview.
		default:
			// Do not copy unparsed syntax into a browser stylesheet.
		}
		if out.Len() > maxCSSBytes*2 {
			return nil, errors.New("reader stylesheet output exceeds size limit")
		}
	}
	if len(stack) != 0 {
		return []byte{}, nil
	}
	return []byte(out.String()), nil
}

func safeAtRule(name string) bool {
	switch name {
	case "@font-face", "@media", "@supports", "@page", "@layer", "@container":
		return true
	}
	return false
}

func safeCSSProperty(name string) bool {
	if name == "behavior" || name == "-moz-binding" || name == "-ms-behavior" || name == "binding" {
		return false
	}
	// A reader preview is static; book-authored continuous animations can consume
	// excessive CPU and do not help inspect its ordinary reading content.
	return !strings.Contains(name, "animation") && !strings.Contains(name, "transition")
}

func safeCSSTokens(tokens []css.Token, resolve Resolver, role string, importRule bool) (string, bool) {
	var out strings.Builder
	importSeen := false
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch t.TokenType {
		case css.CustomPropertyValueToken:
			// The grammar parser deliberately preserves a custom property's value
			// as one raw token. Lex that value again so URLs/functions cannot hide
			// behind var() and bypass the same resource rules as other values.
			l := css.NewLexer(parse.NewInput(bytes.NewReader(t.Data)))
			var parts []css.Token
			for count := 0; ; count++ {
				if count > 500000 {
					return "", false
				}
				typ, data := l.Next()
				if typ == css.ErrorToken {
					if l.Err() != io.EOF {
						return "", false
					}
					break
				}
				parts = append(parts, css.Token{TokenType: typ, Data: append([]byte(nil), data...)})
			}
			value, ok := safeCSSTokens(parts, resolve, role, false)
			if !ok {
				return "", false
			}
			out.WriteString(value)
		case css.BadURLToken, css.BadStringToken, css.ErrorToken:
			return "", false
		case css.CommentToken:
			out.WriteByte(' ')
		case css.URLToken:
			ref, ok := cssURLValue(string(t.Data))
			if !ok {
				return "", false
			}
			mapped := safeResourceURL(ref, role, resolve)
			if mapped == "" {
				return "", false
			}
			out.WriteString("url(")
			out.WriteString(cssQuote(mapped))
			out.WriteByte(')')
			importSeen = true
		case css.FunctionToken:
			fn := strings.ToLower(cssUnescape(strings.TrimSuffix(string(t.Data), "(")))
			if fn == "url" {
				// Quoted url() is tokenized as a function + string + closing bracket.
				j := i + 1
				for j < len(tokens) && (tokens[j].TokenType == css.WhitespaceToken || tokens[j].TokenType == css.CommentToken) {
					j++
				}
				if j >= len(tokens) {
					return "", false
				}
				var ref string
				if tokens[j].TokenType == css.StringToken {
					var ok bool
					ref, ok = cssStringValue(string(tokens[j].Data))
					if !ok {
						return "", false
					}
					j++
				} else {
					// An escaped function name (u\\72l) is a FunctionToken even
					// for an unquoted URL. Decode its complete bounded argument.
					var arg strings.Builder
					for j < len(tokens) && string(tokens[j].Data) != ")" {
						if tokens[j].TokenType == css.FunctionToken || tokens[j].TokenType == css.StringToken || tokens[j].TokenType == css.BadStringToken || tokens[j].TokenType == css.BadURLToken {
							return "", false
						}
						arg.Write(tokens[j].Data)
						j++
					}
					ref = cssUnescape(strings.TrimSpace(arg.String()))
				}
				for j < len(tokens) && (tokens[j].TokenType == css.WhitespaceToken || tokens[j].TokenType == css.CommentToken) {
					j++
				}
				if j >= len(tokens) || string(tokens[j].Data) != ")" {
					return "", false
				}
				mapped := safeResourceURL(ref, role, resolve)
				if mapped == "" {
					return "", false
				}
				out.WriteString("url(")
				out.WriteString(cssQuote(mapped))
				out.WriteByte(')')
				i = j
				importSeen = true
			} else {
				// These can interpret plain strings as URLs, execute legacy CSS, or
				// create URL values indirectly. URLToken rewriting alone is insufficient.
				switch fn {
				case "expression", "image-set", "-webkit-image-set", "image", "src", "attr", "paint", "element", "-moz-element":
					return "", false
				}
				out.Write(t.Data)
			}
		case css.StringToken:
			if importRule && !importSeen {
				ref, ok := cssStringValue(string(t.Data))
				if !ok {
					return "", false
				}
				mapped := safeResourceURL(ref, "stylesheet", resolve)
				if mapped == "" {
					return "", false
				}
				out.WriteString(cssQuote(mapped))
				importSeen = true
			} else {
				out.Write(t.Data)
			}
		default:
			out.Write(t.Data)
		}
	}
	if importRule && !importSeen {
		return "", false
	}
	return out.String(), true
}

func cssURLValue(raw string) (string, bool) {
	start := strings.IndexByte(raw, '(')
	if start < 0 || !strings.HasSuffix(strings.TrimSpace(raw), ")") {
		return "", false
	}
	raw = strings.TrimSpace(strings.TrimSpace(raw)[start+1 : len(strings.TrimSpace(raw))-1])
	if len(raw) > 0 && (raw[0] == '\'' || raw[0] == '"') {
		return cssStringValue(raw)
	}
	return cssUnescape(raw), true
}

func cssStringValue(raw string) (string, bool) {
	if len(raw) < 2 || (raw[0] != '\'' && raw[0] != '"') || raw[len(raw)-1] != raw[0] {
		return "", false
	}
	return cssUnescape(raw[1 : len(raw)-1]), true
}

func cssQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || r == '<' || r == '>':
			b.WriteByte('\\')
			b.WriteString(strconv.FormatInt(int64(r), 16))
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// CSS escapes must be decoded before checking function names or resource URLs.
func cssUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			r, n := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += n
			continue
		}
		i++
		if i == len(s) {
			break
		}
		if s[i] == '\n' || s[i] == '\r' || s[i] == '\f' {
			if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			i++
			continue
		}
		start := i
		for i < len(s) && i-start < 6 && ((s[i] >= '0' && s[i] <= '9') || (s[i] >= 'a' && s[i] <= 'f') || (s[i] >= 'A' && s[i] <= 'F')) {
			i++
		}
		if i > start {
			n, _ := strconv.ParseInt(s[start:i], 16, 32)
			r := rune(n)
			if r == 0 || r > utf8.MaxRune || (r >= 0xd800 && r <= 0xdfff) {
				r = utf8.RuneError
			}
			b.WriteRune(r)
			if i < len(s) && strings.ContainsRune(" \t\r\n\f", rune(s[i])) {
				if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
					i++
				}
				i++
			}
		} else {
			r, n := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += n
		}
	}
	return b.String()
}
