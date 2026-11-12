# Reading and inspecting EPUBs

SpineGuard's built-in reader opens books already present in your scanned library. It reads the local EPUB without extracting it into a working folder or changing its contents.

## Reading a book

Select a scanned book and choose **Read book**. Choose a table-of-contents entry or use chapter navigation to move through the book. **Reading settings** lets you change the theme, font, text size, and line spacing, and switch between page turns and continuous scrolling. Choose **Publisher** or **Reset to publisher** to use the book's own presentation. Publisher styles, tables, local images, passive SVG/MathML, and supported local fonts are retained where possible.

Use text search to find passages in available documents. Queries must contain at least two characters; search returns up to 100 matches and scans up to 128 MiB of text resources. Some damaged, protected, image-only, or unsupported content cannot be searched. Search results can be opened in the reader for inspection.

Use **＋ Bookmark** to save the current page. The **Bookmarks** panel lets you return to saved pages, add notes, or remove bookmarks. Bookmarks and your last reading position are saved locally. Moving or changing the EPUB gives it a different saved-reading identity. Use **Files** to inspect a document, stylesheet, package file, or table of contents in **Source**.

## Opening a finding

Choose **Inspect** on a finding to open its original file when that file is available. For book pages, SpineGuard uses an exact reference or identifier to highlight the relevant element when possible. Missing-link findings point to the page containing the broken link. They cannot display a destination that is absent from the EPUB.

Some concerns belong to package metadata, stylesheets, navigation files, scripts, or archive structure. These open in **Source** or show an explanation when there is no readable file. Source shows the original markup as text, with line numbers. It does not execute that markup. Large files are shown in bounded excerpts around the relevant line and column.

The reader is for inspection. To change a book, return to **Review repairs** and save a separate repaired copy. Rescan and open that copy to compare the result.

## Preview behavior

EPUB.js 0.3.93 is bundled with SpineGuard; no reader code is downloaded at runtime. Go reads known ZIP entries through temporary, opaque reader-session URLs. Closing the reader expires its session.

Book scripts, inline event handlers, forms, embedded frames, plug-ins, active SVG, remote book resources, and continuous CSS animation are disabled. Strict content policy and iframe sandboxing provide additional isolation. Local stylesheets, images, media, and font references are restricted to known resources in the current EPUB. Small, valid PNG/JPEG/GIF data images are also supported. Supported IDPF/Adobe publisher font obfuscation can be decoded for viewing; DRM-protected content remains unavailable.

Malformed HTML can receive a safe display fallback without editing the EPUB. Unsafe or malformed styling may be omitted. Missing chapters, failed CRCs, unsupported protection, ambiguous reading order, or oversized resources can prevent a page from opening; other available files can still be inspected where safe.

This preview uses your Windows WebView2 or macOS system webview. It can help inspect text, navigation, images, and repairs, but does not emulate Kobo firmware or certify EPUB conformance. Font metrics, page breaks, media support, and device behavior can differ. EPUBCheck and testing a permitted copy on the target device remain separate checks.

## Reader limits

| Area | Limit |
| --- | --- |
| EPUB archive | Regular local file, at most 2 GiB; no multipart or ZIP64 containers. |
| ZIP directory | At most 20,000 entries and 32 MiB; unsafe, duplicate, symbolic-link, and ZIP-encrypted entries are refused. |
| Expanded resources | 64 MiB per resource and 1 GiB declared total; excessive compression ratios are refused. |
| Text preview | 16 MiB per document, 250,000 tokens/nodes, 256 nesting levels, and 256 attributes per element. |
| Stylesheets | 4 MiB per stylesheet; unsupported or malformed CSS can be omitted. |
| Source excerpt | Up to 200 lines and 128 KiB per request. |
| Saved reading state | Up to 200 books and 200 bookmarks per book; 4 MiB total state file. |

Reading positions and bookmarks are stored in `reader-state.json` in SpineGuard's normal configuration directory. The operating system webview may also keep its own cache. The original EPUB is never rewritten by opening, searching, navigating, or bookmarking it.
