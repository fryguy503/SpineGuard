package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	maxEntries         = 20000
	maxEntryBytes      = 64 << 20
	maxExpandedBytes   = 1 << 30
	maxTextBytes       = 16 << 20
	maxCachedTextBytes = 128 << 20
	maxIssues          = 2000
)

type archive struct {
	z          *zip.ReadCloser
	files      map[string]*zip.File
	text       map[string][]byte
	unsafe     bool
	protected  bool
	obfuscated bool
	ctx        context.Context
	report     *Report
}

func (a *archive) issue(code, severity, title, detail, file string, repair bool) {
	if len(a.report.Issues) >= maxIssues {
		if len(a.report.Issues) == maxIssues {
			a.report.Issues = append(a.report.Issues, Issue{Code: "SCAN_TRUNCATED", Severity: "warning", Title: "Issue display limit reached", Detail: "Additional findings are omitted after 2,000 issues. Fix these findings and scan again."})
		}
		return
	}
	a.report.Issues = append(a.report.Issues, Issue{Code: code, Severity: severity, Title: title, Detail: detail, File: file, Repairable: repair})
}

func safeName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00:") {
		return false
	}
	trim := strings.TrimSuffix(name, "/")
	return trim != "" && trim != "." && path.Clean(trim) == trim && trim != ".." && !strings.HasPrefix(trim, "../")
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func textResource(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".xml", ".opf", ".xhtml", ".html", ".htm", ".ncx", ".svg", ".css":
		return true
	}
	return false
}

func openArchive(ctx context.Context, filename string, r *Report) (*archive, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	a := &archive{z: z, files: map[string]*zip.File{}, text: map[string][]byte{}, ctx: ctx, report: r}
	if len(z.File) > maxEntries {
		a.issue("ZIP_ENTRY_LIMIT", "error", "Archive has too many entries", fmt.Sprintf("More than %d entries; scanning stopped to protect memory.", maxEntries), "", false)
		a.unsafe = true
		return a, nil
	}
	folded := map[string]string{}
	var total, cached uint64
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			z.Close()
			return nil, err
		}
		if !safeName(f.Name) {
			a.issue("ZIP_UNSAFE_PATH", "error", "Unsafe archive path", "Paths must be relative, canonical EPUB paths without drive letters or backslashes.", f.Name, false)
			a.unsafe = true
			continue
		}
		if _, ok := a.files[f.Name]; ok {
			a.issue("ZIP_DUPLICATE", "error", "Duplicate archive entry", "Duplicate names make resource selection ambiguous.", f.Name, false)
			a.unsafe = true
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			a.issue("ZIP_SYMLINK", "error", "Symbolic link in EPUB", "Symbolic links are not accepted EPUB resources.", f.Name, false)
			a.unsafe = true
			continue
		}
		a.files[f.Name] = f
		key := strings.ToLower(f.Name)
		if prev, ok := folded[key]; ok && prev != f.Name {
			a.issue("ZIP_CASE_COLLISION", "warning", "Filenames differ only by case", "May behave differently on case-insensitive systems: "+prev, f.Name, false)
		}
		folded[key] = f.Name
		if f.FileInfo().IsDir() {
			continue
		}
		r.Stats.Files++
		if f.Flags&1 != 0 {
			a.issue("ZIP_ENCRYPTED", "error", "Encrypted ZIP entry", "Native checks cannot read encrypted ZIP payloads.", f.Name, false)
			a.unsafe = true
			a.protected = true
			continue
		}
		if f.UncompressedSize64 > maxEntryBytes || total+f.UncompressedSize64 > maxExpandedBytes || total+f.UncompressedSize64 < total {
			a.issue("ZIP_SIZE_LIMIT", "error", "Archive exceeds scan limits", "Limits: 64 MiB per resource and 1 GiB expanded per book. This scan is incomplete.", f.Name, false)
			a.unsafe = true
			continue
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > 1<<20 && (f.CompressedSize64 == 0 || f.UncompressedSize64/f.CompressedSize64 > 1000) {
			a.issue("ZIP_RATIO_LIMIT", "error", "Suspicious compression ratio", "A large resource expands more than 1,000 times. It was not decompressed.", f.Name, false)
			a.unsafe = true
			continue
		}
		rc, err := f.Open()
		if err != nil {
			a.issue("ZIP_READ", "error", "Resource cannot be opened", err.Error(), f.Name, false)
			a.unsafe = true
			continue
		}
		reader := contextReader{ctx, io.LimitReader(rc, maxEntryBytes+1)}
		if f.Name == "mimetype" || textResource(f.Name) {
			if f.UncompressedSize64 > maxTextBytes || cached+f.UncompressedSize64 > maxCachedTextBytes {
				a.issue("TEXT_SIZE_LIMIT", "error", "Text resource exceeds scan limits", "Text is limited to 16 MiB per resource and 128 MiB cached per book. This scan is incomplete.", f.Name, false)
				a.unsafe = true
				_, err = io.Copy(io.Discard, reader)
			} else {
				var b []byte
				b, err = io.ReadAll(reader)
				if err == nil {
					a.text[f.Name] = b
					cached += uint64(len(b))
				}
			}
		} else {
			_, err = io.Copy(io.Discard, reader)
		}
		rc.Close()
		if err != nil {
			if ctx.Err() != nil {
				z.Close()
				return nil, ctx.Err()
			}
			a.issue("ZIP_CRC", "error", "Resource integrity check failed", err.Error(), f.Name, false)
			a.unsafe = true
		}
	}
	r.Stats.UncompressedBytes = int64(total)
	a.checkMimetype(filename)
	return a, nil
}

func (a *archive) checkMimetype(filename string) {
	f, ok := a.files["mimetype"]
	if !ok {
		a.issue("MIMETYPE_MISSING", "error", "Missing EPUB mimetype", "The first ZIP entry must be an uncompressed mimetype file containing application/epub+zip.", "mimetype", true)
		return
	}
	if !bytes.Equal(a.text["mimetype"], []byte("application/epub+zip")) {
		a.issue("MIMETYPE_CONTENT", "error", "Incorrect mimetype content", "The exact content must be application/epub+zip, without BOM or whitespace.", "mimetype", true)
	}
	if f.Method != zip.Store {
		a.issue("MIMETYPE_COMPRESSED", "error", "Mimetype is compressed", "The mimetype ZIP entry must use Store compression.", "mimetype", true)
	}
	if len(f.Extra) > 0 {
		a.issue("MIMETYPE_EXTRA", "error", "Mimetype has extra ZIP fields", "The mimetype entry must not have extra header fields.", "mimetype", true)
	}
	h, err := os.Open(filename)
	if err != nil {
		return
	}
	defer h.Close()
	var local [30]byte
	if _, err = h.ReadAt(local[:], 0); err != nil {
		return
	}
	if binary.LittleEndian.Uint32(local[:4]) != 0x04034b50 {
		a.issue("ZIP_PREFIX", "error", "Archive does not start with a ZIP entry", "EPUB containers must start directly with the mimetype local ZIP header.", "mimetype", true)
		return
	}
	nameLen := int(binary.LittleEndian.Uint16(local[26:28]))
	extraLen := int(binary.LittleEndian.Uint16(local[28:30]))
	if nameLen > 4096 {
		a.issue("MIMETYPE_ORDER", "error", "Mimetype is not the first entry", "EPUB requires mimetype as the first ZIP resource.", "mimetype", true)
		return
	}
	name := make([]byte, nameLen)
	_, err = h.ReadAt(name, 30)
	if err == nil && string(name) != "mimetype" {
		a.issue("MIMETYPE_ORDER", "error", "Mimetype is not the first entry", "EPUB requires mimetype as the first ZIP resource.", "mimetype", true)
	}
	if string(name) == "mimetype" && extraLen > 0 && len(f.Extra) == 0 {
		a.issue("MIMETYPE_EXTRA", "error", "Mimetype has local ZIP extra fields", "The local header must not have extra fields.", "mimetype", true)
	}
}

func (a *archive) read(name string) ([]byte, error) {
	if b, ok := a.text[name]; ok {
		return b, nil
	}
	f, ok := a.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	if f.UncompressedSize64 > maxEntryBytes {
		return nil, fmt.Errorf("resource exceeds size limit")
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(contextReader{a.ctx, io.LimitReader(r, maxEntryBytes+1)})
}

func (a *archive) cacheText(name string) ([]byte, error) {
	if b, ok := a.text[name]; ok {
		return b, nil
	}
	f, ok := a.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	var cached uint64
	for _, b := range a.text {
		cached += uint64(len(b))
	}
	if f.UncompressedSize64 > maxTextBytes || cached+f.UncompressedSize64 > maxCachedTextBytes {
		a.unsafe = true
		a.issue("TEXT_SIZE_LIMIT", "error", "Text resource exceeds scan limits", "Limits: 16 MiB per text resource and 128 MiB cached text per book. This scan is incomplete.", name, false)
		return nil, fmt.Errorf("text resource exceeds scan limits")
	}
	b, err := a.read(name)
	if err == nil {
		a.text[name] = b
	}
	return b, err
}
