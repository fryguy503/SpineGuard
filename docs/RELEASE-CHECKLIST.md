# Release checklist

Use this checklist for each version and record the exact build, operating system, architecture, and result. Passing tests, producing a package, signing it, publishing it, and accepting it on a physical reader are separate milestones.

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
- [ ] Documentation describes the final behavior and supported limits. Customer-facing text uses neutral download/support wording and contains no private account/repository URLs.

## Native Windows acceptance

- [ ] Run `./scripts/build.ps1` on Windows x64 and record artifact checksums.
- [ ] Launch on a clean Windows 10/11 test environment with WebView2 installed.
- [ ] Exercise native file/folder/save dialogs, file drop, scan/cancel/rescan, settings persistence, repair copy, reveal-file action, and JSON/CSV export.
- [ ] Restart the app; verify history, preferences, and unavailable/moved-book behavior.
- [ ] Check missing-WebView2 behavior and clear installation guidance.
- [ ] If distributing an installer: run `./scripts/build.ps1 -Installer` with NSIS 3, then verify installation, launch, upgrade, and uninstallation.
- [ ] Apply the chosen publisher signature, if available, and verify the final distributed bytes after signing.

## Native macOS acceptance

- [ ] Run `bash scripts/build-macos.sh` separately on Apple Silicon and Intel hosts/runners.
- [ ] Verify each `.app`, app ZIP, CLI architecture, and checksum.
- [ ] Exercise the native application checks listed for Windows, including Command+O and the native menu.
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
