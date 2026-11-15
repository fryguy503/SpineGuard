package epub

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	repairADETemplateNS = "http://ns.adobe.com/2006/ade"
	repairFOLayoutNS    = "http://www.w3.org/1999/XSL/Format"
)

var repairTemplateLength = regexp.MustCompile(`^(?:0|(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:pt|px|pc|cm|mm|in|em|ex|%))$`)

// repairPageTemplateStyles inspects a deliberately small passive XPGT subset.
// It returns selector-only CSS for the existing per-identifier dependency proof;
// the original template is never rewritten or treated as an ordinary stylesheet.
// Every other accepted value is a literal layout scalar or an internal layout
// master name, whose spelling does not select identifiers in book content.
func repairPageTemplateStyles(ctx context.Context, src []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(src) > 1<<20 {
		return nil, errors.New("page template exceeds the inspection size limit")
	}
	// This also establishes lossless UTF-8 XML and bounded structural parsing.
	elements, err := parseEditableXML(ctx, src)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(elements) > 20000 {
		return nil, errors.New("page template XML could not be safely inspected")
	}
	d := xml.NewDecoder(bytes.NewReader(src))
	stack := []xml.Name{}
	seenRoot, seenDeclaration := false, false
	masters := map[string]bool{}
	masterReferences := []string{}
	var css strings.Builder
	for tokens := 0; ; tokens++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tokens > 100000 {
			return nil, errors.New("page template exceeds the inspection work limit")
		}
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("page template XML could not be safely inspected")
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, errors.New("page template declarations require manual dependency review")
		case xml.ProcInst:
			if t.Target != "xml" || seenRoot || seenDeclaration {
				return nil, errors.New("page template processing instructions require manual review")
			}
			seenDeclaration = true
		case xml.CharData:
			text := string(t)
			if !seenRoot {
				text = strings.TrimPrefix(text, "\ufeff")
			}
			if strings.TrimSpace(text) != "" {
				return nil, errors.New("page template contains unsupported text content")
			}
		case xml.StartElement:
			if len(stack) >= 64 {
				return nil, errors.New("page template nesting exceeds the inspection limit")
			}
			parent := xml.Name{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			if !repairTemplateChild(parent, t.Name) || len(stack) == 0 && seenRoot {
				return nil, errors.New("page template uses an unsupported element or hierarchy")
			}
			seenRoot = true
			values := map[string]string{}
			seen := map[xml.Name]bool{}
			for _, at := range t.Attr {
				if seen[at.Name] {
					return nil, errors.New("page template has ambiguous duplicate attributes")
				}
				seen[at.Name] = true
				if at.Name.Space == "xmlns" || at.Name.Space == "" && at.Name.Local == "xmlns" {
					if at.Value != repairADETemplateNS && at.Value != repairFOLayoutNS && at.Value != xhtmlNS {
						return nil, errors.New("page template declares an unsupported namespace")
					}
					continue
				}
				if !repairTemplateAttribute(t.Name, at) {
					return nil, errors.New("page template uses an unsupported or dynamic attribute")
				}
				values[at.Name.Space+"\x00"+at.Name.Local] = at.Value
			}
			value := func(name string) string { return values["\x00"+name] }
			if t.Name.Space == repairFOLayoutNS && t.Name.Local == "simple-page-master" {
				name := value("master-name")
				if name == "" || masters[name] {
					return nil, errors.New("page template layout masters are ambiguous")
				}
				masters[name] = true
			}
			if t.Name.Space == repairFOLayoutNS && t.Name.Local == "conditional-page-master-reference" {
				if value("master-reference") == "" {
					return nil, errors.New("page template layout reference is missing")
				}
				masterReferences = append(masterReferences, value("master-reference"))
			}
			if t.Name.Space == repairADETemplateNS && t.Name.Local == "styling-rule" {
				selector := strings.TrimSpace(value("selector"))
				if !repairTemplateSelector(selector) {
					return nil, errors.New("page template selector requires manual dependency review")
				}
				css.WriteString(selector)
				css.WriteString("{}\n")
			}
			stack = append(stack, t.Name)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != t.Name {
				return nil, errors.New("page template XML nesting is ambiguous")
			}
			stack = stack[:len(stack)-1]
		}
	}
	if !seenRoot || len(stack) != 0 {
		return nil, errors.New("page template XML is incomplete")
	}
	for _, name := range masterReferences {
		if !masters[name] {
			return nil, errors.New("page template uses an unknown layout master")
		}
	}
	return []byte(css.String()), nil
}

func repairTemplateChild(parent, child xml.Name) bool {
	ade := func(local string) bool { return child.Space == repairADETemplateNS && child.Local == local }
	fo := func(names string) bool { return child.Space == repairFOLayoutNS && tokenHas(names, child.Local) }
	if parent == (xml.Name{}) {
		return ade("template")
	}
	if parent.Space == repairADETemplateNS {
		switch parent.Local {
		case "template":
			return ade("style") || fo("layout-master-set")
		case "style":
			return ade("styling-rule")
		}
	}
	if parent.Space == repairFOLayoutNS {
		switch parent.Local {
		case "layout-master-set":
			return fo("simple-page-master page-sequence-master")
		case "simple-page-master":
			return fo("region-body region-before region-after region-start region-end")
		case "page-sequence-master":
			return fo("repeatable-page-master-alternatives")
		case "repeatable-page-master-alternatives":
			return fo("conditional-page-master-reference")
		}
	}
	return false
}

func repairTemplateAttribute(element xml.Name, at xml.Attr) bool {
	if at.Name.Space == repairADETemplateNS {
		return element.Space == repairFOLayoutNS && element.Local == "conditional-page-master-reference" &&
			tokenHas("min-page-width max-page-width min-page-height max-page-height", at.Name.Local) && repairTemplateLength.MatchString(at.Value)
	}
	if at.Name.Space != "" {
		return false
	}
	name, value := at.Name.Local, at.Value
	if element.Space == repairADETemplateNS {
		if element.Local != "styling-rule" {
			return false
		}
		switch name {
		case "selector":
			return len(value) > 0 && len(value) <= 4096
		case "display":
			return tokenHas("adobe-other-region block inline none", value)
		case "adobe-region":
			return tokenHas("xsl-region-before xsl-region-after xsl-region-start xsl-region-end xsl-region-body", value)
		}
		return false
	}
	if element.Space != repairFOLayoutNS {
		return false
	}
	switch element.Local {
	case "simple-page-master", "page-sequence-master":
		if name == "master-name" {
			return validXMLIdentifier(value)
		}
		return element.Local == "simple-page-master" && tokenHas("page-width page-height margin margin-top margin-bottom margin-left margin-right", name) && repairTemplateLength.MatchString(value)
	case "region-body":
		if name == "column-count" {
			n, err := strconv.Atoi(value)
			return err == nil && n >= 1 && n <= 16 && strconv.Itoa(n) == value
		}
		return tokenHas("column-gap margin margin-top margin-bottom margin-left margin-right", name) && repairTemplateLength.MatchString(value)
	case "region-before", "region-after", "region-start", "region-end":
		if name == "precedence" {
			return value == "true" || value == "false"
		}
		return name == "extent" && repairTemplateLength.MatchString(value)
	case "repeatable-page-master-alternatives":
		if name != "maximum-repeats" {
			return false
		}
		n, err := strconv.Atoi(value)
		return value == "no-limit" || err == nil && n >= 1 && n <= 10000 && strconv.Itoa(n) == value
	case "conditional-page-master-reference":
		switch name {
		case "master-reference":
			return validXMLIdentifier(value)
		case "page-position":
			return tokenHas("first last rest only any", value)
		case "odd-or-even":
			return tokenHas("odd even any", value)
		case "blank-or-not-blank":
			return tokenHas("blank not-blank any", value)
		}
	}
	return false
}

// Only type, class, ID and universal selectors with simple combinators are
// accepted. Attribute/pseudo/namespace selectors and functions remain manual.
func repairTemplateSelector(selector string) bool {
	if selector == "" || len(selector) > 4096 {
		return false
	}
	src := []byte(selector)
	needCompound, allowType := true, true
	for i := 0; i < len(src); {
		if cssWhitespace(src[i]) {
			for i < len(src) && cssWhitespace(src[i]) {
				i++
			}
			if !needCompound {
				allowType = true
			}
			continue
		}
		if src[i] == ',' || src[i] == '>' || src[i] == '+' || src[i] == '~' {
			if needCompound {
				return false
			}
			needCompound, allowType = true, true
			i++
			continue
		}
		if src[i] == '*' {
			if !allowType {
				return false
			}
			i++
		} else {
			if src[i] == '.' || src[i] == '#' {
				i++
			} else if !allowType {
				return false
			}
			if i >= len(src) || !cssNameStart(src[i]) || src[i] >= '0' && src[i] <= '9' {
				return false
			}
			name, next, ok := cssReadName(src, i)
			if !ok || name == "" || name == "-" {
				return false
			}
			i = next
		}
		needCompound, allowType = false, false
	}
	return !needCompound
}
