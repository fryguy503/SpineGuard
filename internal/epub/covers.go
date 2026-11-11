package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"strings"
)

const maxCoverBytes = 2 << 20
const coverThumbnailSize = 320

// LoadCover reads only package metadata and the declared cover. It is not a
// validity scan and intentionally does not inspect unrelated book resources.
// The returned thumbnail is bounded and never contains active SVG content.
func LoadCover(ctx context.Context, filename string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(filename)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return "", errors.New("cover requires a regular EPUB file no larger than 2 GiB")
	}
	z, err := zip.OpenReader(filename)
	if err != nil {
		return "", err
	}
	defer z.Close()
	if len(z.File) > maxEntries {
		return "", errors.New("archive has too many entries for cover loading")
	}
	files := map[string]*zip.File{}
	var expanded uint64
	for _, file := range z.File {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !safeName(file.Name) || file.Mode()&os.ModeSymlink != 0 || files[file.Name] != nil || file.Flags&1 != 0 {
			return "", errors.New("archive contains unsafe or ambiguous resources")
		}
		if file.UncompressedSize64 > maxEntryBytes || expanded+file.UncompressedSize64 < expanded || expanded+file.UncompressedSize64 > maxExpandedBytes {
			return "", errors.New("archive exceeds cover loading limits")
		}
		if file.UncompressedSize64 > 1<<20 && (file.CompressedSize64 == 0 || file.UncompressedSize64/file.CompressedSize64 > 1000) {
			return "", errors.New("archive exceeds compression limits")
		}
		expanded += file.UncompressedSize64
		files[file.Name] = file
	}
	container, err := coverResource(ctx, files["META-INF/container.xml"], 1<<20)
	if err != nil {
		return "", fmt.Errorf("read cover metadata: %w", err)
	}
	root, err := validateXML(container)
	if err != nil || root.Local != "container" || root.Space != containerNS {
		return "", errors.New("cover container metadata is invalid")
	}
	var c struct {
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := decoder(container).Decode(&c); err != nil {
		return "", err
	}
	opf := ""
	for _, rootfile := range c.Rootfiles {
		if rootfile.MediaType == "application/oebps-package+xml" {
			opf = rootfile.FullPath
			break
		}
	}
	if !safeName(opf) {
		return "", errors.New("cover package path is invalid")
	}
	metadata, err := coverResource(ctx, files[opf], maxTextBytes)
	if err != nil {
		return "", fmt.Errorf("read cover package: %w", err)
	}
	root, err = validateXML(metadata)
	if err != nil || root != (xml.Name{Space: opfNS, Local: "package"}) {
		return "", errors.New("cover package metadata is invalid")
	}
	var pkg packageDoc
	if err := decoder(metadata).Decode(&pkg); err != nil {
		return "", err
	}
	candidates := map[string]bool{}
	manifest := map[string]manifestItem{}
	for _, item := range pkg.Manifest {
		if _, duplicate := manifest[item.ID]; duplicate {
			return "", errors.New("cover manifest identifiers are ambiguous")
		}
		manifest[item.ID] = item
		if tokenHas(item.Properties, "cover-image") {
			target, _, external, err := resolveReference(opf, item.Href)
			if err != nil || external || !strings.HasPrefix(item.MediaType, "image/") {
				return "", errors.New("declared cover is not a local image")
			}
			candidates[target] = true
		}
	}
	if len(candidates) == 0 {
		for _, meta := range pkg.Metadata.Meta {
			if meta.XMLName.Space != opfNS || meta.Name != "cover" {
				continue
			}
			item, exists := manifest[meta.Content]
			if !exists || !strings.HasPrefix(item.MediaType, "image/") {
				continue
			}
			target, _, external, err := resolveReference(opf, item.Href)
			if err == nil && !external {
				candidates[target] = true
			}
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	if len(candidates) != 1 {
		return "", errors.New("multiple cover images are declared")
	}
	for name := range candidates {
		return coverThumbnail(ctx, files[name])
	}
	return "", nil
}

func coverResource(ctx context.Context, file *zip.File, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if file == nil {
		return nil, os.ErrNotExist
	}
	if file.UncompressedSize64 > uint64(maxBytes) {
		return nil, errors.New("cover resource exceeds size limit")
	}
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(contextReader{ctx, io.LimitReader(r, maxBytes+1)})
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, errors.New("cover resource exceeds size limit")
	}
	return b, nil
}

func coverThumbnail(ctx context.Context, file *zip.File) (string, error) {
	if file == nil {
		return "", nil
	}
	b, err := coverResource(ctx, file, maxCoverBytes)
	if err != nil {
		return "", err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 6000 || cfg.Height > 6000 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		return "", errors.New("cover image is invalid or exceeds pixel limits")
	}
	if format != "jpeg" && format != "png" && format != "gif" {
		return "", errors.New("cover image format is unsupported")
	}
	source, _, err := image.Decode(contextReader{ctx, bytes.NewReader(b)})
	if err != nil {
		return "", err
	}
	w, h := cfg.Width, cfg.Height
	if w > coverThumbnailSize || h > coverThumbnailSize {
		if w >= h {
			h = max(1, h*coverThumbnailSize/w)
			w = coverThumbnailSize
		} else {
			w = max(1, w*coverThumbnailSize/h)
			h = coverThumbnailSize
		}
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	bounds := source.Bounds()
	for y := 0; y < h; y++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		for x := 0; x < w; x++ {
			r, g, b, a := source.At(bounds.Min.X+x*cfg.Width/w, bounds.Min.Y+y*cfg.Height/h).RGBA()
			// Composite transparent cover pixels onto white before JPEG encoding.
			thumb.SetRGBA(x, y, color.RGBA{uint8((r + 0xffff - a) >> 8), uint8((g + 0xffff - a) >> 8), uint8((b + 0xffff - a) >> 8), 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, thumb, &jpeg.Options{Quality: 80}); err != nil {
		return "", err
	}
	if encoded.Len() > 128<<10 {
		return "", errors.New("cover thumbnail exceeds size limit")
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()), nil
}
