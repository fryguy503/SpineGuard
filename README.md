# SpineGuard

**EPUB health checks, a built-in reader, reading statistics, and conservative repairs for Windows and macOS.**

SpineGuard checks books before you sideload them, explains findings, and saves repaired copies when a supported fix is available. Books are processed on your computer. The desktop interface uses Go and Wails with the operating system's webview; a command-line tool uses the same checking engine.

Version **1.4.1**. Native builds target Windows x64 and macOS on Intel and Apple Silicon. See the [release checklist](docs/RELEASE-CHECKLIST.md) for build evidence and the remaining platform and reader acceptance checks.

## Use the application

1. Open **SpineGuard**. On Windows, run `SpineGuard.exe`; on macOS, open `SpineGuard.app` from the package for your processor.
2. Choose **Add EPUBs**, choose a folder, or drop EPUB files onto the window. EPUB 2, EPUB 3, `.kepub`, and `.kepub.epub` files are accepted. Subfolder scanning is configurable.
3. Select a book to see its statistics and findings. Search and status filters help review a batch. Cancel stops the current operation while retaining completed scan results.
4. Choose **Review repairs** to see the fixes available for that book. Answer any questions shown, such as the book's language, then choose **Save repaired copy**. SpineGuard checks the new copy again and keeps the original. Problems that cannot be fixed automatically appear under **Still needs attention**.
5. Export **JSON** for complete reports or **CSV** for a book/statistics summary. **Clear reports** removes SpineGuard's history without deleting books or repaired copies.

Use **Rescan library** after upgrading to refresh saved findings and statistics. Version 1.1.0 corrects false duplicate-identifier warnings from HTML tree reconstruction and zero word counts caused by self-closing XHTML script elements. These corrections do not require modifying the book.

Choose **Covers** to browse cover images, or **List** for a compact table. **Group by** collects books by author, series, or health; **Sort by** controls their order. Series order uses the sequence supplied in the EPUB. Books without series metadata appear under **No series**. View, grouping, and sorting choices are remembered on this computer. Covers are read locally as needed; a placeholder appears when no supported cover is available.

For bulk repairs, choose **Select books**, select the books you want, and choose **Review selected**. You can also select all books in the current filtered view. The review shows which books have fixes available and which need individual attention. Choose **Choose folder & save copies** to pick one destination folder. Existing files are never replaced; matching filenames receive a numbered suffix. Each saved copy is checked again, and a problem with one book does not stop the others. Stopping a batch keeps copies that already completed.

Bulk repair applies the default supported fixes. Language confirmation, creating a new identifier, and removing interactive features remain individual decisions; use **Review individually** for those books. Changing a source file after the preview requires another review before that book can be repaired.

Version 1.2.0 adds language-tag checks, recovery of existing publication-identifier references, NCX identifier consistency, manifest-backed stylesheet and cover-path fixes, precise identifier dependency checks, empty-guide detection, SVG alignment checks, and offline support for standard character entities in recognized XHTML DTDs. Standard `&mdash;` and `&nbsp;` uses in those documents no longer require a book rewrite to pass the native check. Unknown/custom DTDs still require EPUBCheck.

The repair review asks only for choices relevant to the selected book. For a missing or invalid language, choose a language by name or confirm a suggestion; suggestions are never applied automatically. Removing interactive features is offered only when they are present and starts unchecked. Creating a missing book identifier also requires an explicit choice. Existing identifiers and protected font relationships are preserved.

**Advanced options** and **Technical details** stay collapsed unless you need them. Advanced options include individual repair categories and optional character normalization. Technical details contain file paths, the full proposed edits, and explanations for remaining findings. Changing a choice updates the preview automatically.

Version 1.3.1 also repairs obsolete Cover links in an EPUB 2 table of contents when the book explicitly identifies one matching cover page and that page displays its declared cover image. This uses the existing **Fix broken links** option, including in bulk repair. Missing chapters and ambiguous destinations remain flagged for manual attention.

Use **Preferences** to choose a theme, words per estimated page, reading speed, subfolder scanning, and optional EPUBCheck settings. Keyboard shortcuts: **Ctrl/Command+O** adds books, **/** searches, and **Esc** closes a dialog.

A clean built-in result means no issues were detected by the checks that ran. It does not certify full EPUB conformance or guarantee that a particular reader will never restart. If content is missing or compressed data is damaged, obtain a fresh copy from its provider. Persistent Kobo restarts can also have device, firmware, or library causes.

## Read and inspect a book

Select a scanned book and choose **Read book** to open a separate reader window. The library remains usable, and the reader stays open if you close the library. Opening the same book again brings its existing reader forward. The window starts in portrait orientation, keeps the reading page book-shaped when resized, and fits full-page cover images without stretching them.

Use page turns or continuous scrolling to read. **Explore** opens the contents, findings, search, bookmarks, and files panels; close the panel to return to an unobstructed page. Reading settings control text presentation. Your reading position and bookmarks are saved on this computer.

Choose **Inspect** on a finding to open the relevant page or original file. When a broken link or identifier has a known location, the reader highlights it; package, stylesheet, navigation, and removed interactive content can be inspected in **Source**. A missing resource remains unavailable rather than being replaced with invented content. Use the **Files** view to inspect original book files.

The reader is read-only and works offline. It preserves passive publisher styling, local images, and supported fonts while disabling book scripts, remote resources, forms, and other active content. Its layout uses a bundled EPUB.js renderer and your system webview, so it is a useful preview rather than a Kobo firmware emulator. See the [reader guide](docs/reader.md) for controls, troubleshooting, and limits.

## Statistics

| Statistic | Meaning |
| --- | --- |
| Words | Text from distinct linear reading-order documents, excluding navigation, scripts, styles, and recognized hidden content. |
| Estimated pages | Word count divided by your words-per-page preference, rounded up; default 250 words/page. |
| Source page markers | Page-list entries when supplied, otherwise pagebreak markers from the reading order. These are publisher/source markers. |
| Reading time | Word count divided by your reading-speed preference, rounded up; default 250 words/minute. |
| Reading-order documents | Analyzed linear spine documents; these need not correspond one-to-one with literary chapters. |
| Resources | Images, fonts, file count, expanded size, and largest reading-order document. |

Reflowable books have no single device-independent page count. Reader font, screen size, layout, and the book's own markup affect what you see. Estimates can be incomplete for image-only, protected, or partially analyzed books. See [validation details and limits](docs/validation.md).

## Optional EPUBCheck

The built-in checker runs without Java. Add the official EPUBCheck distribution for deeper EPUB 2/3 standards checks:

1. Install **Java 8 or later** and confirm `java -version` works.
2. Download and extract the [official EPUBCheck distribution](https://www.w3.org/publishing/epubcheck/docs/installation/). Keep `epubcheck.jar` and its accompanying `lib` directory together.
3. In **Preferences**, browse to `epubcheck.jar`, enter `java` or the Java executable's full path, and enable **Run EPUBCheck with every scan**.

EPUBCheck results appear separately as **passed**, **failed**, or **unavailable**. An unavailable checker has not validated the book. Version 5.4.0 has been exercised with valid and malformed EPUB test books. SpineGuard does not download Java or EPUBCheck automatically.

## Local data

Preferences and up to 2,000 recent reports are saved in:

- Windows: `%APPDATA%\SpineGuard`
- macOS: `~/Library/Application Support/SpineGuard`

Set `SPINEGUARD_CONFIG_DIR` before launching to use another directory. `settings.json` stores preferences; `library.json` stores book metadata, findings, statistics, timestamps, and local file paths. `reader-state.json` stores reading positions and bookmarks for up to 200 books, keyed to each file's content and path. Cover thumbnails are omitted from saved history and return after a rescan. The system webview can maintain its own application cache.

There is no account, book upload, or analytics service. JSON exports contain full reports and can include an inline cover thumbnail when one is available in memory. CSV exports contain summary fields and local paths; cells beginning with formula-triggering characters receive an apostrophe prefix for spreadsheet safety. Review exported paths before sharing a report.

## Command-line use

Examples below use `spineguard-cli`; on Windows use `spineguard-cli.exe` or `./spineguard-cli.exe` as appropriate. Put options before book/folder arguments.

```text
spineguard-cli scan book.epub
spineguard-cli scan -recursive=false -format csv Downloads > report.csv
spineguard-cli scan -format json -words-per-page 300 -reading-wpm 200 book.epub > report.json
spineguard-cli scan -epubcheck "/path/to/epubcheck.jar" -java java book.epub
spineguard-cli repair -out book.repaired.epub book.epub
spineguard-cli repair -dry-run book.epub
spineguard-cli repair -dry-run -remove-scripts book.epub
spineguard-cli repair -remove-scripts -out reader-copy.epub book.epub
spineguard-cli repair -dry-run -language en book.epub
spineguard-cli repair -language en -out language-fixed.epub book.epub
spineguard-cli repair -generate-identifier -out identified.epub book.epub
spineguard-cli repair -normalize-entities -out normalized.epub book.epub
spineguard-cli version
```

`scan` supports `-format text|json|csv`, `-recursive`, `-words-per-page` (100–1,000), `-reading-wpm` (80–1,000), `-epubcheck`, and `-java`. `repair` prints its change list and new scan report as JSON and uses default reading estimates. `-dry-run` prints a repair plan without writing a book. `-fix-links=false`, `-fix-duplicate-ids=false`, `-fix-metadata=false`, and `-fix-document-markup=false` disable those categories. `-language TAG` supplies a confirmed language; `-generate-identifier`, `-normalize-entities`, and `-remove-scripts` explicitly select optional changes. `-expect-sha256 HASH` requires the source hash from a preview, rejecting a changed source file. Scan the repaired file with `-epubcheck` if you also want standards validation. The CLI does not modify the desktop's saved history or preferences.

Exit codes: **0** no errors detected, **1** book errors found, **2** usage/tool failure, **130** interrupted. Warnings alone do not produce exit code 1. Shell redirection creates or replaces the specified report file.

## Build from source

Install **Go 1.26 or later**. Builds pin **Wails v2.17.0**; the scripts install it if missing. The interface is plain HTML/CSS/JavaScript with a vendored EPUB.js 0.3.93 browser bundle, so the application build has no Node/npm or frontend dependency-install step. Go dependencies and the Wails CLI require network access on a fresh development machine. The shipped application does not fetch reader code from a CDN.

- **Windows 10/11 x64:** Microsoft WebView2 Runtime is required to run the desktop app. If absent, the Windows build offers the official runtime download page and exits. Install **NSIS 3** only if building an installer.
- **macOS:** Build on a Mac with Xcode Command Line Tools (`xcode-select --install`). The script defaults to the host's architecture. Pass `universal` to build one app and CLI containing both Intel and Apple Silicon slices. Packaged apps require macOS 13 or later; oldest-supported-OS runtime compatibility requires its own testing.

See the [Wails v2 platform requirements](https://v2.wails.io/docs/gettingstarted/installation/) for the native toolchain.

Windows PowerShell, from the checkout:

```powershell
./scripts/build.ps1
# With NSIS installed:
./scripts/build.ps1 -Installer
```

macOS Terminal, from the checkout:

```sh
bash scripts/build-macos.sh
# One package for Intel and Apple Silicon (also used by CI):
bash scripts/build-macos.sh universal
```

Build outputs are in `build/bin`. Both scripts run Go tests and `go vet`, generate the app icons, and build the desktop application and CLI. The Mac script creates an app ZIP and verifies its ad-hoc signature. Universal builds verify both Mach-O slices; Go tests execute on the build host, and interactive runtime acceptance remains a separate check. Public Mac distribution needs Developer ID signing and notarization; neither is implied by a successful build.

For engine-only development:

```sh
go test ./...
go vet ./...
go build -o spineguard-cli ./cmd/spineguard-cli
```

To run the optional real-tool integration test, set `SPINEGUARD_EPUBCHECK_JAR` to a local EPUBCheck JAR and optionally `SPINEGUARD_JAVA_PATH`, then run `go test ./internal/epubcheck`. Normal tests use local fixtures and do not require Java.

The optional browser suite requires Node.js 20 or later, Go on `PATH`, and Playwright Chromium. It starts a local Go fixture host and tests the actual bundled reader against synthetic books:

```sh
cd tests
npm ci
npx playwright install chromium
npm test
```

These are development tools and are not installed or launched by the packaged application.

## Project layout

`internal/epub` contains archive checks, content analysis, and repairs; `internal/epubcheck` adapts the optional Java validator; `internal/library` manages settings, history, discovery, and export. `internal/reader` provides bounded read-only book sessions, sanitization, search, source inspection, and reading-state storage. `frontend` contains the desktop interface and vendored reader; `cmd/spineguard-cli` contains the CLI; `scripts` contains native build helpers.

Licensed under the [MIT License](LICENSE), with [third-party reader notices](THIRD-PARTY-NOTICES.txt). For a problem report, include the app version, operating system, finding codes, and whether EPUBCheck ran. Share a book only when you have permission to distribute it.
