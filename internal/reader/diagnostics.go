package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

func (s *session) operation(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}
func (m *Manager) CancelSearch(id string) {
	if s, e := m.get(id); e == nil {
		s.searchMu.Lock()
		s.searchIntent++
		if s.searchCancel != nil {
			s.searchCancel()
			s.searchCancel = nil
		}
		s.searchMu.Unlock()
	}
}
func (s *session) searchOperation(parent context.Context) (context.Context, func()) {
	ctx, done := s.operation(parent)
	ctx, cancel := context.WithCancel(ctx)
	s.searchMu.Lock()
	s.searchIntent++
	intent := s.searchIntent
	if s.searchCancel != nil {
		s.searchCancel()
	}
	s.searchCancel = cancel
	s.searchMu.Unlock()
	return ctx, func() {
		cancel()
		done()
		s.searchMu.Lock()
		if intent == s.searchIntent {
			s.searchCancel = nil
		}
		s.searchMu.Unlock()
	}
}

// XML byte-order marks are decoded before inspection. No external entities or
// external character sets are loaded; charset uses its bundled tables.
func sourceText(data []byte) string {
	label := ""
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) {
		label = "utf-16le"
		data = data[2:]
	} else if bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		label = "utf-16be"
		data = data[2:]
	}
	if label == "" {
		prefix := data
		if len(prefix) > 512 {
			prefix = prefix[:512]
		}
		if match := xmlEncodingPattern.FindSubmatch(prefix); len(match) == 2 {
			label = string(match[1])
		}
	}
	if label != "" {
		if r, e := charset.NewReaderLabel(label, bytes.NewReader(data)); e == nil {
			if b, e := io.ReadAll(io.LimitReader(r, maxText*2)); e == nil {
				return string(b)
			}
		}
	}
	return strings.ToValidUTF8(strings.TrimPrefix(string(data), "\ufeff"), "�")
}
func bodyText(ctx context.Context, src []byte) (string, error) {
	doc, e := parseReaderHTML(ctx, src)
	if e != nil {
		return "", e
	}
	var body *xhtml.Node
	nodes := []*xhtml.Node{doc}
	for len(nodes) > 0 {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		n := nodes[len(nodes)-1]
		nodes = nodes[:len(nodes)-1]
		if n.Type == xhtml.ElementNode && n.Data == "body" {
			body = n
			break
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			nodes = append(nodes, c)
		}
	}
	if body == nil {
		return "", nil
	}
	type visit struct {
		node *xhtml.Node
		exit bool
	}
	stack := []visit{{node: body}}
	var b strings.Builder
	for count := 0; len(stack) > 0; count++ {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		if count > maxHTMLNodes*2 {
			return "", errors.New("search document exceeds node limit")
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := v.node
		if v.exit {
			b.WriteByte(' ')
			continue
		}
		if n.Type == xhtml.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "template" || n.Data == "noscript") {
			continue
		}
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == xhtml.ElementNode && textBoundary(n.Data) {
			b.WriteByte(' ')
			stack = append(stack, visit{node: n, exit: true})
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, visit{node: c})
		}
	}
	return strings.Join(strings.Fields(b.String()), " "), nil
}
func textBoundary(tag string) bool {
	switch tag {
	case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6", "li", "dt", "dd", "section", "article", "table", "tr", "td", "th", "blockquote", "pre":
		return true
	}
	return false
}
func (m *Manager) Search(parent context.Context, id, query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 || len(query) > 200 {
		return nil, errors.New("enter between 2 and 200 characters to search")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	s, e := m.get(id)
	if e != nil {
		return nil, e
	}
	ctx, done := s.searchOperation(parent)
	defer done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.check(); e != nil {
		return nil, e
	}
	re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
	out := []SearchResult{}
	scanned := int64(0)
	resources := append([]Resource{}, s.book.Spine...)
	seen := map[string]bool{}
	for _, r := range resources {
		seen[r.ID] = true
	}
	for _, r := range s.book.Resources {
		if !seen[r.ID] {
			resources = append(resources, r)
		}
	}
	for _, r := range resources {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if !r.Renderable || r.Missing || s.blocked[r.Path] || r.MediaType == "image/svg+xml" {
			continue
		}
		f := s.files[r.Path]
		if f == nil || f.UncompressedSize64 > maxText {
			continue
		}
		scanned += int64(f.UncompressedSize64)
		if scanned > 128<<20 {
			break
		}
		data, err := s.read(ctx, r.Path, maxText)
		if err != nil {
			continue
		}
		text, err := bodyText(ctx, data)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for i, match := range re.FindAllStringIndex(text, limit-len(out)) {
			a, b := match[0]-100, match[1]+120
			if a < 0 {
				a = 0
			}
			if b > len(text) {
				b = len(text)
			}
			for a > 0 && !utf8.RuneStart(text[a]) {
				a--
			}
			for b < len(text) && !utf8.RuneStart(text[b]) {
				b++
			}
			excerpt := text[a:b]
			if a > 0 {
				excerpt = "…" + excerpt
			}
			if b < len(text) {
				excerpt += "…"
			}
			out = append(out, SearchResult{ResourceID: r.ID, Href: r.URL, Path: r.Path, Excerpt: excerpt, Text: text[match[0]:match[1]], MatchIndex: i})
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (m *Manager) LocateIssue(parent context.Context, id string, index int) (Location, error) {
	s, e := m.get(id)
	if e != nil {
		return Location{}, e
	}
	ctx, done := s.operation(parent)
	defer done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.check(); e != nil {
		return Location{}, e
	}
	if e = ctx.Err(); e != nil {
		return Location{}, e
	}
	if index < 0 || index >= len(s.book.Issues) {
		return Location{}, errors.New("finding is unavailable")
	}
	issue := s.book.Issues[index]
	loc := Location{Mode: "details", Message: issue.Detail, Line: issue.Line, Column: 1, Reference: issue.Reference, Fragment: issue.Anchor}
	if strings.HasPrefix(issue.Code, "LINK_") || issue.Code == "RESOURCE_REMOTE" {
		loc.Fragment = ""
	}
	name := strings.TrimPrefix(issue.File, "./")
	if target, fragment, ok := resolve("root", name); ok {
		name = target
		if loc.Fragment == "" {
			loc.Fragment = fragment
		}
	}
	r := s.byPath[name]
	if r == nil || r.Missing || s.blocked[name] {
		loc.Message = "The original file for this finding is unavailable. " + issue.Detail
		return loc, nil
	}
	loc.ResourceID = r.ID
	loc.Href = r.URL
	loc.Mode = "source"
	data, e := s.read(ctx, name, maxText)
	if e != nil {
		loc.Message = e.Error()
		return loc, nil
	}
	text := sourceText(data)
	if loc.Reference == "" && issue.Code == "RESOURCE_REMOTE" {
		loc.Reference = strings.TrimSpace(issue.Detail)
	}
	if loc.Reference == "" && strings.HasPrefix(issue.Code, "LINK_") {
		if p := strings.Index(issue.Detail, " resolves to "); p > 0 {
			loc.Reference = strings.TrimSpace(issue.Detail[:p])
		} else if strings.HasPrefix(issue.Detail, "Anchor was not found: ") {
			loc.Reference = strings.TrimSpace(strings.TrimPrefix(issue.Detail, "Anchor was not found: "))
		} else if issue.Code == "LINK_FRAGMENT" || issue.Code == "LINK_REMOTE" {
			loc.Reference = strings.TrimSpace(issue.Detail)
		} else if issue.Code == "LINK_PATH" {
			if p := strings.LastIndex(issue.Detail, ": "); p > 0 {
				loc.Reference = strings.TrimSpace(issue.Detail[:p])
			}
		}
	}
	if loc.Fragment == "" && issue.Code == "CONTENT_DUPLICATE_ID" {
		if p := strings.LastIndex(issue.Detail, ": "); p >= 0 {
			loc.Fragment = strings.TrimSpace(issue.Detail[p+2:])
		}
	}
	offset := -1
	if loc.Reference != "" {
		offset = attributeOffset(text, loc.Reference, false)
	}
	if offset < 0 && loc.Fragment != "" {
		offset = attributeOffset(text, loc.Fragment, true)
	}
	if offset < 0 && issue.Line > 0 {
		offset = lineOffset(text, issue.Line)
	}
	if offset < 0 {
		needle := ""
		switch issue.Code {
		case "METADATA_LANGUAGE":
			needle = "</metadata"
		case "METADATA_IDENTIFIER":
			needle = "<package"
		case "CONTENT_SCRIPT":
			needle = "<script"
		}
		if needle != "" {
			offset = strings.Index(strings.ToLower(text), needle)
		}
	}
	if offset >= 0 {
		loc.Line = strings.Count(text[:offset], "\n") + 1
		last := strings.LastIndexByte(text[:offset], '\n')
		loc.Column = utf8.RuneCountInString(text[last+1:offset]) + 1
	}
	if loc.Line < 1 {
		loc.Line = 1
	}
	// Without a proven element mapping, a reported source line is more precise
	// than the beginning of a rendered chapter (including EPUBCheck locations).
	sourceLineOnly := issue.Line > 0 && loc.Reference == "" && loc.Fragment == ""
	if r.Renderable && !sourceLineOnly && issue.Code != "CONTENT_SCRIPT" && !strings.HasPrefix(issue.Code, "XML_") {
		loc.Mode = "page"
		loc.Message = "Preview shows a read-only, compatibility-safe rendering. Source shows the original file."
		if loc.Fragment != "" {
			loc.Href += "#" + urlFragment(loc.Fragment)
		}
	}
	return loc, nil
}
func attributeOffset(src, value string, identifier bool) int {
	z := xhtml.NewTokenizer(strings.NewReader(src))
	offset := 0
	for {
		t := z.Next()
		raw := z.Raw()
		if t == xhtml.ErrorToken {
			return -1
		}
		start := offset
		offset += len(raw)
		if t != xhtml.StartTagToken && t != xhtml.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		for _, a := range tok.Attr {
			ok := a.Key == "href" || a.Key == "src" || a.Key == "poster" || a.Key == "data"
			if identifier {
				ok = a.Key == "id" || a.Key == "name"
			}
			if ok && a.Val == value {
				pos := bytes.Index(raw, []byte(value))
				if pos < 0 {
					pos = 0
				}
				return start + pos
			}
		}
	}
}
func lineOffset(text string, line int) int {
	if line < 1 {
		return 0
	}
	offset := 0
	for i := 1; i < line; i++ {
		p := strings.IndexByte(text[offset:], '\n')
		if p < 0 {
			return len(text)
		}
		offset += p + 1
	}
	return offset
}

func (m *Manager) Source(parent context.Context, id, resourceID string, line, column, maxLines int) (Source, error) {
	s, e := m.get(id)
	if e != nil {
		return Source{}, e
	}
	ctx, done := s.operation(parent)
	defer done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.check(); e != nil {
		return Source{}, e
	}
	r := s.resources[resourceID]
	if r == nil {
		return Source{}, errors.New("file is unavailable")
	}
	media := r.MediaType
	if !(r.Renderable || strings.Contains(media, "xml") || media == "text/css" || strings.HasPrefix(media, "text/") || strings.Contains(media, "javascript") || media == "application/json" || strings.EqualFold(path.Base(r.Path), "mimetype")) {
		return Source{}, errors.New("this binary resource has no text source")
	}
	data, e := s.read(ctx, r.Path, maxText)
	if e != nil {
		return Source{}, fmt.Errorf("source cannot be read: %w", e)
	}
	if e = ctx.Err(); e != nil {
		return Source{}, e
	}
	text := sourceText(data)
	total := strings.Count(text, "\n") + 1
	if line < 1 {
		line = 1
	}
	if line > total {
		line = total
	}
	if column < 1 {
		column = 1
	}
	if maxLines < 1 || maxLines > 200 {
		maxLines = 200
	}
	startLine := line - maxLines/2
	if startLine < 1 {
		startLine = 1
	}
	start := lineOffset(text, startLine)
	end := lineOffset(text, startLine+maxLines)
	if end < start {
		end = len(text)
	}
	target := lineOffset(text, line)
	lineEnd := strings.IndexByte(text[target:], '\n')
	if lineEnd < 0 {
		lineEnd = len(text) - target
	}
	target += byteColumn(text[target:target+lineEnd], column)
	const budget = 128 << 10
	startColumn := 1
	if end-start > budget {
		windowStart, windowEnd := start, end
		start = target - budget/2
		if start < windowStart {
			start = windowStart
		}
		end = start + budget
		if end > windowEnd {
			end = windowEnd
			start = end - budget
			if start < windowStart {
				start = windowStart
			}
		}
		for start < len(text) && !utf8.RuneStart(text[start]) {
			start++
		}
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		startLine = strings.Count(text[:start], "\n") + 1
		last := strings.LastIndexByte(text[:start], '\n')
		startColumn = utf8.RuneCountInString(text[last+1:start]) + 1
	}
	return Source{Path: r.Path, Content: text[start:end], StartLine: startLine, StartColumn: startColumn, TotalLines: total, Truncated: start > 0 || end < len(text)}, nil
}
func byteColumn(line string, column int) int {
	for i := range line {
		column--
		if column <= 0 {
			return i
		}
	}
	return len(line)
}
