# Validation and repair behavior

SpineGuard 1.2.1 performs local structural and integrity checks. Its native checker is useful before sideloading; the optional official EPUBCheck tool provides broader standards validation. Neither predicts every e-reader firmware or hardware failure.

## What the built-in engine checks

| Area | Checks |
| --- | --- |
| ZIP container | Readability, payload CRCs within scan limits, duplicate names, unsafe paths, symbolic links, encrypted entries, case collisions, and resource-size/expansion limits. |
| EPUB identification | Exact `mimetype` content, first local ZIP entry, Store compression, and absence of extra mimetype header fields. |
| Container and package | Required `META-INF/container.xml`, package reference, expected root namespaces, EPUB 2/3 version, and required publication title/language/identifier. EPUB 3 modification metadata is checked for presence. |
| Manifest and spine | Required manifest attributes, duplicate IDs/resources, missing resources, remote resources, missing/duplicate reading-order references, and empty reading order. |
| Navigation | EPUB 3 navigation declaration and table-of-contents marker; EPUB 2 NCX reference. |
| Content | XML parse failures, XHTML root namespace, duplicate IDs, selected local resource paths and fragments, and CSS `url()`/quoted `@import` references. |
| Compatibility | Script/frame content, remote embedded resources, large reading-order documents, large images, and unsupported or damaged image data. |
| Protection | Digital-signature metadata, encrypted resources, and recognized font-obfuscation algorithms. |

The checker never opens a book as an interactive web page, executes its scripts, or fetches remote book resources. It inspects archive entries in place rather than extracting them into folders.

CSS reference checks use a limited parser; they do not validate the complete CSS grammar or reproduce a reading system's layout. Image decoding covers Go's JPEG, PNG, and GIF decoders. Unrecognized image formats produce an advisory rather than proof that an image is corrupt. Pixel decoding is limited to supported images at most 16 MiB and within the dimension advisory limits. SVG receives XML/reference checks, not a rendered-image comparison.

The native engine does not implement every EPUB schema, namespace constraint, HTML/CSS rule, media codec, script behavior, accessibility rule, or fallback-chain requirement. Recognized standard XHTML DTD declarations can use the built-in HTML4 character-entity table without a network fetch. Internal/custom declarations do not enable that table. A remaining DTD entity that prevents native parsing produces an incomplete-check warning and is preserved. Use EPUBCheck when a standards-conformance decision matters.

## Reading statistics

Words come from distinct, analyzed linear spine documents. Navigation, script/style text, templates, recognized hidden content, and `linear="no"` documents are excluded. The text walker recognizes HTML `hidden`, `aria-hidden="true"`, and a simple inline `display:none` pattern; it does not compute an entire stylesheet's visibility rules.

Word counting groups letters and numbers with internal apostrophes/hyphens. Han, Hiragana, Katakana, and Hangul characters are counted individually. This is a consistent counting rule, not language-specific linguistic segmentation. Image-only pages and text inside images do not receive OCR.

- **Estimated pages:** `ceil(words / wordsPerPage)`, default 250 words/page.
- **Estimated reading minutes:** `ceil(words / readingWPM)`, default 250 words/minute.
- **Source page markers:** the largest detected page-list marker count when available, otherwise the sum of pagebreak markers in analyzed linear content.
- **Reading-order documents:** distinct analyzed linear spine documents, which may include front matter or several documents for a single literary chapter.

Estimates and reference analysis can be incomplete when content is protected, missing, malformed, unsupported, or above a scan limit. Source page markers represent boundaries supplied by the book; their count does not establish a printed edition's total length or the number of screens a Kobo will display.

## Processing limits

These are SpineGuard resource limits and advisories, not published Kobo limits.

| Limit | Value / behavior |
| --- | --- |
| Files selected per batch | At most 2,000; folder discovery skips hidden subfolders and symlink entries. |
| Saved report history | Most recent 2,000 reports. |
| ZIP entries per book | 20,000; scanning stops above this count. |
| One expanded resource | 64 MiB. |
| Total declared expanded resource size | 1 GiB per book. |
| Large-resource expansion ratio | Above 1,000:1 for resources larger than 1 MiB is rejected. |
| One text resource / cached text total | 16 MiB / 128 MiB per book. |
| XML/content nesting | 1,024 levels. |
| Content tree | Streaming preflight before DOM allocation: 200,000 tokens, 1,024 open tags, 256 attributes per element; at most 200,000 visited nodes. Findings explain incomplete analysis. |
| Native findings | First 2,000 findings plus a truncation notice. |
| Large chapter advisory | More than 1 MiB expanded markup in one linear reading-order document. |
| Large image advisory | More than 6,000 pixels on either edge or 20 megapixels. |
| Inline cover thumbnail | At most 2 MiB, supported raster format, within image dimension limits. |
| EPUBCheck subprocess | Two minutes, 512 MiB maximum Java heap, 64 KiB retained tool log. |
| EPUBCheck report import | 32 MiB JSON and first 10,000 issue locations; omissions are described. |

Scan-limit findings mean the book was not completely analyzed. Splitting an unusually large book or obtaining a better-produced edition can help; raising limits is not a repair for damaged payloads.

## Repairs

Repair always writes a separate new `.epub`. An existing destination is never overwritten, including when a native save dialog offers to replace it. The source book is not modified.

The repair review lists selected actions, affected files, and unresolved findings. Changing an option refreshes the preview. The desktop binds the saved copy to the reviewed source hash and refuses a source that changed after review.

Supported actions are:

1. Rebuild ZIP packaging with exact `application/epub+zip` identification first, uncompressed, with no mimetype extra fields.
2. In otherwise reparsable UTF-8 XML, escape bare ampersands, replace recognized HTML named entities with their Unicode equivalents, and remove invalid control characters. A changed document is accepted only when it passes the native XML parser.
3. Correct a broken internal resource path when exactly one proven replacement exists: a unique fragment destination, unambiguous case correction, or a matching existing manifest resource with a compatible role/media type. Explicit cover-role images can use a uniquely identified package cover image. Ambiguous or absent destinations stay unresolved; no missing payloads are invented.
4. Repair duplicate source identifiers when dependency analysis proves the later occurrences can be renamed. The first destination and existing navigation remain intact. CSS ID dependencies, scripts, forms/ARIA/SMIL references, unavailable styles, or syntax that cannot be analyzed can prevent editing. Class selectors alone do not block all identifier repairs.
5. When explicitly selected, remove script elements and inline scripting from a reader copy. This can remove interactive behavior. JavaScript resource files and other archive resources are retained; they are not silently deleted.
6. Restore a unique existing publication identifier's reference without changing its value. For a uniquely declared EPUB 2 NCX, synchronize its `dtb:uid` metadata with the verified package identifier, preserving navigation points and play order. A missing/invalid language is filled only from an explicit user value. New identifier generation requires selection and is bound to the reviewed source; valid identifiers remain unchanged.
7. Remove a truly empty optional OPF guide, and canonicalize a provable case-only SVG alignment value. Other structure and geometry are preserved.
8. Optionally normalize recognized standard named character entities to numeric references in text and attributes. Comments, CDATA, processing instructions, and the DOCTYPE remain unchanged. Unknown/custom entity definitions are preserved.
9. Preserve other resources and their original compressed payloads, then rescan the output and show the change list and remaining findings. Content edits use source-byte patches instead of whole-document HTML serialization.

SpineGuard does not guess a language, reconstruct missing content, or invent chapter navigation. New publication identity requires an explicit choice. It does not close broken tags, reconstruct truncated compressed data, simplify CSS, resize images, or convert EPUB to KEPUB. Scripts remain unless reader cleanup is selected. A successful repair may leave content or conformance errors to resolve.

Version 1.1.0 reads well-formed XHTML as XML for statistics and identifiers. Valid self-closing script tags no longer hide the following book text, and HTML parser reconstruction no longer creates spurious duplicate-identifier warnings. Rescan saved reports after upgrading; these scanner corrections require no EPUB edits.

Repair is refused for failed archive-integrity/safety checks, missing `container.xml`, unknown encryption, or digital signatures. DRM is not removed. Recognized IDPF/Adobe font obfuscation is preserved; its package and encryption documents are left intact so the publication identifier and font relationship remain unchanged. XML with custom entity declarations is not automatically rewritten.

If the report still shows missing files, unreadable ZIP data, or structural XML damage after the supported repairs, obtain a fresh copy or ask its provider to correct the book. Test the repaired copy on your reader while retaining the original.

## EPUBCheck integration

SpineGuard invokes the selected local JAR with Java and imports its JSON report. The accompanying `lib` directory must remain beside the JAR. Java 8 or later is required for the tested EPUBCheck 5.4.0 distribution. Tool files are never downloaded automatically.

| Result | Meaning |
| --- | --- |
| Passed | EPUBCheck completed without error/fatal findings; warnings can still exist. |
| Failed | EPUBCheck completed and reported conformance errors. |
| Unavailable | Configuration, Java startup, timeout, cancellation, or report processing prevented a usable validation result. |

EPUBCheck errors and warnings are also imported into the book's findings. Informational/usage messages stay informational. Multiple locations can produce multiple SpineGuard findings, so location totals need not equal EPUBCheck's message-group totals. Repairs always run native verification; the desktop also reruns EPUBCheck when enabled. The CLI repair command uses native verification; follow it with a standards-enabled scan if needed.

Reference specifications and tools: [EPUB 3.3](https://www.w3.org/TR/epub-33/), [EPUB 2.0.1 OPF](https://idpf.org/epub/20/spec/OPF_2.0_latest.htm), [EPUBCheck command-line reference](https://www.w3.org/publishing/epubcheck/docs/cli/), and [EPUBCheck reporting](https://w3c.github.io/epubcheck/docs/report/).

Kobo provides separate guidance for [book-opening problems](https://help.kobo.com/hc/en-us/articles/360017605054-Troubleshoot-eBook-and-audiobook-issues) and [frozen/unresponsive readers](https://help.kobo.com/hc/en-us/articles/360017605134-Fix-common-Kobo-eReader-problems). A clean file scan cannot rule out those other causes.
