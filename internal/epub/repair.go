package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// sanitizeXML repairs only recognizable XML syntax damage. It does not invent
// structure, close tags, remove scripts, fetch DTDs, or interpret custom entities.
// A candidate is accepted only if the strict XML parser can read the result.
func sanitizeXML(src []byte) ([]byte, bool) {
	if _, err := validateXML(src); err == nil || dtdEntityUnsupported(src, err) {
		return src, false
	}
	if !utf8.Valid(src) || bytes.Contains(bytes.ToUpper(src), []byte("<!ENTITY")) {
		return src, false
	}
	clean := strings.Map(func(r rune) rune {
		if r == 9 || r == 10 || r == 13 || r >= 32 && r != 0xfffe && r != 0xffff {
			return r
		}
		return -1
	}, string(src))
	var out strings.Builder
	inTag := false
	quote := byte(0)
	for i := 0; i < len(clean); {
		isDoctype := len(clean)-i >= 9 && strings.EqualFold(clean[i:i+9], "<!DOCTYPE")
		if strings.HasPrefix(clean[i:], "<![CDATA[") || strings.HasPrefix(clean[i:], "<!--") || strings.HasPrefix(clean[i:], "<?") || isDoctype {
			endToken := ">"
			prefix := 1
			switch {
			case strings.HasPrefix(clean[i:], "<![CDATA["):
				endToken = "]]>"
				prefix = 9
			case strings.HasPrefix(clean[i:], "<!--"):
				endToken = "-->"
				prefix = 4
			case strings.HasPrefix(clean[i:], "<?"):
				endToken = "?>"
				prefix = 2
			}
			end := strings.Index(clean[i+prefix:], endToken)
			if end < 0 {
				return src, false
			}
			end += i + prefix + len(endToken)
			out.WriteString(clean[i:end])
			i = end
			continue
		}
		c := clean[i]
		if c == '<' {
			inTag = true
		} else if c == '>' && quote == 0 {
			inTag = false
		} else if inTag && (c == '\'' || c == '"') {
			if quote == 0 {
				quote = c
			} else if quote == c {
				quote = 0
			}
		}
		if c == '&' && (!inTag || quote != 0) {
			j := i + 1
			for j < len(clean) && j-i < 40 && ((clean[j] >= 'a' && clean[j] <= 'z') || (clean[j] >= 'A' && clean[j] <= 'Z') || (clean[j] >= '0' && clean[j] <= '9') || clean[j] == '#') {
				j++
			}
			if j < len(clean) && clean[j] == ';' && j > i+1 {
				entity := clean[i : j+1]
				switch entity {
				case "&amp;", "&lt;", "&gt;", "&quot;", "&apos;":
					out.WriteString(entity)
				default:
					if strings.HasPrefix(entity, "&#") {
						out.WriteString(entity)
					} else {
						decoded := stdhtml.UnescapeString(entity)
						// html.UnescapeString also accepts legacy prefix matches:
						// &notit; becomes "¬it;" although notit is not an entity.
						// Complete HTML entity values never retain their terminal
						// semicolon, except the entity that represents one itself.
						if decoded == entity || (strings.HasSuffix(decoded, ";") && entity != "&semi;") {
							return src, false
						}
						out.WriteString(decoded)
					}
				}
				i = j + 1
				continue
			}
			out.WriteString("&amp;")
			i++
			continue
		}
		out.WriteByte(c)
		i++
	}
	candidate := []byte(out.String())
	if bytes.Equal(candidate, src) {
		return src, false
	}
	if _, err := validateXML(candidate); err != nil {
		return src, false
	}
	return candidate, true
}

// Repair writes a new EPUB. Existing destinations are never overwritten; unknown
// encryption, signatures, unreadable payloads and ambiguous paths are refused.
func Repair(ctx context.Context, input, output string, opts Options) (result RepairResult, err error) {
	inputAbs, e := filepath.Abs(input)
	if e != nil {
		return result, e
	}
	outputAbs, e := filepath.Abs(output)
	if e != nil {
		return result, e
	}
	if strings.EqualFold(inputAbs, outputAbs) {
		return result, errors.New("repair output must be different from the original book")
	}
	if _, e = os.Lstat(outputAbs); e == nil {
		return result, errors.New("repair destination already exists; choose another filename")
	} else if !errors.Is(e, os.ErrNotExist) {
		return result, e
	}
	report := Report{Issues: []Issue{}}
	a, e := openArchive(ctx, inputAbs, &report)
	if e != nil {
		return result, fmt.Errorf("cannot repair archive: %w", e)
	}
	defer a.z.Close()
	a.inspectProtection()
	if a.unsafe {
		return result, errors.New("archive integrity or safety checks failed; repair cannot recover missing or corrupt payloads")
	}
	if a.protected {
		return result, errors.New("repair is disabled for encrypted or digitally signed books")
	}
	if _, ok := a.files["META-INF/container.xml"]; !ok {
		return result, errors.New("required container.xml is missing; its contents cannot be safely invented")
	}
	changes := []string{"Rebuilt EPUB ZIP packaging: exact mimetype first, uncompressed, without extra fields."}
	modified := map[string][]byte{}
	packages := map[string]bool{}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if b := a.text["META-INF/container.xml"]; len(b) > 0 {
		if decoder(b).Decode(&container) == nil {
			for _, r := range container.Rootfiles {
				packages[r.FullPath] = true
			}
		}
	}
	for name, b := range a.text {
		if name == "mimetype" || strings.HasSuffix(strings.ToLower(name), ".css") {
			continue
		}
		if a.obfuscated && (packages[name] || strings.HasSuffix(strings.ToLower(name), ".opf") || name == "META-INF/encryption.xml") {
			continue
		}
		if fixed, ok := sanitizeXML(b); ok {
			modified[name] = fixed
			changes = append(changes, "Normalized malformed XML entities or invalid control characters in "+name+".")
		}
	}
	sort.Strings(changes[1:])
	if err = ctx.Err(); err != nil {
		return result, err
	}
	tmp, e := os.CreateTemp(filepath.Dir(outputAbs), ".spineguard-repair-*.epub")
	if e != nil {
		return result, e
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	z := zip.NewWriter(tmp)
	fatal := func(e error) (RepairResult, error) { z.Close(); tmp.Close(); return RepairResult{}, e }
	h := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	h.SetMode(0644)
	w, e := z.CreateHeader(h)
	if e != nil {
		return fatal(e)
	}
	if _, e = w.Write([]byte("application/epub+zip")); e != nil {
		return fatal(e)
	}
	for _, f := range a.z.File {
		if e = ctx.Err(); e != nil {
			return fatal(e)
		}
		if f.Name == "mimetype" {
			continue
		}
		header := f.FileHeader
		// Raw copy keeps original compressed payloads byte-for-byte unless a
		// documented XML repair was applied. No resources are silently deleted.
		if b, ok := modified[f.Name]; ok {
			header.CRC32 = 0
			header.CompressedSize = 0
			header.CompressedSize64 = 0
			header.UncompressedSize = 0
			header.UncompressedSize64 = 0
			writer, er := z.CreateHeader(&header)
			if er != nil {
				return fatal(er)
			}
			if _, er = writer.Write(b); er != nil {
				return fatal(er)
			}
		} else {
			reader, er := f.OpenRaw()
			if er != nil {
				return fatal(er)
			}
			writer, er := z.CreateRaw(&header)
			if er != nil {
				return fatal(er)
			}
			if _, er = io.Copy(writer, contextReader{ctx, reader}); er != nil {
				return fatal(er)
			}
		}
	}
	if e = z.Close(); e != nil {
		tmp.Close()
		return result, e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return result, e
	}
	if e = tmp.Close(); e != nil {
		return result, e
	}
	verified, e := Scan(ctx, tmpName, opts)
	if e != nil {
		return result, fmt.Errorf("repaired copy could not be verified: %w", e)
	}
	for _, issue := range verified.Issues {
		if strings.HasPrefix(issue.Code, "ZIP_") || strings.HasPrefix(issue.Code, "MIMETYPE_") {
			if issue.Severity == "error" {
				return result, errors.New("repaired archive did not pass container verification")
			}
		}
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	// A hard link publishes the completed file atomically with exclusive-create
	// semantics. If unavailable, an exclusive destination and rollback preserve
	// the same no-overwrite guarantee (with a short copy window).
	if e = os.Link(tmpName, outputAbs); e != nil {
		dst, er := os.OpenFile(outputAbs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if er != nil {
			return result, fmt.Errorf("cannot create repair output: %w", er)
		}
		ok := false
		defer func() {
			dst.Close()
			if !ok {
				os.Remove(outputAbs)
			}
		}()
		src, er := os.Open(tmpName)
		if er != nil {
			return result, er
		}
		_, er = io.Copy(dst, contextReader{ctx, src})
		src.Close()
		if er == nil {
			er = dst.Sync()
		}
		if er == nil {
			er = dst.Close()
		}
		if er != nil {
			return result, er
		}
		ok = true
	}
	verified.Path = outputAbs
	verified.Filename = filepath.Base(outputAbs)
	result = RepairResult{OutputPath: outputAbs, Changes: changes, Report: verified}
	return result, nil
}
