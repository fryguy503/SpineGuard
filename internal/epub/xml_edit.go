package epub

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// Edits refer to bytes in the original UTF-8 document, never a serialized tree.
type xmlEdit struct {
	Start, End  int
	Replacement []byte
}
type xmlAttributeSpan struct {
	Name, Local, Value             string
	Start, End, FullStart, FullEnd int
	Quote                          byte
}
type xmlElementSpan struct {
	Name                xml.Name
	Start, OpenEnd, End int
	Attributes          []xmlAttributeSpan
	Parent              int
	SelfClosing         bool
}

func parseEditableXML(ctx context.Context, src []byte) ([]xmlElementSpan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(src) > maxTextBytes || !utf8.Valid(src) {
		return nil, errors.New("lossless repair requires a UTF-8 XML document within the text limit")
	}
	// A declared legacy encoding cannot safely receive newly encoded UTF-8 text.
	d := xml.NewDecoder(bytes.NewReader(src))
	d.Entity = recognizedDoctypeEntities(src)
	var elements []xmlElementSpan
	var stack []int
	roots := 0
	for tokens := 0; ; tokens++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tokens > 200000 {
			return nil, errors.New("XML repair token limit reached")
		}
		before := int(d.InputOffset())
		token, err := d.Token()
		after := int(d.InputOffset())
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.ProcInst:
			if t.Target == "xml" {
				text := strings.ToLower(string(t.Inst))
				if p := strings.Index(text, "encoding"); p >= 0 {
					text = strings.TrimSpace(text[p+8:])
					if strings.HasPrefix(text, "=") {
						text = strings.TrimSpace(text[1:])
						if len(text) > 1 && (text[0] == '\'' || text[0] == '"') {
							end := strings.IndexByte(text[1:], text[0])
							if end >= 0 {
								enc := text[1 : end+1]
								if enc != "utf-8" && enc != "utf8" {
									return nil, errors.New("lossless repair preserves non-UTF-8 declared encodings without editing")
								}
							}
						}
					}
				}
			}
		case xml.StartElement:
			if len(stack) == 0 {
				roots++
				if roots > 1 {
					return nil, errors.New("multiple XML roots")
				}
			}
			if len(stack) >= 1024 {
				return nil, errors.New("XML repair nesting limit reached")
			}
			attrs, selfClosing, err := lexXMLStart(src, before, after, t.Attr)
			if err != nil {
				return nil, err
			}
			parent := -1
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			elements = append(elements, xmlElementSpan{Name: t.Name, Start: before, OpenEnd: after, End: after, Attributes: attrs, Parent: parent, SelfClosing: selfClosing})
			stack = append(stack, len(elements)-1)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("unmatched XML close")
			}
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			elements[i].End = after
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(strings.TrimPrefix(string(t), "\ufeff")) != "" {
				return nil, errors.New("text outside XML root")
			}
		}
	}
	if roots != 1 || len(stack) != 0 {
		return nil, errors.New("incomplete XML document")
	}
	return elements, nil
}

func xmlSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func lexXMLStart(src []byte, start, end int, decoded []xml.Attr) ([]xmlAttributeSpan, bool, error) {
	if start < 0 || end > len(src) || start >= end || src[start] != '<' {
		return nil, false, errors.New("XML token offset did not identify a start tag")
	}
	i := start + 1
	for i < end && !xmlSpace(src[i]) && src[i] != '>' && src[i] != '/' {
		i++
	}
	var attrs []xmlAttributeSpan
	for i < end {
		fullStart := i
		for i < end && xmlSpace(src[i]) {
			i++
		}
		if i >= end || src[i] == '>' || src[i] == '/' {
			break
		}
		nameStart := i
		for i < end && !xmlSpace(src[i]) && src[i] != '=' {
			i++
		}
		name := string(src[nameStart:i])
		for i < end && xmlSpace(src[i]) {
			i++
		}
		if i >= end || src[i] != '=' {
			return nil, false, errors.New("invalid attribute boundary")
		}
		i++
		for i < end && xmlSpace(src[i]) {
			i++
		}
		if i >= end || (src[i] != '\'' && src[i] != '"') {
			return nil, false, errors.New("unquoted XML attribute")
		}
		quote := src[i]
		i++
		valueStart := i
		for i < end && src[i] != quote {
			i++
		}
		if i >= end {
			return nil, false, errors.New("unterminated XML attribute")
		}
		valueEnd := i
		i++
		local := name
		if colon := strings.LastIndexByte(name, ':'); colon >= 0 {
			local = name[colon+1:]
		}
		index := len(attrs)
		if index >= len(decoded) {
			return nil, false, errors.New("XML attribute offset mismatch")
		}
		attrs = append(attrs, xmlAttributeSpan{Name: name, Local: local, Value: decoded[index].Value, Start: valueStart, End: valueEnd, FullStart: fullStart, FullEnd: i, Quote: quote})
		if len(attrs) > 256 {
			return nil, false, errors.New("XML repair attribute limit reached")
		}
	}
	if len(attrs) != len(decoded) {
		return nil, false, errors.New("XML attribute count mismatch")
	}
	j := end - 2
	for j > start && xmlSpace(src[j]) {
		j--
	}
	return attrs, j > start && src[j] == '/', nil
}

func escapeXMLAttribute(value string, quote byte) []byte {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	if quote == '\'' {
		value = strings.ReplaceAll(value, "'", "&apos;")
	} else {
		value = strings.ReplaceAll(value, "\"", "&quot;")
	}
	return []byte(value)
}

func applyXMLEdits(src []byte, edits []xmlEdit) ([]byte, error) {
	if len(edits) == 0 {
		return src, nil
	}
	edits = append([]xmlEdit(nil), edits...)
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].Start == edits[j].Start {
			return edits[i].End < edits[j].End
		}
		return edits[i].Start < edits[j].Start
	})
	var out bytes.Buffer
	cursor := 0
	for _, edit := range edits {
		if edit.Start < cursor || edit.Start < 0 || edit.End < edit.Start || edit.End > len(src) {
			return nil, fmt.Errorf("overlapping or invalid XML repair spans")
		}
		out.Write(src[cursor:edit.Start])
		out.Write(edit.Replacement)
		cursor = edit.End
	}
	out.Write(src[cursor:])
	result := out.Bytes()
	if _, err := validateXML(result); err != nil {
		return nil, fmt.Errorf("edited XML did not parse: %w", err)
	}
	return result, nil
}
