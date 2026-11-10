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
      code: 'MIMETYPE_ORDER', severity: 'warning',
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
const scriptedBook = {
  ...cleanBook,path:'/books/scripted.epub',filename:'scripted.epub',title:'A Scripted Book',
  status:'warning',warnings:1,
  issues:[{code:'CONTENT_SCRIPT',severity:'warning',title:'Scripted content',detail:'This book contains a script.',file:'EPUB/chapter.xhtml',repairable:true,optionalRepair:true,repairKind:'reader_cleanup'}],
};
const metadataBook={
  ...cleanBook,path:'/books/missing-metadata.epub',filename:'missing-metadata.epub',title:'A Book With Missing Metadata',language:'',identifier:'',status:'error',errors:2,
  issues:[
    {code:'METADATA_LANGUAGE',severity:'error',title:'Publication language is missing',detail:'A language must be confirmed.',repairable:true,repairKind:'metadata',file:'EPUB/package.opf'},
    {code:'METADATA_IDENTIFIER',severity:'error',title:'Publication identifier is missing',detail:'The identifier cannot be recovered.',repairable:true,optionalRepair:true,repairKind:'metadata',file:'EPUB/package.opf'},
  ],
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
      currentSHA:config.sourceSHA||'a'.repeat(64),mutatedSource:false,
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
        return { version: '1.2.1', settings, reports, warning: config.startupWarning || '' };
      },
      async SelectFiles() {
        invoke('SelectFiles');
        return config.selectedFiles ?? ['/books/new-book.epub'];
      },
      async SelectFolder() { invoke('SelectFolder'); return config.selectedFolder ?? '/books'; },
      async ScanPaths(paths) {
        invoke('ScanPaths', [paths]);
        if(harness.previewRunning)throw new Error('A repair preview is still running.');
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
      async PreviewRepair(path,options) {
        invoke('PreviewRepair',[path,options]);
        if(config.invalidLanguage&&options.fixMetadata&&options.language===config.invalidLanguage)throw new Error('Enter a valid BCP 47 language tag, such as en or en-US.');
        harness.previewRunning=true;
        if(config.previewDelay)await new Promise(resolve=>setTimeout(resolve,config.previewDelay));
        harness.previewRunning=false;
        const source=config.previewReports?.[path]||reports.find(report=>report.path===path);
        let actions=config.planActions??(source.issues.some(issue=>issue.repairable&&(issue.code.startsWith('MIMETYPE_')||issue.code==='ZIP_PREFIX'))?[{kind:'packaging',file:'mimetype',description:'Normalize EPUB mimetype packaging.',count:1,requiresOptIn:false}]:[]);
        actions=actions.filter(action=>!(action.kind==='links'&&!options.fixLinks)&&!(action.kind==='duplicate_ids'&&!options.fixDuplicateIds)&&!(action.kind==='metadata'&&!options.fixMetadata)&&!(action.kind==='document_markup'&&!options.fixDocumentMarkup));
        if(config.missingLanguage&&options.fixMetadata&&options.language)actions=[...actions,{kind:'metadata',file:'EPUB/package.opf',description:`Set the missing language to ${options.language}.`,count:1,requiresOptIn:true}];
        if(config.missingIdentity&&options.fixMetadata&&options.generateIdentifier)actions=[...actions,{kind:'metadata',file:'EPUB/package.opf',description:'Create a new publication identifier.',count:1,requiresOptIn:true}];
        if(config.entities&&options.normalizeEntities)actions=[...actions,{kind:'entities',file:'EPUB/chapter.xhtml',description:'Normalize standard HTML entities to numeric references.',count:3,requiresOptIn:true}];
        const hasScripts=source.issues.some(issue=>issue.code==='CONTENT_SCRIPT'&&issue.repairable);
        if(options.removeScripts&&hasScripts)actions=[...actions,{kind:'reader_cleanup',file:'EPUB/chapter.xhtml',description:'Remove scripts and scripted actions.',count:3,requiresOptIn:true}];
        const unresolved=source.issues.filter(issue=>{
          if(options.removeScripts&&issue.code==='CONTENT_SCRIPT'&&issue.repairable)return false;
          if(options.fixMetadata&&options.language&&['METADATA_LANGUAGE','METADATA_LANGUAGE_INVALID'].includes(issue.code)&&issue.repairable)return false;
          if(options.fixMetadata&&options.generateIdentifier&&issue.code==='METADATA_IDENTIFIER'&&issue.repairable&&issue.optionalRepair)return false;
          if(issue.repairable&&!issue.optionalRepair){
            if((issue.code.startsWith('MIMETYPE_')||issue.code==='ZIP_PREFIX')&&actions.some(a=>a.kind==='packaging'))return false;
            if(['LINK_MISSING','LINK_FRAGMENT'].includes(issue.code)&&actions.some(a=>a.kind==='links'&&a.file===issue.file))return false;
            if(issue.code==='CONTENT_DUPLICATE_ID'&&actions.some(a=>a.kind==='duplicate_ids'&&a.file===issue.file))return false;
            if(['OPF_GUIDE_EMPTY','SVG_PRESERVE_ASPECT_RATIO'].includes(issue.code)&&actions.some(a=>a.kind==='document_markup'&&a.file===issue.file))return false;
          }
          return true;
        });
        return {path,sourceSha256:harness.currentSHA,options,actions:config.blockedReason?[]:actions,unresolved,blockedReason:config.blockedReason||'',suggestions:config.suggestions||{},report:source};
      },
      async RepairBookWithOptions(path,options) {
        invoke('RepairBookWithOptions', [path,options]);
        if(config.mutateSourceOnSave&&!harness.mutatedSource){harness.currentSHA='b'.repeat(64);harness.mutatedSource=true;}
        if(options.expectedSha256!==harness.currentSHA)throw new Error('The book changed since this review. Refresh the repair review.');
        if (config.cancelRepair) return { outputPath: '', changes: [], report: {} };
        const source = config.previewReports?.[path]||reports.find(report => report.path === path);
        const outputPath = path.replace(/\.epub$/, '.repaired.epub');
        const remaining=source.issues.filter(issue=>issue.severity==='warning'&&(!issue.repairable||issue.optionalRepair)&&!(options.removeScripts&&issue.code==='CONTENT_SCRIPT'&&issue.repairable));
        return {
          outputPath, changes: ['Normalized EPUB mimetype packaging.', 'Preserved all publication resources.'],
          report: {
            ...source, path: outputPath, filename: outputPath.split('/').pop(),
            status:remaining.length?'warning':'healthy',errors:0,warnings:remaining.length,infos:0,issues:remaining,
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

async function reviewAndSave(page){
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.locator('#save-repair-button').waitFor({state:'visible'});
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  await page.getByRole('button',{name:'Save repaired copy',exact:true}).click();
}

async function waitForReview(page){
  await page.waitForFunction(()=>document.getElementById('repair-review-dialog').getAttribute('aria-busy')==='false');
}

async function openAdvanced(page){
  await waitForReview(page);
  const advanced=page.locator('#repair-advanced');
  if(!await advanced.evaluate(el=>el.open))await advanced.locator('summary').click();
}

async function customLanguage(page,value){
  await page.locator('#repair-language-select').selectOption('custom');
  await page.locator('#repair-language').fill(value);
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
  // Let theme and hover color transitions finish before taking review images.
  await page.waitForTimeout(200);
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
  assert.equal(await page.locator('[data-detail-action=repair]').isDisabled(), false,'Saved findings should allow a fresh repair review');
});

test('repairs preserve the original row and display unresolved findings in the new copy', async t => {
  const page = await openApp(t);
  await page.getByRole('button', { name: 'View Where the Light Goes', exact: true }).click();
  await reviewAndSave(page);
  await page.locator('#result-dialog').waitFor({ state: 'visible' });
  await waitForCount(page, 4);
  const result = await page.locator('#repair-result').textContent();
  assert.match(result, /original file has not been changed/);
  assert.match(result, /1 finding still needs attention/);
  assert.match(result, /Normalized EPUB mimetype packaging/);
  assert.match(result, /where-the-light-goes\.repaired\.epub/);
  assert.match(await page.locator('#repair-result').innerText(),/Fix EPUB file structure/);
  assert.doesNotMatch(await page.locator('#repair-result').innerText(),/Normalized EPUB mimetype packaging/);
  assert.equal(await page.locator('#repair-result details').evaluate(el=>el.open),false);
  await page.getByRole('button', { name: 'Show repaired file', exact: true }).click();
  const reveal = await page.evaluate(() => window.__testHarness.calls.find(call => call.name === 'RevealFile'));
  assert.equal(reveal.args[0], '/books/where-the-light-goes.repaired.epub');
  await page.getByRole('button', { name: 'Back to library', exact: true }).click();
  assert.match(await page.locator('.issue-list').textContent(), /demanding for some readers/);
  assert.equal(await page.locator('[data-detail-action=repair]').isDisabled(), false,'Unresolved findings still have reviewable reasons');
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
    RepairBookWithOptions: 'This protected book cannot be safely repaired.',
  } });
  await page.getByRole('button', { name: 'Add EPUBs', exact: false }).click();
  await page.getByRole('alert').filter({ hasText: 'The selected file could not be read.' }).waitFor();
  assert.equal(await page.locator('#scan-progress').isVisible(), false);
  assert.equal(await page.locator('#add-button').isDisabled(), false);
  await waitForCount(page, 3);
  await page.getByRole('button', { name: 'View Where the Light Goes', exact: true }).click();
  await reviewAndSave(page);
  await page.getByRole('alert').filter({ hasText: 'cannot be safely repaired' }).waitFor();
  assert.equal(await page.locator('#result-dialog').isVisible(), false);
  assert.equal(await page.locator('#save-repair-button').isDisabled(), true);
  assert.equal(await page.locator('#refresh-repair-preview').isDisabled(), false);
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
  await reviewAndSave(page);
  await page.waitForFunction(() => window.__testHarness.calls.some(call => call.name === 'RepairBookWithOptions'));
  assert.equal(await page.locator('#result-dialog').isVisible(), false);
  assert.equal(await page.locator('#repair-review-dialog').isVisible(), true);
  await page.getByRole('button',{name:'Close repair review',exact:true}).click();
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
  assert.match(await page.locator('#book-detail .issue-file').textContent(), /chapter\.xhtml:8/);
});

test('repair review defaults preserve scripts and requires explicit reader cleanup',async t=>{
  const page=await openApp(t,{reports:[scriptedBook]});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(page);
  assert.equal(await page.locator('[name=removeScripts]').isChecked(),false);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.equal(await page.locator('#repair-script-choice').isVisible(),true);
  assert.match(await page.locator('#repair-script-choice').textContent(),/interactive features may stop working/);
  assert.doesNotMatch(await page.locator('#repair-plan-unresolved').textContent(),/Scripted content/,'The choice should not also appear as a manual repair');
  const initial=await page.evaluate(()=>window.__testHarness.calls.find(c=>c.name==='PreviewRepair'));
  assert.deepEqual(initial.args[1],{fixLinks:true,fixDuplicateIds:true,removeScripts:false,fixMetadata:true,language:'',generateIdentifier:false,fixDocumentMarkup:true,normalizeEntities:false});
  await page.locator('[name=removeScripts]').check();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.match(await page.locator('#repair-plan-actions').textContent(),/Remove interactive features/);
  assert.match(await page.locator('#repair-technical').textContent(),/Remove scripts and scripted actions/);
  await screenshot(page,'repair-review-cleanup');
  await page.getByRole('button',{name:'Save repaired copy',exact:true}).click();
  await page.locator('#result-dialog').waitFor({state:'visible'});
  const saved=await page.evaluate(()=>window.__testHarness.calls.find(c=>c.name==='RepairBookWithOptions'));
  assert.equal(saved.args[1].removeScripts,true);
  assert.equal(saved.args[1].expectedSha256,'a'.repeat(64));
  await waitForCount(page,2);
});

test('safe repair choices, unfixable findings, and protected-book reasons remain reviewable',async t=>{
  const page=await openApp(t,{reports:[formalFailure]});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(page);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.match(await page.locator('#repair-technical').textContent(),/EPUBCheck/);
  assert.match(await page.locator('#repair-technical').textContent(),/formal standards validation error/);
  assert.equal(await page.locator('#repair-technical').evaluate(el=>el.open),false);
  assert.equal(await callCount(page,'RepairBookWithOptions'),0);
  await page.getByRole('button',{name:'Close repair review',exact:true}).click();
  const protectedPage=await openApp(t,{reports:[warningBook],blockedReason:'This book has a digital signature; repackaging would invalidate it.'});
  await protectedPage.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(protectedPage);
  assert.match(await protectedPage.locator('#repair-technical').textContent(),/digital signature/);
  assert.equal(await protectedPage.locator('#save-repair-button').isDisabled(),true);
});

test('detected language stays a suggestion until selected and new identities remain opt-in',async t=>{
  const page=await openApp(t,{reports:[metadataBook],missingLanguage:true,missingIdentity:true,
    suggestions:{language:'en',languageSources:['EPUB/title.xhtml','<img src=x onerror="window.__xssExecuted=true">']},
  });
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.getByRole('button',{name:'Use English',exact:true}).waitFor();
  assert.equal(await page.locator('#repair-language').inputValue(),'');
  assert.equal(await page.locator('[name=generateIdentifier]').isChecked(),false);
  assert.equal(await page.locator('[name=normalizeEntities]').isChecked(),false);
  assert.equal(await page.locator('[name=fixMetadata]').isChecked(),true);
  assert.equal(await page.locator('[name=fixDocumentMarkup]').isChecked(),true);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.match(await page.locator('#language-suggestion-source').textContent(),/EPUB\/title.xhtml/);
  assert.equal(await page.locator('#language-suggestion-source img').count(),0);
  assert.equal(await page.evaluate(()=>window.__xssExecuted),false);
  const initial=await page.evaluate(()=>window.__testHarness.calls.find(c=>c.name==='PreviewRepair'));
  assert.equal(initial.args[1].language,'');
  assert.equal(await page.locator('#language-suggestion-source').isVisible(),false);
  await page.getByRole('button',{name:'Use English',exact:true}).click();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.equal(await page.locator('#repair-language').inputValue(),'en');
  assert.equal(await page.locator('#repair-language-select').inputValue(),'en');
  assert.match(await page.locator('#repair-technical').textContent(),/missing language to en/);
  await page.locator('[name=generateIdentifier]').check();
  await waitForReview(page);
  assert.match(await page.locator('#repair-technical').textContent(),/Create a new publication identifier/);
  await screenshot(page,'repair-metadata-review');
  await page.getByRole('button',{name:'Save repaired copy',exact:true}).click();
  await page.locator('#result-dialog').waitFor({state:'visible'});
  const saved=await page.evaluate(()=>window.__testHarness.calls.find(c=>c.name==='RepairBookWithOptions'));
  assert.deepEqual(saved.args[1],{fixLinks:true,fixDuplicateIds:true,removeScripts:false,fixMetadata:true,language:'en',generateIdentifier:true,fixDocumentMarkup:true,normalizeEntities:false,expectedSha256:'a'.repeat(64)});
});

test('language typing invalidates the previous plan immediately and debounces previews',async t=>{
  const page=await openApp(t,{reports:[metadataBook],missingLanguage:true,
    planActions:[{kind:'document_markup',file:'EPUB/package.opf',description:'Remove an empty optional guide.',count:1}],
  });
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  await page.locator('#repair-language-select').selectOption('custom');
  const initialCount=await callCount(page,'PreviewRepair');
  await page.locator('#repair-language').pressSequentially('en-US',{delay:20});
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.equal(await callCount(page,'PreviewRepair'),initialCount,'Typing should not start a native preview on every key');
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.equal(await callCount(page,'PreviewRepair'),initialCount+1);
  const latest=await page.evaluate(()=>window.__testHarness.calls.filter(c=>c.name==='PreviewRepair').at(-1));
  assert.equal(latest.args[1].language,'en-US');
  const pending=await openApp(t,{reports:[metadataBook],missingLanguage:true,previewDelay:150,
    planActions:[{kind:'document_markup',file:'EPUB/package.opf',description:'Remove an empty optional guide.',count:1}],
  });
  await pending.getByRole('button',{name:'Review repairs',exact:true}).click();
  await customLanguage(pending,'fr');
  await pending.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  const pendingCalls=await pending.evaluate(()=>window.__testHarness.calls.filter(c=>c.name==='PreviewRepair'));
  assert.equal(pendingCalls.length,2,'A pending old preview must wait for the debounced latest value');
  assert.equal(pendingCalls.at(-1).args[1].language,'fr');
});

test('invalid language feedback remains editable and disabling metadata gates its choices',async t=>{
  const page=await openApp(t,{reports:[metadataBook],missingLanguage:true,missingIdentity:true,invalidLanguage:'not a language',suggestions:{language:'en',languageSources:['EPUB/title.xhtml']}});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.getByRole('button',{name:'Use English',exact:true}).waitFor();
  await customLanguage(page,'not a language');
  await page.getByRole('alert').filter({hasText:'valid BCP 47 language tag'}).waitFor();
  assert.equal(await page.locator('#repair-language').isEditable(),true);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  await page.locator('#repair-language').fill('en');
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  await page.locator('[name=generateIdentifier]').check();
  await waitForReview(page);
  assert.match(await page.locator('#repair-technical').textContent(),/Create a new publication identifier/);
  await openAdvanced(page);
  await page.locator('[name=fixMetadata]').uncheck();
  await waitForReview(page);
  assert.equal(await page.locator('#repair-language').isDisabled(),true);
  assert.equal(await page.locator('[name=generateIdentifier]').isDisabled(),true);
  assert.equal(await page.locator('#use-detected-language').isDisabled(),true);
  const latest=await page.evaluate(()=>window.__testHarness.calls.filter(c=>c.name==='PreviewRepair').at(-1));
  assert.equal(latest.args[1].fixMetadata,false);
  assert.equal(latest.args[1].language,'en');
  assert.equal(latest.args[1].generateIdentifier,true,'The disabled choice is retained but gated by fixMetadata');
});

test('document markup is selected by default while valid-entity normalization requires a choice',async t=>{
  const page=await openApp(t,{reports:[warningBook],entities:true,
    planActions:[{kind:'document_markup',file:'EPUB/package.opf',description:'Remove an empty optional guide.',count:1}],
  });
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(page);
  assert.match(await page.locator('#repair-technical').textContent(),/Remove an empty optional guide/);
  assert.equal(await page.locator('[name=fixDocumentMarkup]').isChecked(),true);
  assert.equal(await page.locator('[name=normalizeEntities]').isChecked(),false);
  assert.doesNotMatch(await page.locator('#repair-technical').textContent(),/Normalize standard HTML entities/);
  await openAdvanced(page);
  await page.locator('[name=normalizeEntities]').check();
  await waitForReview(page);
  assert.match(await page.locator('#repair-technical').textContent(),/Normalize standard HTML entities to numeric references/);
  await page.locator('[name=fixDocumentMarkup]').uncheck();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.doesNotMatch(await page.locator('#repair-technical').textContent(),/empty optional guide/);
  await page.getByRole('button',{name:'Save repaired copy',exact:true}).click();
  await page.locator('#result-dialog').waitFor({state:'visible'});
  const saved=await page.evaluate(()=>window.__testHarness.calls.find(c=>c.name==='RepairBookWithOptions'));
  assert.equal(saved.args[1].fixDocumentMarkup,false);
  assert.equal(saved.args[1].normalizeEntities,true);
  assert.equal(saved.args[1].expectedSha256,'a'.repeat(64));
});

test('fresh review updates stale findings and preserves healthy-book repair explanation',async t=>{
  const corrected={...warningBook,status:'healthy',warnings:0,infos:0,issues:[]};
  const page=await openApp(t,{reports:[warningBook],previewReports:{[warningBook.path]:corrected}});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(page);
  assert.equal(await page.locator('#healthy-books').textContent(),'1');
  await page.getByRole('button',{name:'Close repair review',exact:true}).click();
  assert.equal(await page.locator('[data-detail-action=repair]').isDisabled(),true);
  assert.equal(await page.locator('[data-detail-action=repair]').getAttribute('title'),'No repairs needed: no findings were detected');
});

test('option changes invalidate pending plans and only the latest selection can be saved',async t=>{
  const page=await openApp(t,{reports:[scriptedBook],previewDelay:120,
    planActions:[{kind:'links',file:'EPUB/toc.xhtml',description:'Correct a link to its unique existing anchor.',count:1,requiresOptIn:false}],
  });
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  await page.locator('[name=removeScripts]').check();
  await page.locator('[name=removeScripts]').uncheck();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.doesNotMatch(await page.locator('#repair-plan-actions').textContent(),/Remove scripts/);
  const previews=await page.evaluate(()=>window.__testHarness.calls.filter(c=>c.name==='PreviewRepair'));
  assert.equal(previews.at(-1).args[1].removeScripts,false);
  await openAdvanced(page);
  await page.locator('[name=fixLinks]').uncheck();
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  await waitForReview(page);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
});

test('changed source files require a refreshed review before another save',async t=>{
  const page=await openApp(t,{reports:[warningBook],mutateSourceOnSave:true});
  await reviewAndSave(page);
  await page.getByRole('alert').filter({hasText:'book changed since this review'}).waitFor();
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.equal(await page.locator('#result-dialog').isVisible(),false);
  await page.getByRole('button',{name:'Refresh review',exact:true}).click();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  await page.getByRole('button',{name:'Save repaired copy',exact:true}).click();
  await page.locator('#result-dialog').waitFor({state:'visible'});
  const calls=await page.evaluate(()=>window.__testHarness.calls.filter(c=>c.name==='RepairBookWithOptions'));
  assert.equal(calls[0].args[1].expectedSha256,'a'.repeat(64));
  assert.equal(calls[1].args[1].expectedSha256,'b'.repeat(64));
});

test('preview failures recover and repair proposal metadata remains escaped',async t=>{
  const failed=await openApp(t,{reports:[warningBook],failures:{PreviewRepair:'The book is no longer at its saved path.'}});
  await failed.getByRole('button',{name:'Review repairs',exact:true}).click();
  await failed.getByRole('alert').filter({hasText:'no longer at its saved path'}).waitFor();
  assert.equal(await failed.locator('#save-repair-button').isDisabled(),true);
  assert.equal(await failed.locator('#refresh-repair-preview').isDisabled(),false);
  const page=await openApp(t,{reports:[warningBook],planActions:[{kind:'links',file:'<script>alert(1)</script>',description:'<img src=x onerror="window.__xssExecuted=true">',count:1}]});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.equal(await page.locator('#repair-review-dialog img, #repair-review-dialog script').count(),0);
  assert.match(await page.locator('#repair-technical').textContent(),/<img src=x/);
  assert.equal(await page.evaluate(()=>window.__xssExecuted),false);
});

test('rescan library sends every saved path and is disabled while scanning or empty',async t=>{
  const page=await openApp(t,{scanDelay:30000});
  await page.getByRole('button',{name:'Rescan library',exact:true}).click();
  await page.locator('#scan-progress').waitFor({state:'visible'});
  const scan=await page.evaluate(()=>window.__testHarness.calls.find(c=>c.name==='ScanPaths'));
  assert.deepEqual(scan.args[0],fixtureReports.map(r=>r.path));
  assert.equal(await page.locator('#rescan-library-button').isDisabled(),true);
  await page.getByRole('button',{name:'Cancel scan',exact:true}).click();
  await page.locator('#scan-progress').waitFor({state:'hidden'});
  assert.equal(await page.locator('#rescan-library-button').isDisabled(),false);
  const empty=await openApp(t,{reports:[]});
  assert.equal(await empty.locator('#rescan-library-button').isDisabled(),true);
});

test('repair review fits a compact desktop and closing a pending review cannot apply changes',async t=>{
  const page=await openApp(t,{reports:[scriptedBook],previewDelay:150},{width:980,height:680});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.getByRole('button',{name:'Close repair review',exact:true}).click();
  await page.evaluate(()=>window.__testHarness.drop(0,0,['/books/queued.epub']));
  await waitForCount(page,2);
  assert.equal(await page.locator('#repair-review-dialog').isVisible(),false);
  assert.equal(await callCount(page,'RepairBookWithOptions'),0);
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(page);
  await page.locator('[name=removeScripts]').check();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  for(const theme of ['dark','light']){
    await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;},theme);
    const bounds=await page.locator('#repair-review-dialog').evaluate(el=>{
      const dialog=el.getBoundingClientRect(),footer=el.querySelector('.modal-footer').getBoundingClientRect();
      return {left:dialog.left,right:dialog.right,top:dialog.top,bottom:dialog.bottom,footer:footer.bottom,width:innerWidth,height:innerHeight};
    });
    assert.ok(bounds.left>=0&&bounds.right<=bounds.width&&bounds.top>=0&&bounds.bottom<=bounds.height);
    assert.ok(bounds.footer<=bounds.height,'Save controls must stay within the viewport');
    assert.equal(await page.locator('#save-repair-button').isVisible(),true);
    await screenshot(page,`repair-review-980-${theme}`);
  }
});

test('a language-only repair asks one clear question and fits without scrolling',async t=>{
  const book={...metadataBook,identifier:'urn:uuid:existing',errors:1,issues:metadataBook.issues.slice(0,1)};
  const page=await openApp(t,{reports:[book],missingLanguage:true,suggestions:{language:'en',languageSources:['EPUB/title.xhtml (xml:lang=en)']}},{width:980,height:680});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.getByRole('button',{name:'Use English',exact:true}).waitFor();
  await waitForReview(page);
  assert.equal(await page.locator('#repair-language-choice').isVisible(),true);
  assert.equal(await page.locator('#repair-language-select').inputValue(),'');
  assert.equal(await page.locator('#repair-language').isVisible(),false);
  assert.equal(await page.locator('#repair-identity-choice').isVisible(),false);
  assert.equal(await page.locator('#repair-script-choice').isVisible(),false);
  assert.equal(await page.locator('#repair-advanced').evaluate(el=>el.open),false);
  assert.equal(await page.locator('#repair-technical').evaluate(el=>el.open),false);
  assert.equal(await page.locator('#refresh-repair-preview').isVisible(),false);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.doesNotMatch(await page.locator('#repair-plan-unresolved').innerText(),/language/i,'Do not repeat a question as an unfixable problem');
  const visible=await page.locator('#repair-review-dialog .modal-body').innerText();
  assert.doesNotMatch(visible,/identifier|HTML entit|markup|resource paths|xml:lang|title\.xhtml/i);
  for(const theme of ['dark','light']){
    await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;},theme);
    assert.equal(await page.locator('#repair-review-dialog .modal-body').evaluate(el=>el.scrollHeight>el.clientHeight+1),false,'A single language question should not require scrolling');
    await screenshot(page,`repair-language-simple-980-${theme}`);
  }
  await page.getByRole('button',{name:'Use English',exact:true}).click();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.equal(await page.locator('#repair-language-choice').isVisible(),true,'A successful preview must leave the choice editable');
  assert.equal(await page.locator('#repair-language-select').inputValue(),'en');
  await page.locator('#repair-language-select').focus();
  await page.locator('#repair-language-select').selectOption('fr');
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.equal(await page.locator('#repair-language-select').evaluate(el=>el===document.activeElement),true,'Refreshing a preview must preserve keyboard focus');
  const latest=await page.evaluate(()=>window.__testHarness.calls.filter(c=>c.name==='PreviewRepair').at(-1));
  assert.equal(latest.args[1].language,'fr');
  assert.equal(latest.args[1].generateIdentifier,false);
  assert.equal(latest.args[1].removeScripts,false);
});

test('ready repairs show actual fixes and hide unrelated choices and incidental packaging',async t=>{
  const book={...cleanBook,status:'error',errors:1,issues:[{code:'LINK_MISSING',severity:'error',title:'Linked resource is missing',detail:'A cover path needs correction.',file:'EPUB/cover.xhtml',repairable:true,repairKind:'links'}]};
  const page=await openApp(t,{reports:[book],planActions:[
    {kind:'packaging',description:'Write selected edits into a new EPUB archive.',count:1,requiresOptIn:false},
    {kind:'links',file:'EPUB/cover.xhtml',description:'Correct 1 internal link using its proven existing image.',count:1,requiresOptIn:false},
  ]});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.equal(await page.locator('#repair-language-choice').isVisible(),false);
  assert.equal(await page.locator('#repair-identity-choice').isVisible(),false);
  assert.equal(await page.locator('#repair-script-choice').isVisible(),false);
  assert.equal(await page.locator('#repair-advanced').evaluate(el=>el.open),false);
  assert.equal(await page.locator('#repair-technical').evaluate(el=>el.open),false);
  assert.match(await page.locator('#repair-plan-actions').innerText(),/Fix broken links/);
  assert.doesNotMatch(await page.locator('#repair-plan-actions').innerText(),/packag|archive|cover\.xhtml|2 proposed/i);
  assert.match(await page.locator('#repair-technical').textContent(),/Write selected edits into a new EPUB archive/);
  await screenshot(page,'repair-ready-simple');
});

test('protected metadata does not offer a language or identifier choice that cannot be applied',async t=>{
  const book={...metadataBook,issues:metadataBook.issues.map(issue=>({...issue,repairable:false,repairReason:'Metadata edits are disabled for this font-obfuscated book to preserve identifier consistency and font keys.'}))};
  const page=await openApp(t,{reports:[book],suggestions:{language:'en',languageSources:['EPUB/title.xhtml']}});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(page);
  assert.equal(await page.locator('#repair-language-choice').isVisible(),false);
  assert.equal(await page.locator('#repair-identity-choice').isVisible(),false);
  assert.equal(await page.locator('#use-detected-language').isVisible(),false);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.equal(await page.locator('#repair-technical').evaluate(el=>el.open),false);
  assert.match(await page.locator('#repair-technical').textContent(),/font-obfuscated/);
  assert.match(await page.locator('#repair-plan-unresolved').innerText(),/language|identifier/i);
});

test('an optional script choice never hides a separate unrepairable script finding',async t=>{
  const blockedScript={...scriptedBook.issues[0],file:'EPUB/uneditable.xhtml',repairable:false,repairReason:'This document cannot be edited safely.'};
  const book={...scriptedBook,warnings:2,issues:[...scriptedBook.issues,blockedScript]};
  const page=await openApp(t,{reports:[book]});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await waitForReview(page);
  assert.equal(await page.locator('#repair-script-choice').isVisible(),true);
  assert.match(await page.locator('#repair-plan-unresolved').innerText(),/Still needs attention.*Scripted content/s);
  assert.equal(await page.locator('#repair-plan-unresolved .review-unresolved').count(),1);
  await page.locator('[name=removeScripts]').check();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.match(await page.locator('#repair-plan-unresolved').innerText(),/Still needs attention.*Scripted content/s);
  assert.equal(await page.locator('#repair-plan-unresolved .review-unresolved').count(),1);
  assert.match(await page.locator('#repair-technical').textContent(),/uneditable\.xhtml/);
  await page.getByRole('button',{name:'Save repaired copy',exact:true}).click();
  await page.locator('#result-dialog').waitFor({state:'visible'});
  assert.match(await page.locator('#repair-result').innerText(),/1 finding still needs attention/);
});

test('switching a supported fix off explains how to restore it instead of claiming it is impossible',async t=>{
  const book={...cleanBook,status:'error',errors:1,issues:[{code:'LINK_MISSING',severity:'error',title:'Linked resource is missing',file:'EPUB/cover.xhtml',repairable:true,repairKind:'links'}]};
  const page=await openApp(t,{reports:[book],planActions:[{kind:'links',file:'EPUB/cover.xhtml',description:'Correct the link to its existing resource.',count:1}]});
  await page.getByRole('button',{name:'Review repairs',exact:true}).click();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  await openAdvanced(page);
  await page.locator('[name=fixLinks]').uncheck();
  await waitForReview(page);
  assert.equal(await page.locator('#save-repair-button').isDisabled(),true);
  assert.match(await page.locator('#repair-plan-summary').innerText(),/Repairs are switched off/);
  assert.match(await page.locator('#repair-plan-unresolved').innerText(),/Turn it back on in Advanced options/);
  assert.doesNotMatch(await page.locator('#repair-plan-unresolved').innerText(),/cannot repair|could not be found|fresh download/i);
  await page.locator('[name=fixLinks]').check();
  await page.waitForFunction(()=>!document.getElementById('save-repair-button').disabled);
  assert.equal(await page.locator('#repair-plan-unresolved .review-unresolved').count(),0);
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
