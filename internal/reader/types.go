// Package reader provides a read-only, bounded EPUB presentation layer. It
// never serves application assets or writes changes back into a publication.
package reader

import "github.com/fryguy503/SpineGuard/internal/epub"

type Resource struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	URL        string `json:"url"`
	MediaType  string `json:"mediaType"`
	Title      string `json:"title"`
	SpineIndex int    `json:"spineIndex"`
	Missing    bool   `json:"missing"`
	Renderable bool   `json:"renderable"`
}
type TOCEntry struct {
	Label      string     `json:"label"`
	Href       string     `json:"href"`
	ResourceID string     `json:"resourceId"`
	Fragment   string     `json:"fragment"`
	Missing    bool       `json:"missing"`
	Children   []TOCEntry `json:"children"`
}
type Bookmark struct {
	ID        string `json:"id"`
	Location  string `json:"location"`
	Label     string `json:"label"`
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
}
type ReadingState struct {
	Location  string     `json:"location"`
	Bookmarks []Bookmark `json:"bookmarks"`
}
type Book struct {
	SessionID  string       `json:"sessionId"`
	BookKey    string       `json:"bookKey"`
	Title      string       `json:"title"`
	Author     string       `json:"author"`
	PackageURL string       `json:"packageUrl"`
	Spine      []Resource   `json:"spine"`
	Resources  []Resource   `json:"resources"`
	TOC        []TOCEntry   `json:"toc"`
	Issues     []epub.Issue `json:"issues"`
	Warnings   []string     `json:"warnings"`
	State      ReadingState `json:"state"`
	Direction  string       `json:"direction"`
	Layout     string       `json:"layout"`
}
type SearchResult struct {
	ResourceID string `json:"resourceId"`
	Href       string `json:"href"`
	Path       string `json:"path"`
	Excerpt    string `json:"excerpt"`
	Text       string `json:"text"`
	Line       int    `json:"line"`
	MatchIndex int    `json:"matchIndex"`
}
type Location struct {
	ResourceID string `json:"resourceId"`
	Href       string `json:"href"`
	Fragment   string `json:"fragment"`
	Reference  string `json:"reference"`
	Text       string `json:"text"`
	Mode       string `json:"mode"`
	Message    string `json:"message"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
}
type Source struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	TotalLines  int    `json:"totalLines"`
	Truncated   bool   `json:"truncated"`
}
