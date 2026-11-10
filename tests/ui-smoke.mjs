import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { createServer } from 'node:http';
import { readFile, mkdir } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// The adapter below exists only in this test process. Production frontend files
// are served unchanged; the optional preview adapter is injected before loading.
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.SPINEGUARD_PLAYWRIGHT_MODULE || 'playwright');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
let server;
let browser;
let origin;

const commonStats = {
  words: 82450, estimatedPages: 330, sourcePages: 312, chapters: 25,
  images: 12, fonts: 3, readingMinutes: 330, files: 48,
  uncompressedBytes: 5500000, largestChapterBytes: 45000,
};
const cleanBook = {
  path: '/books/the-wild-atlas.epub', filename: 'the-wild-atlas.epub',
  title: 'The Wild Atlas', author: 'Evelyn Hart', language: 'en',
  epubVersion: '3.0', identifier: 'urn:uuid:atlas', fileBytes: 2456000,
  scannedAt: '2026-11-10T15:00:00Z', durationMs: 123,
  status: 'healthy', errors: 0, warnings: 0, infos: 0,
  stats: commonStats, issues: [],
};
const warningBook = {
  ...cleanBook, path: '/books/where-the-light-goes.epub',
  filename: 'where-the-light-goes.epub', title: 'Where the Light Goes',
  author: 'Mara Williams', status: 'warning', warnings: 2, infos: 1,
  stats: { ...commonStats, words: 71280, estimatedPages: 286, sourcePages: 0 },
  issues: [
    {
      code: 'ZIP_MIMETYPE', severity: 'warning',
      title: 'Mimetype is not the first archive entry',
      detail: 'Repackaging can correct this without changing publication content.',
      file: 'mimetype', repairable: true,
    },
    {
      code: 'CHAPTER_LARGE', severity: 'warning',
      title: 'A chapter may be demanding for some readers',
      detail: 'This is a SpineGuard compatibility heuristic, not a documented Kobo limit.',
      file: 'EPUB/chapter-12.xhtml', repairable: false,
    },
    {
      code: 'PAGE_MARKERS', severity: 'info', title: 'No source page markers',
      detail: 'Estimated pages use your reading preferences.', repairable: false,
    },
  ],
};
const hostileBook = {
  ...cleanBook, path: '/books/broken.epub', filename: 'broken.epub',
  title: '<img src=x onerror="window.__xssExecuted=true">',
  author: '<script>window.__xssExecuted=true</script>',
  status: 'unreadable', errors: 1, stats: {},
  coverData: 'data:image/svg+xml,<svg onload="window.__xssExecuted=true"/>',
  issues: [{
    code: 'ZIP_INVALID', severity: 'error', title: '<b>Archive could not be opened</b>',
    detail: '<img src=x onerror="window.__xssExecuted=true">',
    file: '<script>window.__xssExecuted=true</script>', repairable: false,
  }],
};
const formalFailure = {
  ...cleanBook, path: '/books/formal-failure.epub', filename: 'formal-failure.epub',
  title: 'A Formal Finding', status: 'error', errors: 1,
  epubcheck: { status: 'failed', version: '5.4.0', detail: 'Formal validation found 1 error.' },
  issues: [{
    code: 'EPUBCHECK_RSC_005', severity: 'error', title: 'EPUBCheck: Invalid content',
    detail: 'A formal standards validation error.', file: 'EPUB/chapter.xhtml',
    line: 8, repairable: false,
  }],
};
const fixtureReports = [cleanBook, warningBook, hostileBook];

before(async () => {
  const files = new Map([
    ['/', ['index.html', 'text/html; charset=utf-8']],
    ['/index.html', ['index.html', 'text/html; charset=utf-8']],
    ['/app.js', ['app.js', 'text/javascript; charset=utf-8']],
    ['/styles.css', ['styles.css', 'text/css; charset=utf-8']],
  ]);
  server = createServer(async (req, res) => {
    const pathname = new URL(req.url, 'http://localhost').pathname;
    if (pathname === '/favicon.ico') { res.writeHead(204).end(); return; }
    const entry = files.get(pathname);
    if (!entry || !['GET', 'HEAD'].includes(req.method)) { res.writeHead(404).end(); return; }
    try {
      const body = await readFile(join(root, 'frontend', entry[0]));
      res.writeHead(200, { 'Content-Type': entry[1], 'Cache-Control': 'no-store' });
      res.end(req.method === 'HEAD' ? undefined : body);
    } catch {
      res.writeHead(500).end('Unable to read frontend resource');
    }
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  origin = `http://127.0.0.1:${server.address().port}`;
  const launchOptions = { headless: true };
  if (process.env.SPINEGUARD_BROWSER_EXECUTABLE) {
    launchOptions.executablePath = process.env.SPINEGUARD_BROWSER_EXECUTABLE;
  }
  try { browser = await chromium.launch(launchOptions); }
  catch (error) {
    throw new Error(`Cannot launch Chromium. Run "npm run install-browser" in tests/, or set SPINEGUARD_BROWSER_EXECUTABLE. ${error.message}`);
  }
});

after(async () => {
  await browser?.close();
  if (server) await new Promise(resolve => server.close(resolve));
});

async function openApp(t, config = {}, viewport = { width: 1440, height: 1000 }) {
  const context = await browser.newContext({ viewport });
  const page = await context.newPage();
  const errors = [];
  const externalRequests = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') errors.push(message.text());
  });
  await page.route('**/*', route => {
    if (route.request().url().startsWith(`${origin}/`)) return route.continue();
    externalRequests.push(route.request().url());
    return route.abort();
  });
  await page.addInitScript(({ reports, ...config }) => {
    const settings = {
      wordsPerPage: 250, readingWPM: 250, recursive: true,
      epubcheckJar: '', javaPath: 'java', runEpubcheck: false, theme: 'dark',
    };
    const handlers = {};
    const harness = window.__testHarness = {
      calls: [], handlers, reports, settings, scanTimer: null, resolveScan: null,
    };
    window.__xssExecuted = false;
    const invoke = (name, args = []) => {
      harness.calls.push({ name, args });
      if (config.failures?.[name]) throw new Error(config.failures[name]);
    };
    window.runtime = {
      EventsOn(name, handler) { handlers[name] = handler; },
      OnFileDrop(handler) { harness.drop = handler; },
    };
    window.spineguardPreview = {
      async GetState() {
        invoke('GetState');
        return { version: '1.0.0', settings, reports, warning: config.startupWarning || '' };
      },
      async SelectFiles() {
        invoke('SelectFiles');
        return config.selectedFiles ?? ['/books/new-book.epub'];
      },
      async SelectFolder() { invoke('SelectFolder'); return config.selectedFolder ?? '/books'; },
      async ScanPaths(paths) {
        invoke('ScanPaths', [paths]);
        handlers['scan:progress']?.({ path: paths[0], index: 0, total: 1 });
        return new Promise(resolve => {
          harness.resolveScan = resolve;
          harness.scanTimer = setTimeout(() => {
            const report = { ...config.newBook, path: paths[0] };
            handlers['scan:progress']?.({ path: paths[0], index: 1, total: 1, report });
            resolve([report]);
          }, config.scanDelay ?? 30);
        });
      },
      async CancelScan() {
        invoke('CancelScan');
        clearTimeout(harness.scanTimer);
        harness.resolveScan?.([]);
      },
      async RepairBook(path) {
        invoke('RepairBook', [path]);
        if (config.cancelRepair) return { outputPath: '', changes: [], report: {} };
        const source = reports.find(report => report.path === path);
        const outputPath = path.replace(/\.epub$/, '.repaired.epub');
        return {
          outputPath, changes: ['Normalized EPUB mimetype packaging.', 'Preserved all publication resources.'],
          report: {
            ...source, path: outputPath, filename: outputPath.split('/').pop(),
            errors: 0, warnings: 1, infos: 0, issues: [source.issues[1]],
          },
        };
      },
      async SaveSettings(next) {
        invoke('SaveSettings', [next]); Object.assign(settings, next); return next;
      },
      async SelectEPUBCheck() { invoke('SelectEPUBCheck'); return '/tools/epubcheck/epubcheck.jar'; },
      async RevealFile(path) { invoke('RevealFile', [path]); },
      async ExportReports(format) {
        invoke('ExportReports', [format]); return config.cancelExport ? '' : `/reports/scan.${format}`;
      },
      async ClearReports() { invoke('ClearReports'); },
    };
  }, { reports: fixtureReports, newBook: { ...cleanBook, title: 'A New Book', filename: 'new-book.epub' }, ...config });
  t.after(async () => {
    try {
      assert.deepEqual(errors, [], 'Frontend must not emit JavaScript or console errors');
      assert.deepEqual(externalRequests, [], 'Frontend must not request external resources');
    } finally { await context.close(); }
  });
  await page.goto(origin);
  await page.locator('.preview-banner').waitFor({ state: 'visible' });
  await page.waitForFunction(() => window.__testHarness.calls.some(call => call.name === 'GetState'));
  return page;
}

async function waitForCount(page, count) {
  await page.waitForFunction(expected => document.getElementById('total-books').textContent === String(expected), count);
}

async function callCount(page, name) {
  return page.evaluate(name => window.__testHarness.calls.filter(call => call.name === name).length, name);
}

async function assertFits(page) {
  const layout = await page.evaluate(() => ({
    horizontal: document.documentElement.scrollWidth > innerWidth,
    vertical: document.documentElement.scrollHeight > innerHeight,
    footerBottom: document.querySelector('.library-footer').getBoundingClientRect().bottom,
    viewportHeight: innerHeight,
  }));
  assert.equal(layout.horizontal, false, 'The app must not overflow horizontally');
  assert.equal(layout.vertical, false, 'Lists and inspector should scroll inside the window');
  assert.ok(layout.footerBottom <= layout.viewportHeight, 'The library footer must remain visible');
}

async function screenshot(page, name) {
  if (!process.env.SPINEGUARD_UI_SCREENSHOTS) return;
  await mkdir(process.env.SPINEGUARD_UI_SCREENSHOTS, { recursive: true });
  await page.screenshot({ path: join(process.env.SPINEGUARD_UI_SCREENSHOTS, `${name}.png`) });
}

test('empty state, desktop import, native drop, and keyboard shortcuts', async t => {
  const page = await openApp(t, { reports: [] });
  await waitForCount(page, 0);
  assert.equal(await page.locator('#empty-state').isVisible(), true);
  assert.equal(await page.locator('#export-button').isDisabled(), true);
  await screenshot(page, 'empty-dark');
  await page.getByRole('button', { name: 'Choose your first books', exact: true }).click();
  await waitForCount(page, 1);
  assert.equal(await page.locator('#empty-state').isVisible(), false);
  assert.equal(await callCount(page, 'ScanPaths'), 1);
  await page.keyboard.press('/');
  assert.equal(await page.locator('#search-input').evaluate(el => el === document.activeElement), true);
  await page.locator('#search-input').blur();
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+o' : 'Control+o');
  await page.waitForFunction(() => window.__testHarness.calls.filter(call => call.name === 'SelectFiles').length === 2);
  await page.locator('#scan-progress').waitFor({ state: 'hidden' });
  await page.evaluate(() => window.__testHarness.drop(0, 0, ['/books/dropped.epub']));
  await waitForCount(page, 2);
  assert.equal(await callCount(page, 'ScanPaths'), 3);
});

test('library filters, search, source-page distinction, and hostile metadata escaping', async t => {
  const page = await openApp(t);
  await waitForCount(page, 3);
  assert.equal(await page.locator('#healthy-books').textContent(), '1');
  assert.equal(await page.locator('#attention-books').textContent(), '2');
  await page.locator('#search-input').fill('evelyn');
  assert.equal(await page.locator('.book-row').count(), 1);
  await page.locator('#search-input').fill('no matching title');
  assert.equal(await page.locator('#no-results').isVisible(), true);
  await page.locator('#search-input').fill('');
  await page.locator('[data-filter=attention]').click();
  assert.equal(await page.locator('.book-row').count(), 1);
  await page.locator('.book-row').click();
  assert.match(await page.locator('.estimate-note').textContent(), /250 words per estimated page/);
  assert.match(await page.locator('.estimate-note').textContent(), /No source page markers supplied/);
  await page.locator('[data-filter=unreadable]').click();
  assert.equal(await page.locator('.book-row').count(), 1);
  await page.locator('.book-row').click();
  assert.equal(await page.locator('.book-detail h2').textContent(), hostileBook.title);
  assert.equal(await page.locator('.book-detail h2 img').count(), 0);
  assert.equal(await page.locator('.issue-list script, .issue-list img, .issue-list b').count(), 0);
  assert.equal(await page.locator('.book-detail .book-cover img').count(), 0, 'SVG cover data must not render');
  assert.equal(await page.evaluate(() => window.__xssExecuted), false);
  assert.equal(await page.locator('[data-detail-action=repair]').isDisabled(), true);
});

test('repairs preserve the original row and display unresolved findings in the new copy', async t => {
  const page = await openApp(t);
  await page.getByRole('button', { name: 'View Where the Light Goes', exact: true }).click();
  await page.getByRole('button', { name: 'Repair a copy', exact: true }).click();
  await page.locator('#result-dialog').waitFor({ state: 'visible' });
  await waitForCount(page, 4);
  const result = await page.locator('#repair-result').textContent();
  assert.match(result, /original file has not been changed/);
  assert.match(result, /1 finding still needs attention/);
  assert.match(result, /Normalized EPUB mimetype packaging/);
  assert.match(result, /where-the-light-goes\.repaired\.epub/);
  await page.getByRole('button', { name: 'Show repaired file', exact: true }).click();
  const reveal = await page.evaluate(() => window.__testHarness.calls.find(call => call.name === 'RevealFile'));
  assert.equal(reveal.args[0], '/books/where-the-light-goes.repaired.epub');
  await page.getByRole('button', { name: 'Back to library', exact: true }).click();
  assert.match(await page.locator('.issue-list').textContent(), /demanding for some readers/);
  assert.equal(await page.locator('[data-detail-action=repair]').isDisabled(), true);
});

test('preferences update estimates/theme; EPUBCheck browsing, both exports, and report clearing work', async t => {
  const page = await openApp(t);
  await page.getByRole('button', { name: 'Preferences', exact: true }).click();
  await page.locator('[name=wordsPerPage]').fill('300');
  await page.locator('[name=readingWPM]').fill('200');
  await page.locator('[name=theme]').selectOption('light');
  await page.getByRole('button', { name: 'Browse', exact: true }).click();
  assert.equal(await page.locator('[name=epubcheckJar]').inputValue(), '/tools/epubcheck/epubcheck.jar');
  await page.getByRole('button', { name: 'Save preferences', exact: true }).click();
  await page.locator('#settings-dialog').waitFor({ state: 'hidden' });
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'light');
  assert.match(await page.locator('.estimate-note').textContent(), /300 words per estimated page/);
  assert.equal(await page.locator('.detail-stat strong').nth(1).textContent(), '275');
  assert.equal(await page.locator('.detail-stat strong').nth(3).textContent(), '6h 53m');
  await screenshot(page, 'library-light');
  for (const format of ['JSON', 'CSV']) {
    await page.getByRole('button', { name: 'Export reports', exact: true }).click();
    await page.locator(`[data-export=${format.toLowerCase()}]`).click();
  }
  const exports = await page.evaluate(() => window.__testHarness.calls.filter(call => call.name === 'ExportReports').map(call => call.args[0]));
  assert.deepEqual(exports, ['json', 'csv']);
  await page.getByRole('button', { name: 'Clear library reports', exact: true }).click();
  await page.getByRole('button', { name: 'Keep reports', exact: true }).click();
  await waitForCount(page, 3);
  await page.getByRole('button', { name: 'Clear library reports', exact: true }).click();
  await page.getByRole('button', { name: 'Clear reports', exact: true }).click();
  await waitForCount(page, 0);
  assert.equal(await page.locator('#empty-state').isVisible(), true);
});

test('in-flight cancellation retains existing results and restores controls', async t => {
  const page = await openApp(t, { scanDelay: 30000 });
  await waitForCount(page, 3);
  await page.getByRole('button', { name: 'Add folder', exact: true }).click();
  await page.locator('#scan-progress').waitFor({ state: 'visible' });
  assert.equal(await page.locator('#add-button').isDisabled(), true);
  await page.getByRole('button', { name: 'Cancel scan', exact: true }).click();
  await page.locator('#scan-progress').waitFor({ state: 'hidden' });
  await waitForCount(page, 3);
  assert.equal(await page.locator('#add-button').isDisabled(), false);
  assert.equal(await callCount(page, 'CancelScan'), 1);
  assert.match(await page.locator('#toast-stack').textContent(), /Scan stopped/);
});

test('scan and repair failures remain reviewable and controls recover', async t => {
  const page = await openApp(t, { failures: {
    ScanPaths: 'The selected file could not be read.',
    RepairBook: 'This protected book cannot be safely repaired.',
  } });
  await page.getByRole('button', { name: 'Add EPUBs', exact: false }).click();
  await page.getByRole('alert').filter({ hasText: 'The selected file could not be read.' }).waitFor();
  assert.equal(await page.locator('#scan-progress').isVisible(), false);
  assert.equal(await page.locator('#add-button').isDisabled(), false);
  await waitForCount(page, 3);
  await page.getByRole('button', { name: 'View Where the Light Goes', exact: true }).click();
  await page.getByRole('button', { name: 'Repair a copy', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: 'cannot be safely repaired' }).waitFor();
  assert.equal(await page.locator('#result-dialog').isVisible(), false);
  assert.equal(await page.locator('[data-detail-action=repair]').isDisabled(), false);
  await waitForCount(page, 3);
});

test('cancelled native dialogs are quiet and warning messages are escaped', async t => {
  const page = await openApp(t, { selectedFiles: [], cancelRepair: true, cancelExport: true,
    startupWarning: 'Previous library could not be loaded.',
  });
  await page.getByRole('alert').filter({ hasText: 'Previous library could not be loaded.' }).waitFor();
  await page.getByRole('button', { name: 'Add EPUBs', exact: false }).click();
  assert.equal(await callCount(page, 'ScanPaths'), 0);
  await page.getByRole('button', { name: 'View Where the Light Goes', exact: true }).click();
  await page.getByRole('button', { name: 'Repair a copy', exact: true }).click();
  await page.waitForFunction(() => window.__testHarness.calls.some(call => call.name === 'RepairBook'));
  assert.equal(await page.locator('#result-dialog').isVisible(), false);
  await page.getByRole('button', { name: 'Export reports', exact: true }).click();
  await page.locator('[data-export=json]').click();
  assert.doesNotMatch(await page.locator('#toast-stack').textContent(), /Saved JSON/);
  await page.evaluate(() => window.__testHarness.handlers['app:warning']('<img src=x onerror="window.__xssExecuted=true">'));
  assert.equal(await page.locator('#toast-stack img').count(), 0);
  assert.equal(await page.evaluate(() => window.__xssExecuted), false);
  await waitForCount(page, 3);
});

test('formal EPUBCheck errors are clearly attributed and never appear as a healthy result', async t => {
  const page = await openApp(t, { reports: [formalFailure] });
  await waitForCount(page, 1);
  assert.equal(await page.locator('#healthy-books').textContent(), '0');
  assert.match(await page.locator('.health-summary').textContent(), /Issues detected/);
  assert.doesNotMatch(await page.locator('.health-summary').textContent(), /found no issues/);
  assert.match(await page.locator('.epubcheck-result').textContent(), /EPUBCheck.*failed.*5\.4\.0/);
  assert.match(await page.locator('.issue-list').textContent(), /EPUBCheck: Invalid content/);
  assert.match(await page.locator('.issue-file').textContent(), /chapter\.xhtml:8/);
});

test('layout fits the minimum and standard desktop window in both themes', async t => {
  const page = await openApp(t);
  await page.getByRole('button', { name: 'View Where the Light Goes', exact: true }).click();
  for (const viewport of [{ width: 980, height: 680 }, { width: 1440, height: 1000 }]) {
    await page.setViewportSize(viewport);
    for (const theme of ['dark', 'light']) {
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      await assertFits(page);
      assert.equal(await page.locator('[data-filter=unreadable]').isVisible(), true);
      const inspectorScrolls = await page.locator('#book-detail').evaluate(el => el.scrollHeight > el.clientHeight);
      assert.equal(inspectorScrolls, true, 'Long findings should scroll within the inspector');
      await screenshot(page, `library-${viewport.width}-${theme}`);
    }
  }
});
