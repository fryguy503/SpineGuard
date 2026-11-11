package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
)

func coverFixture(t *testing.T, width, height int, legacy bool) []fixtureEntry {
	t.Helper()
	entries := fixtureEntries()
	properties := ` properties="cover-image"`
	if legacy {
		properties = ""
		entries[2].body = strings.Replace(entries[2].body, `</metadata>`, `<meta name="cover" content="cover"/></metadata>`, 1)
	}
	entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="cover" href="cover.png" media-type="image/png"`+properties+`/></manifest>`, 1)
	pixels := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixels.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 85, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, pixels); err != nil {
		t.Fatal(err)
	}
	return append(entries, fixtureEntry{"OPS/cover.png", data.String(), zip.Deflate})
}

func TestCoverThumbnailsAndMetadataOnlyReload(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		entries := coverFixture(t, 640, 960, legacy)
		book := writeFixture(t, entries)
		before, _ := os.ReadFile(book)
		data, err := LoadCover(context.Background(), book)
		if err != nil || !strings.HasPrefix(data, "data:image/jpeg;base64,") {
			t.Fatalf("cover load: %q, %v", data, err)
		}
		encoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, "data:image/jpeg;base64,"))
		if err != nil || len(encoded) > 128<<10 {
			t.Fatalf("thumbnail payload not bounded: %d,%v", len(encoded), err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(encoded))
		if err != nil || cfg.Height != coverThumbnailSize || cfg.Width >= coverThumbnailSize {
			t.Fatalf("thumbnail aspect ratio/bounds: %+v, %v", cfg, err)
		}
		report, err := Scan(context.Background(), book, Options{})
		if err != nil || report.CoverData != data {
			t.Fatalf("scan/lazy thumbnail mismatch: %v", err)
		}
		after, _ := os.ReadFile(book)
		if !bytes.Equal(before, after) {
			t.Fatal("cover loading modified the source book")
		}
	}
}

func TestCoverLoadDoesNotReadUnrelatedResources(t *testing.T) {
	entries := coverFixture(t, 20, 30, false)
	entries[3].method = zip.Store
	book := writeFixture(t, entries)
	data, _ := os.ReadFile(book)
	index := bytes.Index(data, []byte(entries[3].body))
	if index < 0 {
		t.Fatal("chapter payload not found")
	}
	data[index+20] ^= 1 // Unrelated resource now has an invalid ZIP CRC.
	if err := os.WriteFile(book, data, 0600); err != nil {
		t.Fatal(err)
	}
	if cover, err := LoadCover(context.Background(), book); err != nil || cover == "" {
		t.Fatalf("cover request inspected unrelated chapter: %v", err)
	}
	report, err := Scan(context.Background(), book, Options{})
	if err != nil || !hasIssue(report, "ZIP_CRC") {
		t.Fatalf("fixture did not contain chapter corruption: %+v, %v", report.Issues, err)
	}
}

func TestCoverLoadingRejectsUnsafeOrUnusableSources(t *testing.T) {
	for _, tc := range []struct {
		name      string
		edit      func([]fixtureEntry) []fixtureEntry
		wantError bool
	}{
		{"missing declared image", func(entries []fixtureEntry) []fixtureEntry { return entries[:len(entries)-1] }, false},
		{"remote image", func(entries []fixtureEntry) []fixtureEntry {
			entries[2].body = strings.Replace(entries[2].body, `href="cover.png"`, `href="https://example.invalid/cover.png"`, 1)
			return entries
		}, true},
		{"unsafe archive path", func(entries []fixtureEntry) []fixtureEntry {
			return append(entries, fixtureEntry{"../escape.png", "x", zip.Store})
		}, true},
		{"duplicate resource", func(entries []fixtureEntry) []fixtureEntry { return append(entries, entries[len(entries)-1]) }, true},
		{"multiple declared covers", func(entries []fixtureEntry) []fixtureEntry {
			entries[2].body = strings.Replace(entries[2].body, "</manifest>", `<item id="cover2" href="other.png" media-type="image/png" properties="cover-image"/></manifest>`, 1)
			return entries
		}, true},
		{"active SVG not rendered", func(entries []fixtureEntry) []fixtureEntry {
			entries[len(entries)-1].body = `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`
			return entries
		}, true},
		{"pixel bomb rejected before decode", func(entries []fixtureEntry) []fixtureEntry {
			b := []byte(entries[len(entries)-1].body)
			binary.BigEndian.PutUint32(b[16:20], 5000)
			binary.BigEndian.PutUint32(b[20:24], 5000)
			binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
			entries[len(entries)-1].body = string(b)
			return entries
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cover, err := LoadCover(context.Background(), writeFixture(t, tc.edit(coverFixture(t, 2, 3, false))))
			if cover != "" || (tc.wantError && err == nil) {
				t.Fatalf("unexpected cover=%q,error=%v", cover, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadCover(ctx, "not-read.epub"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cover request ignored cancellation: %v", err)
	}
	if data, err := LoadCover(context.Background(), writeFixture(t, fixtureEntries())); err != nil || data != "" {
		t.Fatalf("no-cover book should use a placeholder: %q,%v", data, err)
	}
}
