# Release checklist

Use this checklist for each version and record the exact build, operating system, architecture, and result. Passing tests, producing a package, signing it, publishing it, and accepting it on a physical reader are separate milestones.

## 1.4.0 built-in reader

- Added an offline, read-only EPUB reader using the vendored EPUB.js 0.3.93 browser renderer, including page/scroll reading, contents, document navigation, search, reading controls, saved position, and bookmarks.
- Findings can locate their original page/reference or escaped source file. Missing resources remain unavailable, and preview fallbacks do not change book contents or validation results.
- Go reader sessions restrict access to known EPUB resources, enforce archive/resource/markup limits, verify payload reads, expire on close, and reject a changed source file. Sanitized XHTML/CSS/SVG, permanent script disabling, content policy, and iframe sandboxing isolate book content from the application bridge and external network.
- Reader tests use synthetic EPUBs and the actual Go resource handler and vendored renderer. The frontend CI job installs Go for its local fixture host. Windows ZIP/installer and Mac app resources include the reader guide and third-party notices.
- Native Windows/macOS reader acceptance, signed public distribution, and sustained physical-reader behavior require the platform and device checks below. Record the final test/build results for the release revision before publishing.

## 1.3.1 stale NCX cover links

- Added a narrowly evidenced correction for a missing Cover entry in the active EPUB 2 NCX: the OPF guide identifies the cover page, the navigation label matches, and the existing cover wrapper displays the uniquely declared cover image.
- Only the broken link is edited. Navigation labels, play order, chapter contents, and original files are preserved. Ambiguous identities, missing chapters, unknown bases/encoding, and invalid cover evidence remain unmodified.
- A real-book test copy cleared the native missing-link error, preserved its original hash, word/chapter counts, and all 69 other compressed resource payloads, and changed only the one NCX cover path. Official EPUBCheck cleared the missing-resource finding without introducing any new formal findings; two unrelated existing conformance findings remained.
- Focused fixtures cover ambiguous or missing roles, invalid navigation structure, namespaces, base URIs, encoding, queries/fragments, non-cover contents, existing destinations, and disabled link repair. Local Go tests and `go vet` pass, and Windows app/CLI/portable/installer packages build successfully. Platform CI checks the full browser suite and Windows/universal Mac packages before distribution.

## 1.3.0 cover views, grouping, and bulk repair

- Added cover and list views with remembered author/series/health grouping and sort preferences. EPUB series metadata is read without guessing from filenames.
- Covers load locally from known saved books without requiring a full rescan; bounded thumbnails stay out of persisted report history.
- Bulk repair reviews selected books, applies default fixes only after confirmation, chooses one output folder, and preserves originals and existing output files. Language and optional cleanup decisions remain individual.
- Cancellation retains completed copies and reports unfinished items; changed-source and per-book failures remain visible.
- Go tests and `go vet` pass, and Windows app/CLI/portable/installer packages build locally. Browser coverage includes 37 passing cases, including filtered selection, bulk-to-individual handoff, keyboard focus, source hashes, cancellation, partial failures, and compact layouts in both themes.
- Read-only verification of 31 local books preserved all source hashes and word counts. Nine books supplied declared series metadata; all 29 declared covers loaded successfully without a health rescan. Two books without declared covers use placeholders.
- Native interactive acceptance, Windows publisher signing, Mac notarization, and physical-reader acceptance remain separate gates.

## 1.2.1 simpler repair review

- Published the versioned Windows/Mac binaries, command-line tools, checksums, and build provenance. All eight published assets were freshly downloaded and matched their reviewed SHA-256 hashes.

- Repair review presents the selected book's available fixes in plain language and shows only relevant questions or optional changes.
- Language choices use language names; detected suggestions still require confirmation. Optional removal of interactive features and creation of a missing identifier remain explicit choices.
- Individual repair switches and optional character normalization move into collapsed Advanced options. File paths, full edit descriptions, and detailed remaining findings move into collapsed Technical details.
- Changing a choice still invalidates the previous preview, and saving still requires the current source hash. The original is preserved and the saved copy is rescanned.
- Go tests and `go vet` pass locally, and Windows portable and installer packages build successfully. Browser coverage includes 26 cases, with compact-window visual review in both themes. The language-only review fits at 980×680 without scrolling; mixed repairable/unrepairable findings and deselected fixes retain accurate next steps.
- Platform CI runs the full browser suite and Windows x64 / universal Mac builds separately. Universal packaging verifies Intel and Apple Silicon slices and ad-hoc signatures; it does not establish interactive Intel runtime acceptance.
- Windows signing, Mac Developer ID signing/notarization, interactive platform acceptance, and physical-reader acceptance remain separate gates.

## 1.2.0 additional checks and repairs

- Added confirmed-language input and source-language suggestions, existing identifier/reference recovery, explicit missing-identifier generation, and package/NCX identifier consistency.
- Added manifest-backed resource and cover-path correction, bounded per-ID CSS dependency checks, empty OPF guide checks, and SVG alignment validation/canonicalization.
- Recognized standard XHTML entities parse offline without source edits; numeric normalization is optional. Unknown/custom entity definitions remain protected.
- Tested separate copies from a 31-book corpus: all original SHA-256 hashes stayed unchanged. Default selected fixes produced nine test copies; explicit language choices, script cleanup, and entity normalization were exercised separately. Reading-order word counts were preserved.
- Official EPUBCheck 5.4.0 compared 14 original/copy pairs. No new error kinds were introduced; unrelated pre-existing conformance errors remain visible. Supported repairs made the tested Goodbye, Nemesis Games, and Drive copies pass EPUBCheck.
- Go tests, `go vet`, and all 21 browser UI tests pass. Windows portable and installer packages built locally. CI runs Go tests on Windows x64 and Apple Silicon macOS. Universal Mac packages compile both Intel and Apple Silicon slices, verify them with `lipo`, and verify their ad-hoc signatures; this does not establish Intel runtime acceptance. Interactive Mac, signing/notarization, and physical-reader acceptance remain separate gates.

## 1.1.0 repair expansion evidence

- Source-based XHTML parsing replaces HTML tree reconstruction for word counts and identifiers. A read-only rescan of 31 local EPUBs reduced duplicate-identifier warnings from 1,379 to 5 and corrected a zero-word report to 10,564 words.
- Test copies repaired two uniquely identifiable broken links in one book, while retaining the third unresolved link. Optional cleanup removed nine script elements across two books and preserved their word counts.
- SHA-256 verification confirmed all 31 original EPUBs were unchanged. Output copies were written only to a separate test directory.
- Official EPUBCheck 5.4.0 compared three original/copy pairs and found no new error kinds. Existing conformance errors remain; these results do not establish universal EPUB validity or physical-reader acceptance.
- Go tests and `go vet` pass. All 17 browser UI tests pass, including default script preservation, explicit cleanup selection, preview/source binding, stale reports, and whole-library rescanning.
- Windows 1.1.0 portable and installer packages built locally. That version's native CI builds passed on Windows, Intel macOS, and Apple Silicon macOS. Signing and physical-reader acceptance remain independent gates.

## Initial 1.0.0 evidence

| Area | Evidence currently established |
| --- | --- |
| Go engine / library / CLI | Local automated checks exercised on Windows; rerun the final suite for the release commit. |
| Optional EPUBCheck | Official 5.4.0 distribution executed locally with Java against valid and malformed EPUB fixtures; import and result classification passed. |
| Windows desktop build | Production executable, portable ZIP, and NSIS installer built using Go 1.27.3 and Wails 2.17.0; initial native CI build also passed. Installer lifecycle acceptance remains pending. |
| Windows startup | Actual Wails interface and native EPUB picker opened; accessibility confirmed the live interface and Go dialog bridge. Full native scan/repair/export acceptance remains pending. |
| Frontend behavior | All 9 reproducible Playwright tests passed, including workflows, error handling, hostile metadata, and both themes at minimum and standard window sizes. The suite injects an explicit preview adapter. |
| macOS | Initial native builds, Go tests, and packaging passed on Intel and Apple Silicon CI runners. Interactive Mac runtime acceptance remains pending. |
| Physical Kobo / other reader | Hardware behavior and restart prevention have not been established. |
| Distribution | Windows packages are unsigned; Mac packages are ad-hoc signed, without Developer ID notarization. No public release has been published. |

## Source and automated checks

- [ ] Version matches app constant, CLI output, package metadata, and release notes.
- [ ] `go mod verify`, `go test ./...`, and `go vet ./...` pass for the final source.
- [ ] Frontend harness passes; review empty state, findings, filters, search, preferences, progress/cancel, repair result, exports, keyboard use, and both themes.
- [ ] Optional real-tool test passes with `SPINEGUARD_EPUBCHECK_JAR` pointing to the complete extracted distribution.
- [ ] Malformed XML, missing resources, CRC failures, duplicate/unsafe paths, protection metadata, image failures, and scan-limit fixtures still produce the expected findings.
- [ ] Repair fixtures prove source preservation, no destination overwrite, no missing-resource deletion, obfuscated-font preservation, and post-repair verification.
- [ ] Reader fixtures cover EPUB 2/3 navigation, fixed-layout/RTL content, safe SVG and fonts, source/reference jumps, missing and damaged resources, hostile markup/CSS, session expiry, cancellation, changed sources, and reading-state bounds.
- [ ] Test reader controls, page/scroll flow, contents/files, search, bookmarks/resume, and source excerpts in both themes and minimum/standard window sizes. Confirm no book scripts, external requests, or application bridge are exposed in book frames.
- [ ] Documentation describes the final behavior and supported limits. Customer-facing text uses neutral download/support wording and contains no private account/repository URLs.

## Native Windows acceptance

- [ ] Run `./scripts/build.ps1` on Windows x64 and record artifact checksums.
- [ ] Launch on a clean Windows 10/11 test environment with WebView2 installed.
- [ ] Exercise native file/folder/save dialogs, file drop, scan/cancel/rescan, settings persistence, repair copy, reveal-file action, and JSON/CSV export.
- [ ] Read a local EPUB in both page/scroll flows, follow concerns to pages/source, close during opening/search, and verify bookmarks/resume after restart. Confirm third-party notices and documentation are included in ZIP and installed files.
- [ ] Restart the app; verify history, preferences, and unavailable/moved-book behavior.
- [ ] Check missing-WebView2 behavior and clear installation guidance.
- [ ] If distributing an installer: run `./scripts/build.ps1 -Installer` with NSIS 3, then verify installation, launch, upgrade, and uninstallation.
- [ ] Apply the chosen publisher signature, if available, and verify the final distributed bytes after signing.

## Native macOS acceptance

- [ ] Build with `bash scripts/build-macos.sh universal` and verify both Mach-O slices, or record separate native architecture builds. Record the host on which Go tests actually ran.
- [ ] Verify each `.app`, app ZIP, CLI architecture, and checksum.
- [ ] Exercise the native application checks listed for Windows, including Command+O and the native menu.
- [ ] Exercise the actual WebKit reader with publisher fonts, SVG, page/scroll layout, concern jumps, and reading-state persistence. Confirm the signed app contains documentation and third-party notices in `Contents/Resources`.
- [ ] Verify packaged launch and settings persistence on the oldest macOS version claimed for that artifact.
- [ ] For public distribution, replace the local ad-hoc signature with Developer ID signing, notarize the package, staple as appropriate, and verify Gatekeeper acceptance on a clean machine.
- [ ] Recheck the final signed/notarized archive and record its checksum.

## Reader and publication acceptance

- [ ] Use owned/licensed sample books with EPUB 2, EPUB 3, image-heavy content, page markers, obfuscated fonts, and repaired XML/packaging.
- [ ] Sideload selected checked copies to the target Kobo model and firmware; verify import, navigation, images, page turns, sleep/wake, and sustained reading. Record duration and observed failures without claiming universal compatibility.
- [ ] Confirm unresolved errors and unavailable standards validation remain visible in the final app/package.
- [ ] Publish only the artifacts whose platform checks are complete. Clearly label unsigned or unnotarized packages.
- [ ] Include version, changes, supported platform/architecture, prerequisites, known limitations, checksums, and neutral support instructions with the download.

Release record: version / source revision / toolchain / OS and architecture / test evidence / signing and notarization / artifact checksums / reader model and firmware / publication status.
