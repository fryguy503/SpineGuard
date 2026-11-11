package epub

import (
	"strings"
	"testing"
)

func TestScanDeclaredSeriesMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, version, metadata, series, index string
	}{
		{"EPUB2 calibre fractional", "2.0", `<meta name="calibre:series" content="The Expanse"/><meta name="calibre:series_index" content="0.5"/>`, "The Expanse", "0.5"},
		{"EPUB3 typed collection", "3.0", `<meta property="belongs-to-collection" id="set">A collection</meta><meta refines="#set" property="collection-type">set</meta><meta property="belongs-to-collection" id="series">The Expanse</meta><meta refines="#series" property="collection-type">series</meta><meta refines="#series" property="group-position"> 2.5 </meta>`, "The Expanse", "2.5"},
		{"EPUB3 takes precedence", "3.0", `<meta property="belongs-to-collection" id="s">Declared series</meta><meta refines="#s" property="collection-type">series</meta><meta refines="#s" property="group-position">Volume IV</meta><meta name="calibre:series" content="Other series"/>`, "Declared series", "Volume IV"},
		{"untyped is not a series", "3.0", `<meta property="belongs-to-collection" id="s">An anthology</meta><meta refines="#s" property="group-position">2</meta>`, "", ""},
		{"calibre fallback", "3.0", `<meta property="belongs-to-collection" id="s">An anthology</meta><meta refines="#s" property="collection-type">set</meta><meta name="calibre:series" content="Separate series"/>`, "Separate series", ""},
		{"ambiguous series not guessed", "3.0", `<meta property="belongs-to-collection" id="a">First</meta><meta refines="#a" property="collection-type">series</meta><meta property="belongs-to-collection" id="b">Second</meta><meta refines="#b" property="collection-type">series</meta><meta name="calibre:series" content="Fallback"/>`, "", ""},
		{"conflicting calibre series", "2.0", `<meta name="calibre:series" content="First"/><meta name="calibre:series" content="Second"/>`, "", ""},
		{"conflicting position omitted", "3.0", `<meta property="belongs-to-collection" id="s">Series</meta><meta refines="#s" property="collection-type">series</meta><meta refines="#s" property="group-position">1</meta><meta refines="#s" property="group-position">2</meta>`, "Series", ""},
		{"foreign metadata ignored", "3.0", `<meta xmlns="urn:foreign" name="calibre:series" content="Foreign"/>`, "", ""},
		{"orphaned position ignored", "3.0", `<meta name="calibre:series" content="Series"/><meta property="group-position" refines="#unknown">20</meta>`, "Series", ""},
		{"duplicate collection ID ignored", "3.0", `<meta property="belongs-to-collection" id="s">Series</meta><meta property="belongs-to-collection" id="s">Other</meta><meta refines="#s" property="collection-type">series</meta>`, "", ""},
		{"index without series ignored", "2.0", `<meta name="calibre:series_index" content="3"/>`, "", ""},
		{"missing series stays empty", "3.0", ``, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := fixtureEntries()
			entries[2].body = strings.Replace(entries[2].body, `version="3.0"`, `version="`+tc.version+`"`, 1)
			entries[2].body = strings.Replace(entries[2].body, "</metadata>", tc.metadata+"</metadata>", 1)
			report := scanFixture(t, entries)
			if report.Series != tc.series || report.SeriesIndex != tc.index {
				t.Fatalf("series=(%q,%q), want (%q,%q)", report.Series, report.SeriesIndex, tc.series, tc.index)
			}
		})
	}
}
