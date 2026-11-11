# Frontend smoke tests

These development-only tests serve the actual static frontend on a temporary
loopback HTTP port and inject a simulated desktop API before the page loads.
The UI displays its **Preview data** banner. No mock data or test server is
included in the application build, and no EPUB files are uploaded.

From this directory, with Node.js 20 or newer:

```sh
npm ci
npm run install-browser
npm test
```

On a Linux CI runner, use `npx playwright install --with-deps chromium` for the
browser installation step. Browser binaries and npm are not needed to build
or run the desktop application.

The suite covers importing, native-drop registration, progress and cancellation,
repair previews and unresolved findings, issue-specific repair choices,
collapsed advanced controls and technical details, explicit script-cleanup selection,
metadata recovery defaults, named language choices and suggestions that require selection,
debounced language entry and validation feedback, gated identifier generation,
document-markup choices and optional entity normalization,
source-hash binding and refresh after source changes, pending-option changes,
formal-check errors, metadata escaping,
search/filter, preferences and estimates, exports, error recovery, report
clearing, whole-library rescans, keyboard shortcuts, and layout at 980×680 and
1440×1000. Repair review is checked in both themes at the minimum window size.
Cover/list views, remembered grouping and sorting, natural series order, bounded
lazy cover reads, missing-cover placeholders, filtered selection, bulk review,
default-only options and source hashes, individual-review handoff, cancellation,
folder-picker cancellation, and partial failures are covered as well.
It checks for JavaScript/console errors and unexpected network requests.

Optional environment variables:

- `SPINEGUARD_BROWSER_EXECUTABLE`: use an existing Chrome/Chromium executable.
- `SPINEGUARD_PLAYWRIGHT_MODULE`: reuse an existing Playwright package by its
  absolute package-directory path instead of installing another copy.
- `SPINEGUARD_UI_SCREENSHOTS`: directory for optional verification screenshots.

These tests exercise the UI contract. Go tests cover the EPUB engine and app
backend separately; native file dialogs and Wails/platform integration require
an actual desktop smoke test.
