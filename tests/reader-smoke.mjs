import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { spawn } from 'node:child_process';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// These tests render the production reader against the real Go publication
// manager and sanitizer. Only the desktop API transport is adapted for HTTP.
// The Go helper creates all books in its own disposable temporary directory.
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.SPINEGUARD_PLAYWRIGHT_MODULE || 'playwright');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const executable = join(root, '.tools', `reader-test-host-${process.pid}${process.platform === 'win32' ? '.exe' : ''}`);
let host;
let browser;
let origin;
let hostOutput = '';

async function run(command, args) {
  const child = spawn(command, args, { cwd: root, env: process.env, windowsHide: true });
  let output = '';
  child.stdout.on('data', data => { output += data; });
  child.stderr.on('data', data => { output += data; });
  await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', code => code === 0 ? resolve() : reject(new Error(`${command} exited ${code}: ${output}`)));
  });
}

async function json(path, options) {
  const response = await fetch(`${origin}${path}`, options);
  const value = await response.json();
  if (!response.ok) throw new Error(value.error || `HTTP ${response.status}`);
  return value;
}

const api = (method, ...args) => json('/__test/api', {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ method, args }),
});

before(async () => {
  await mkdir(dirname(executable), { recursive: true });
  await run(process.env.SPINEGUARD_GO_EXECUTABLE || 'go', ['build', '-o', executable, './tests/reader-host']);
  host = spawn(executable, [join(root, 'frontend')], { cwd: root, env: process.env, windowsHide: true });
  origin = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`Reader host did not start: ${hostOutput}`)), 20_000);
    host.stdout.on('data', data => {
      hostOutput += data;
      const line = hostOutput.split(/\r?\n/).find(line => line.startsWith('{"origin":'));
      if (line) { clearTimeout(timer); resolve(JSON.parse(line).origin); }
    });
    host.stderr.on('data', data => { hostOutput += data; });
    host.once('error', error => { clearTimeout(timer); reject(error); });
    host.once('exit', code => { clearTimeout(timer); reject(new Error(`Reader host exited ${code}: ${hostOutput}`)); });
  });
  const launchOptions = { headless: true };
  if (process.env.SPINEGUARD_BROWSER_EXECUTABLE) launchOptions.executablePath = process.env.SPINEGUARD_BROWSER_EXECUTABLE;
  browser = await chromium.launch(launchOptions);
});

after(async () => {
  await browser?.close();
  if (host?.exitCode === null) {
    await json('/__test/shutdown').catch(() => {});
    await new Promise(resolve => {
      const timer = setTimeout(() => { host.kill(); }, 4_000);
      host.once('exit', () => { clearTimeout(timer); resolve(); });
      if (host.exitCode !== null) { clearTimeout(timer); resolve(); }
    });
  }
  await rm(executable, { force: true, maxRetries: 5, retryDelay: 100 });
});

async function openApp(t, config = {}) {
  await json('/__test/reset', { method: 'POST' });
  const state = await json('/__test/state');
  const context = await browser.newContext({ viewport: config.viewport || { width: 1440, height: 960 } });
  const page = await context.newPage();
  const errors = [];
  const externalRequests = [];
  page.on('pageerror', error => errors.push(error.message));
  await context.route('**/*', route => {
    const url = route.request().url();
    if (url.startsWith(`${origin}/`) || url.startsWith('blob:') || url.startsWith('data:')) return route.continue();
    externalRequests.push(url);
    return route.abort();
  });
  await page.addInitScript(({ state, config }) => {
    const reports = config.kinds ? state.reports.filter(book => config.kinds.some(kind => state.paths[kind] === book.path)) : state.reports;
    const harness = window.__readerHarness = { calls: [], reports, paths: state.paths, lastBook: null, bridgeCalls: 0, sourceRequests:0, saveRequests:0, searchRequests:0 };
    window.__bookExecuted = false;
    window.go = { main: { App: { DangerousBookCall() { harness.bridgeCalls++; } } } };
    const listeners=new Map();
    window.runtime = {
      EventsOn(name,callback){const group=listeners.get(name)||[];group.push(callback);listeners.set(name,group);return ()=>listeners.set(name,(listeners.get(name)||[]).filter(entry=>entry!==callback));},
      OnFileDrop() {},WindowSetTitle() {},WindowShow() {},WindowCenter() {},
    };
    harness.emit=(name,...args)=>{for(const listener of listeners.get(name)||[])listener(...args);};
    const call = async (method, args) => {
      const record={ name: method, args, done:false };harness.calls.push(record);
      const sourceIndex=method==='ReadReaderSource'?harness.sourceRequests++:-1;
      const searchIndex=method==='SearchReader'?harness.searchRequests++:-1;
      if(method==='SearchReader')record.pendingStops=harness.calls.filter(call=>call.name==='CancelReaderSearch'&&!call.done).length;
      if(method==='CancelReaderSearch'&&config.cancelSearchDelay)await new Promise(resolve=>setTimeout(resolve,config.cancelSearchDelay));
      if(method==='SaveReaderState'){
        const delay=config.saveDelays?.[harness.saveRequests++]||0;
        if(delay)await new Promise(resolve=>setTimeout(resolve,delay));
      }
      const response = await fetch('/__test/api', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ method, args }),
      });
      const value = await response.json();
      if (!response.ok) throw new Error(value.error);
      if (method === 'OpenReader') {
        harness.lastBook = value;
        if (config.openDelay) await new Promise(resolve => setTimeout(resolve, config.openDelay));
      }
      if(method==='ReadReaderSource'){
        const delay=config.sourceDelays?.[sourceIndex]||0;
        if(delay)await new Promise(resolve=>setTimeout(resolve,delay));
      }
      if(method==='SearchReader'){
        const delay=config.searchDelays?.[searchIndex]||0;
        if(delay)await new Promise(resolve=>setTimeout(resolve,delay));
      }
      record.done=true;
      return value;
    };
    const readerAPI=Object.fromEntries(['OpenReader', 'CancelReaderOpen', 'CancelReaderSearch', 'CloseReader', 'SaveReaderState', 'SearchReader', 'LocateReaderIssue', 'ReadReaderSource'].map(method => [method, (...args) => call(method, args)]));
    const adapter = {
      async GetState() { return { ...state, reports, settings: { ...state.settings, theme: config.appTheme || 'dark' } }; },
      async GetBookCover() { return ''; },
      async SaveSettings(settings) { return settings; },
      async RevealBook() {}, async OpenExternal() {},
      async OpenReaderWindow(path,issueIndex=-1){harness.calls.push({name:'OpenReaderWindow',args:[path,issueIndex],done:true});if(!config.launchDetachedOnly)void window.SpineGuardReader.open(path,issueIndex);},
      ...readerAPI,
    };
    if(config.standalone){
      delete window.go.main.App;
      window.go.main.ReaderWindow={...readerAPI,
        async GetReaderLaunch(){harness.calls.push({name:'GetReaderLaunch',args:[],done:true});return {path:state.paths[config.readerKind||'normal'],issueIndex:config.issueIndex??-1,theme:config.appTheme||'dark'};},
        async RequestClose(){harness.calls.push({name:'RequestClose',args:[],done:true});harness.emit('reader:before-close');},
        async CloseWindow(){harness.calls.push({name:'CloseWindow',args:[],done:true});},
      };
    }else window.spineguardPreview=adapter;
  }, { state, config });
  t.after(async () => {
    await page.evaluate(() => window.SpineGuardReader?.close()).catch(() => {});
    await context.close();
    assert.deepEqual(errors, [], 'reader must not raise unhandled JavaScript errors');
    assert.deepEqual(externalRequests, [], 'EPUB resources must not request external network access');
  });
  await page.goto(config.standalone?`${origin}/reader.html`:origin);
  await page.waitForFunction(standalone => !!window.SpineGuardReader && (standalone?document.querySelector('#reader-dialog')?.open:document.querySelector('#book-rows')?.children.length > 0),!!config.standalone);
  return { page, state, context };
}

async function openBook(page, state, kind = 'normal', issueIndex = -1) {
  await page.evaluate(({ path, issueIndex }) => { void window.SpineGuardReader.open(path, issueIndex); }, { path: state.paths[kind], issueIndex });
  await page.locator('#reader-dialog').waitFor({ state: 'visible' });
  await page.waitForFunction(() => !!document.querySelector('#reader-stage iframe')?.contentDocument?.body?.textContent?.trim());
}

async function expectChapter(page, text) {
  try {
    await page.waitForFunction(text => [...document.querySelectorAll('#reader-stage iframe')].some(frame => frame.contentDocument?.body?.textContent?.includes(text)), text);
  } catch (error) {
    const diagnostic = await page.evaluate(() => ({
      notice: document.querySelector('#reader-notice')?.textContent,
      progress: document.querySelector('#reader-progress')?.textContent,
      frames: [...document.querySelectorAll('#reader-stage iframe')].map(frame => ({ url: frame.src, text: frame.contentDocument?.body?.textContent?.slice(0, 250), link:frame.contentDocument?.querySelector('#internal-link')?.outerHTML })),
    }));
    throw new Error(`${error.message}\nReader diagnostic: ${JSON.stringify(diagnostic)}`);
  }
}

async function choosePane(page, pane) {
  await page.locator(`[data-reader-pane="${pane}"]`).click();
}

async function unchanged() {
  const evidence = await json('/__test/evidence');
  assert.ok(Object.values(evidence).every(value => value.unchanged), 'reading, searching, and bookmarking must preserve original EPUB bytes');
}

async function paintedCover(page,stage){
  const deadline=Date.now()+5_000;let png,pixels;
  do{
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    png=await page.screenshot();
    pixels=await json('/__test/pixels',{method:'POST',headers:{'Content-Type':'image/png'},body:png});
    if(pixels.green.count>stage.width*stage.height*0.4&&pixels.marker.count>100)break;
  }while(Date.now()<deadline);
  assert.ok(pixels.green.count>stage.width*stage.height*0.4,'the actual cover must paint on the first page without requiring a resize: '+JSON.stringify(pixels));
  assert.ok(Math.abs(pixels.green.width/pixels.green.height-2/3)<0.015,'the painted cover must retain its portrait proportions: '+JSON.stringify(pixels));
  assert.ok(Math.abs(pixels.marker.width/pixels.marker.height-1)<0.06,'the round artwork marker must remain round: '+JSON.stringify(pixels));
  return {png,pixels};
}

test('real Manager preserves local resources, rejects traversal and expires closed sessions', async () => {
  await json('/__test/reset', { method: 'POST' });
  const state = await json('/__test/state');
  const book = await api('OpenReader', state.paths.normal);
  assert.equal(book.spine.length, 2);
  assert.ok(book.packageUrl.startsWith(`/__reader/${book.sessionId}/`));
  const opf = await fetch(`${origin}${book.packageUrl}`);
  assert.equal(opf.status, 200);
  const xhtml = book.resources.find(resource => resource.path.endsWith('chapter-one.xhtml'));
  const image = book.resources.find(resource => resource.path.endsWith('cover.png'));
  const css = book.resources.find(resource => resource.path.endsWith('book.css'));
  assert.ok((await (await fetch(`${origin}${xhtml.url}`)).text()).includes('Publisher styles are retained.'));
  assert.equal((await fetch(`${origin}${image.url}`)).headers.get('content-type'), 'image/png');
  assert.ok((await (await fetch(`${origin}${css.url}`)).text()).includes('text-indent'));
  for (const path of [
    `/__reader/${book.sessionId}/index.html`,
    `/__reader/${book.sessionId}/`,
    `/__reader/${book.sessionId}/r/%2e%2e%2fbook.opf`,
    `/__reader/${book.sessionId}/r/%2e%2e%5cbook.opf`,
    '/__reader/not-a-session/package.opf',
  ]) assert.notEqual((await fetch(`${origin}${path}`)).status, 200, path);
  await api('CloseReader', book.sessionId);
  assert.notEqual((await fetch(`${origin}${book.packageUrl}`)).status, 200);
  const hostile = await api('OpenReader', state.paths.hostile);
  const hostileChapter = hostile.spine[0];
  const passive = await (await fetch(`${origin}${hostileChapter.url}`)).text();
  for (const style of passive.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)) {
    assert.doesNotMatch(style[1], /outside\.invalid|javascript:/i, 'CSS custom properties must not bypass resource filtering');
  }
  await api('CloseReader', hostile.sessionId);
  await unchanged();
});

test('EPUB3 opens with publisher styling, local image, paging, contents and search', async t => {
  const { page, state } = await openApp(t, { kinds: ['normal'] });
  await page.locator('[data-detail-action="read"]').click();
  await page.waitForFunction(()=>window.__readerHarness.calls.some(call=>call.name==='OpenReaderWindow'));
  await page.locator('#reader-dialog').waitFor({ state: 'visible' });
  await expectChapter(page, 'First chapter');
  await page.waitForFunction(() => document.querySelector('#reader-stage iframe')?.contentDocument?.querySelector('#local-image')?.naturalWidth === 36);
  const presentation = await page.locator('#reader-stage iframe').first().evaluate(frame => {
    const doc = frame.contentDocument;
    const paragraph = doc.querySelector('#publisher-styled');
    const image = doc.querySelector('#local-image');
    return { indent: doc.defaultView.getComputedStyle(paragraph).textIndent, imageWidth: image.naturalWidth, sandbox: frame.getAttribute('sandbox') };
  });
  assert.notEqual(presentation.indent, '0px');
  assert.equal(presentation.imageWidth, 36);
  assert.ok(!presentation.sandbox.split(/\s+/).includes('allow-scripts'));
  const initial = await page.locator('#reader-progress').textContent();
  await page.locator('#reader-next').click();
  await page.waitForFunction(previous => document.querySelector('#reader-progress').textContent !== previous, initial);
  await page.locator('#reader-prev').click();
  await page.locator('#reader-stage iframe').first().contentFrame().locator('body').click({ position: { x: 10, y: 10 } });
  const beforeKey = await page.locator('#reader-progress').textContent();
  await page.keyboard.press('ArrowRight');
  await page.waitForFunction(previous => document.querySelector('#reader-progress').textContent !== previous, beforeKey);
  await choosePane(page, 'contents');
  await page.locator('#reader-toc').getByRole('button', { name: 'Second chapter', exact: true }).click();
  await expectChapter(page, 'The unique silver river phrase');
  await choosePane(page, 'search');
  await page.locator('#reader-search-input').fill('silver river');
  await page.locator('#reader-search-button').click();
  await page.locator('#reader-search-results button').first().waitFor();
  assert.match(await page.locator('#reader-search-results').textContent(), /silver river/i);
  await page.locator('#reader-search-results button').first().click();
  await expectChapter(page, 'The unique silver river phrase');
  await page.locator('#reader-settings-button').click();
  await page.locator('#reader-font-size').selectOption('22');
  await page.locator('#reader-font-family').selectOption('sans');
  await page.locator('#reader-line-height').selectOption('1.8');
  await page.locator('#reader-theme').selectOption('sepia');
  await page.locator('#reader-flow').selectOption('scrolled-doc');
  await expectChapter(page, 'The unique silver river phrase');
  const font = await page.locator('#reader-stage iframe').first().evaluate(frame => frame.contentDocument.defaultView.getComputedStyle(frame.contentDocument.body).fontSize);
  assert.equal(font, '22px');
  await page.locator('#reader-licenses').click();
  await page.locator('#reader-license-dialog').waitFor({state:'visible'});
  await page.keyboard.press('Escape');
  await page.locator('#reader-license-dialog').waitFor({state:'hidden'});
  assert.equal(await page.locator('#reader-dialog').isVisible(),true);
  await unchanged();
});

test('library Read requests a detached reader without covering or blocking the library', async t=>{
  const {page,state}=await openApp(t,{kinds:['normal'],launchDetachedOnly:true});
  await page.locator('[data-detail-action="read"]').click();
  await page.waitForFunction(()=>window.__readerHarness.calls.some(call=>call.name==='OpenReaderWindow'));
  const launch=await page.evaluate(()=>window.__readerHarness.calls.find(call=>call.name==='OpenReaderWindow'));
  assert.equal(launch.args[0],state.paths.normal);
  assert.equal(launch.args[1],-1);
  assert.equal(await page.locator('#reader-dialog').isVisible(),false);
  assert.equal(await page.locator('#add-button').isEnabled(),true);
  assert.equal(await page.locator('#rescan-library-button').isEnabled(),true);
  await unchanged();
});

test('EPUB2 NCX contents and in-book links navigate between reading-order documents', async t => {
  const { page, state } = await openApp(t, { kinds: ['legacy'] });
  await openBook(page, state, 'legacy');
  await choosePane(page, 'contents');
  await page.locator('#reader-toc').getByRole('button', { name: 'First chapter', exact: true }).waitFor();
  await page.locator('#reader-stage iframe').first().contentFrame().locator('#internal-link').click();
  await expectChapter(page, 'The unique silver river phrase');
  await page.locator('#reader-toc').getByRole('button', { name: 'First chapter', exact: true }).click();
  await expectChapter(page, 'Publisher styles are retained.');
  await unchanged();
});

test('bookmark notes and reading position survive close and reopen', async t => {
  const { page, state } = await openApp(t, { kinds: ['normal'] });
  await openBook(page, state);
  await choosePane(page, 'contents');
  await page.locator('#reader-toc').getByRole('button', { name: 'Second chapter', exact: true }).click();
  await expectChapter(page, 'The unique silver river phrase');
  await page.locator('#reader-bookmark-button').click();
  await choosePane(page, 'bookmarks');
  await page.locator('[data-bookmark-note]').fill('Check the river reference.');
  await page.locator('[data-save-bookmark-note]').click();
  await page.locator('#reader-close').click();
  await page.locator('#reader-dialog').waitFor({ state: 'hidden' });
  await openBook(page, state);
  await expectChapter(page, 'The unique silver river phrase');
  await choosePane(page, 'bookmarks');
  assert.equal(await page.locator('[data-bookmark-note]').inputValue(), 'Check the river reference.');
  const saved = await page.evaluate(() => window.__readerHarness.lastBook.state);
  assert.ok(saved.location.startsWith('epubcfi('));
  assert.equal(saved.bookmarks.length, 1);
  assert.equal(saved.bookmarks[0].note, 'Check the river reference.');
  await unchanged();
});

test('findings jump to the broken-link origin and metadata concerns show original source', async t => {
  const { page, state } = await openApp(t, { kinds: ['normal'] });
  const report = state.reports.find(book => book.path === state.paths.normal);
  const linkIndex = report.issues.findIndex(issue => issue.code === 'LINK_MISSING');
  const metadataIndex = report.issues.findIndex(issue => issue.code === 'METADATA_LANGUAGE');
  assert.ok(linkIndex >= 0 && metadataIndex >= 0, 'fixtures must have real scanner findings');
  await page.locator(`[data-reader-issue="${linkIndex}"]`).click();
  await page.locator('#reader-dialog').waitFor({ state: 'visible' });
  await expectChapter(page, 'A missing image follows.');
  await page.waitForFunction(() => document.querySelector('#reader-stage iframe')?.contentDocument?.querySelector('[data-sg-focus="true"]')?.getAttribute('data-sg-ref') === 'missing-image.png');
  const located = await api('LocateReaderIssue', (await page.evaluate(() => window.__readerHarness.lastBook)).sessionId, linkIndex);
  assert.equal(located.mode, 'page');
  assert.ok(located.reference.includes('missing-image.png'));
  await page.locator('#reader-close').click();
  await page.locator('#reader-dialog').waitFor({ state: 'hidden' });
  await page.evaluate(({ path, index }) => { void window.SpineGuardReader.open(path, index); }, { path: state.paths.normal, index: metadataIndex });
  await page.locator('#reader-source-output').waitFor({ state: 'visible' });
  await page.waitForFunction(() => document.querySelector('#reader-source-output')?.textContent?.includes('<package'));
  assert.match(await page.locator('#reader-source-file').textContent(), /book\.opf/);
  assert.match(await page.locator('#reader-source-output').textContent(), /<package/);
  assert.equal(await page.locator('#reader-source-output package').count(), 0, 'original markup must be escaped text');
  await unchanged();
});

test('hostile EPUB remains readable without executing scripts, active SVG or bridge calls', async t => {
  const { page, state } = await openApp(t, { kinds: ['hostile'] });
  await openBook(page, state, 'hostile');
  await expectChapter(page, 'This safe text must remain.');
  await page.waitForFunction(() => document.querySelector('#reader-stage iframe')?.contentDocument?.querySelector('img')?.naturalWidth === 36);
  const security = await page.locator('#reader-stage iframe').first().evaluate(frame => {
    const doc = frame.contentDocument;
    const elements = [...doc.querySelectorAll('*')];
    return {
      unsafeElements: doc.querySelectorAll('script,iframe,object,embed,form,foreignObject,animate').length,
      unsafeAttributes: elements.flatMap(el => [...el.attributes]).filter(attr => /^on/i.test(attr.name) || /^(?:href|src|xlink:href|style)$/i.test(attr.name) && /javascript:/i.test(attr.value)).length,
      unsafeStyles: [...doc.querySelectorAll('style')].filter(style => /outside\.invalid|javascript:/i.test(style.textContent)).length,
      imageWidth: doc.querySelector('img')?.naturalWidth,
      sandbox: frame.getAttribute('sandbox'),
    };
  });
  assert.equal(security.unsafeElements, 0);
  assert.equal(security.unsafeAttributes, 0);
  assert.equal(security.unsafeStyles, 0);
  assert.equal(security.imageWidth, 36);
  assert.ok(!security.sandbox.split(/\s+/).includes('allow-scripts'));
  const execution = await page.evaluate(() => ({ executed: window.__bookExecuted, bridgeCalls: window.__readerHarness.bridgeCalls }));
  assert.deepEqual(execution, { executed: false, bridgeCalls: 0 });
  const book = await page.evaluate(() => window.__readerHarness.lastBook);
  const resource = book.resources.find(resource => resource.path.endsWith('chapter-one.xhtml'));
  const original = await api('ReadReaderSource', book.sessionId, resource.id, 1, 0);
  assert.match(original.content, /<script>/, 'source inspection must preserve original hostile markup as text');
  await page.locator('#reader-tab-source').click();
  await page.waitForFunction(() => document.querySelector('#reader-source-output')?.textContent?.includes('<script>'));
  assert.match(await page.locator('#reader-source-output').textContent(), /<script>/);
  assert.equal(await page.locator('#reader-source-output script').count(), 0);
  await unchanged();
});

test('reader controls recover after an unreadable file and after close during open', async t => {
  const { page, state } = await openApp(t, { kinds: ['normal', 'invalid'], openDelay: 250 });
  await page.evaluate(path => { void window.SpineGuardReader.open(path); }, state.paths.invalid);
  await page.locator('#reader-dialog').waitFor({ state: 'visible' });
  await page.locator('#reader-notice.reader-error').waitFor({state:'visible'});
  assert.ok((await page.locator('#reader-notice').textContent()).length > 0);
  await page.locator('#reader-close').click();
  await page.locator('#reader-dialog').waitFor({ state: 'hidden' });
  await page.evaluate(path => { void window.SpineGuardReader.open(path); }, state.paths.normal);
  await page.waitForFunction(() => window.__readerHarness.lastBook !== null);
  const lateBook = await page.evaluate(() => window.__readerHarness.lastBook);
  await page.locator('#reader-close').click();
  await page.waitForFunction(() => window.__readerHarness.calls.some(call => call.name === 'CloseReader'));
  await page.locator('#reader-dialog').waitFor({ state: 'hidden' });
  assert.notEqual((await fetch(`${origin}${lateBook.packageUrl}`)).status, 200, 'late open result must be closed');
  await openBook(page, state);
  await expectChapter(page, 'Publisher styles are retained.');
  await page.locator('#reader-tab-source').click();
  const packageId=await page.evaluate(()=>window.__readerHarness.lastBook.resources.find(resource=>resource.path.endsWith('book.opf')).id);
  await page.locator('#reader-source-file').selectOption(packageId);
  await page.waitForFunction(()=>document.querySelector('#reader-source-output').textContent.includes('<package'));
  await choosePane(page,'files');
  await page.locator('#reader-close').click();
  await page.locator('#reader-dialog').waitFor({state:'hidden'});
  await page.evaluate(path=>{void window.SpineGuardReader.open(path);},state.paths.invalid);
  await page.locator('#reader-notice.reader-error').waitFor({state:'visible'});
  assert.equal(await page.locator('#reader-title').textContent(),'Book preview unavailable');
  assert.equal(await page.locator('#reader-source-file option').count(),0);
  assert.equal(await page.locator('#reader-source-output').textContent(),'');
  assert.equal(await page.locator('#reader-files button').count(),0);
  assert.equal(await page.locator('#reader-chapter').textContent(),'');
  assert.equal(await page.locator('#reader-tab-source').isDisabled(),true);
  assert.equal(await page.locator('#reader-bookmark-button').isDisabled(),true);
  await unchanged();
});

test('damaged document markup can be inspected while its original source stays intact', async t => {
  const { page, state } = await openApp(t, { kinds: ['malformed'] });
  await openBook(page, state, 'malformed');
  await expectChapter(page, 'Text survives a malformed document.');
  await page.locator('#reader-tab-source').click();
  await page.locator('#reader-source-output').waitFor({ state: 'visible' });
  await page.waitForFunction(() => document.querySelector('#reader-source-output')?.textContent?.includes('Broken original markup'));
  const source = await page.locator('#reader-source-output').textContent();
  assert.match(source, /Broken original markup<p>/);
  assert.ok(!source.includes('</html>'), 'source inspector must show the damaged original rather than the generated preview');
  assert.equal(await page.locator('#reader-source-output h1').count(), 0);
  await unchanged();
});

test('rapid source line requests keep the latest requested location', async t => {
  const {page,state}=await openApp(t,{kinds:['normal'],sourceDelays:[250,0]});
  await openBook(page,state);
  await page.locator('#reader-tab-source').click();
  await page.waitForFunction(()=>window.__readerHarness.calls.some(call=>call.name==='ReadReaderSource'));
  await page.locator('#reader-source-line').fill('20');
  await page.locator('#reader-source-go').click();
  await page.waitForFunction(()=>window.__readerHarness.calls.filter(call=>call.name==='ReadReaderSource').length===2&&window.__readerHarness.calls.filter(call=>call.name==='ReadReaderSource').every(call=>call.done));
  await page.waitForFunction(()=>document.querySelector('#reader-source-output .reader-source-focus')?.dataset.line==='20');
  assert.equal(await page.locator('#reader-source-line').inputValue(),'20');
  await unchanged();
});

test('rapid contents navigation finishes on the last selected chapter', async t => {
  const {page,state}=await openApp(t,{kinds:['normal']});
  await openBook(page,state);
  await choosePane(page,'contents');
  await page.evaluate(()=>{
    const buttons=[...document.querySelectorAll('#reader-toc button')];
    buttons.find(button=>button.textContent==='Second chapter').click();
    buttons.find(button=>button.textContent==='First chapter').click();
    buttons.find(button=>button.textContent==='Second chapter').click();
  });
  await expectChapter(page,'The unique silver river phrase');
  await page.waitForFunction(()=>document.querySelector('#reader-loading').hidden);
  await page.waitForFunction(()=>document.querySelector('#reader-progress').textContent.includes('Section 2 of 2'));
  assert.match(await page.locator('#reader-progress').textContent(),/Section 2 of 2/);
  assert.equal(await page.locator('#reader-notice.reader-error').count(),0);
  await unchanged();
});

test('a delayed search Stop cannot cancel or overwrite the next query', async t => {
  const {page,state}=await openApp(t,{kinds:['normal'],searchDelays:[500,0],cancelSearchDelay:200});
  await openBook(page,state);
  await choosePane(page,'search');
  await page.locator('#reader-search-input').fill('valley');
  await page.locator('#reader-search-button').click();
  await page.waitForFunction(()=>window.__readerHarness.calls.some(call=>call.name==='SearchReader'));
  await page.locator('#reader-search-stop').click();
  await page.locator('#reader-search-input').fill('silver river');
  await page.locator('#reader-search-button').click();
  await page.locator('#reader-search-results button').first().waitFor();
  await page.waitForFunction(()=>window.__readerHarness.calls.filter(call=>call.name==='SearchReader').length===2&&window.__readerHarness.calls.filter(call=>call.name==='SearchReader').every(call=>call.done));
  const calls=await page.evaluate(()=>window.__readerHarness.calls.filter(call=>call.name==='SearchReader'));
  assert.equal(calls[1].args[1],'silver river');
  assert.equal(calls[1].pendingStops,0,'replacement query must wait for the earlier Stop acknowledgement');
  assert.match(await page.locator('#reader-search-results').textContent(),/silver river/i);
  assert.doesNotMatch(await page.locator('#reader-search-results').textContent(),/quiet valley/i);
  assert.equal(await page.locator('#reader-search-button').isDisabled(),false);
  await unchanged();
});

test('closing with an earlier save pending preserves the latest bookmark note', async t => {
  const {page,state}=await openApp(t,{kinds:['normal'],saveDelays:[500,0]});
  await openBook(page,state);
  await page.waitForFunction(()=>window.__readerHarness.calls.some(call=>call.name==='SaveReaderState'&&!call.done));
  await page.locator('#reader-bookmark-button').click();
  await page.locator('[data-bookmark-note]').fill('Keep the latest note when closing.');
  await page.locator('[data-save-bookmark-note]').click();
  await page.locator('#reader-close').click();
  await page.locator('#reader-dialog').waitFor({state:'hidden'});
  await openBook(page,state);
  await choosePane(page,'bookmarks');
  assert.equal(await page.locator('[data-bookmark-note]').inputValue(),'Keep the latest note when closing.');
  await unchanged();
});

test('source fingerprint changes invalidate session resources instead of reading changed bytes', async () => {
  await json('/__test/reset', { method: 'POST' });
  const state = await json('/__test/state');
  const book = await api('OpenReader', state.paths.normal);
  await json('/__test/mutate?kind=normal', { method: 'POST' });
  assert.notEqual((await fetch(`${origin}${book.packageUrl}`)).status, 200);
  await assert.rejects(api('ReadReaderSource', book.sessionId, book.spine[0].id, 1, 0), /changed|modified|reopen|stale/i);
  await api('CloseReader', book.sessionId);
});

for(const coverKind of ['svgcover','htmlcover']){
  test(`detached ${coverKind} stays portrait and preserves artwork proportions when resized`,async t=>{
    const {page,state}=await openApp(t,{standalone:true,readerKind:coverKind,viewport:{width:760,height:980}});
    await page.waitForFunction(()=>!!document.querySelector('#reader-stage iframe')?.contentDocument?.querySelector('#cover-svg, #cover-html-image'));
    assert.equal(await page.evaluate(()=>typeof window.spineguardPreview),'undefined','standalone reader must use its restricted ReaderWindow bridge');
    assert.equal(await page.locator('#reader-panels-button').getAttribute('aria-expanded'),'false');
    for(const viewport of [{width:760,height:980},{width:1920,height:1080},{width:560,height:680}]){
      await page.setViewportSize(viewport);
      const presentationHandle=await page.waitForFunction(()=>{
        const rect=element=>{const r=element.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,right:r.right,bottom:r.bottom};};
        const stage=document.getElementById('reader-stage'),canvas=stage.closest('.reader-canvas'),frame=stage.querySelector('iframe'),doc=frame?.contentDocument;
        const page=rect(stage),area=rect(canvas);if(!doc||page.width<200||page.height<300||Math.abs(page.width/page.height-2/3)>0.02||doc.documentElement.dataset.spineguardCover!=='true'||page.x<area.x||page.right>area.right||page.y<area.y||page.bottom>area.bottom||getComputedStyle(frame).visibility!=='visible')return false;
        const svg=doc.querySelector('#cover-svg'),svgImage=doc.querySelector('#cover-svg-image'),image=doc.querySelector('#cover-html-image');
        const matrix=svgImage?.getScreenCTM();
        if(svgImage&&!matrix||image&&!image.naturalWidth)return false;
        const bodyStyle=doc.defaultView.getComputedStyle(doc.body),artStyle=doc.defaultView.getComputedStyle(svg||image);
        const ancestors=[];for(let node=frame;node&&node!==canvas;node=node.parentElement){const style=getComputedStyle(node);ancestors.push({name:node.className||node.id||node.localName,rect:rect(node),visibility:style.visibility,opacity:style.opacity,overflow:style.overflow,clip:style.clipPath,transform:style.transform,scrollLeft:node.scrollLeft,scrollTop:node.scrollTop});}
        const sourceStyles=[doc.documentElement,doc.body,svg||image].map(node=>{const style=doc.defaultView.getComputedStyle(node);return {node:node.localName,css:node.getAttribute('style'),width:style.width,height:style.height,columnWidth:style.columnWidth,columnCount:style.columnCount,columnGap:style.columnGap,overflow:style.overflow,position:style.position,margin:style.margin,padding:style.padding,scrollLeft:node.scrollLeft,scrollTop:node.scrollTop};});
        return {stage:page,canvas:rect(canvas),frame:rect(frame),body:rect(doc.body),art:rect(svg||image),ancestors,sourceStyles,visible:{loading:!document.getElementById('reader-loading').hidden,body:bodyStyle.visibility,bodyOpacity:bodyStyle.opacity,art:artStyle.visibility,artOpacity:artStyle.opacity,view:getComputedStyle(frame.parentElement).cssText,viewOpacity:getComputedStyle(frame.parentElement).opacity},cover:doc.documentElement.dataset.spineguardCover,
          svg:svg?{aspect:svg.getAttribute('preserveAspectRatio'),imageAspect:svgImage.getAttribute('preserveAspectRatio'),href:svgImage.href.baseVal,base:doc.baseURI,scaleX:Math.hypot(matrix.a,matrix.b),scaleY:Math.hypot(matrix.c,matrix.d)}:null,
          image:image?{naturalWidth:image.naturalWidth,naturalHeight:image.naturalHeight,fit:doc.defaultView.getComputedStyle(image).objectFit}:null,
          overflow:document.documentElement.scrollWidth-innerWidth};
      });
      const presentation=await presentationHandle.jsonValue();await presentationHandle.dispose();
      assert.ok(presentation.stage.x>=presentation.canvas.x-1&&presentation.stage.right<=presentation.canvas.right+1,JSON.stringify(presentation));
      assert.ok(presentation.stage.y>=presentation.canvas.y-1&&presentation.stage.bottom<=presentation.canvas.bottom+1,JSON.stringify(presentation));
      assert.ok(presentation.overflow<=1,JSON.stringify(presentation));
      assert.equal(presentation.cover,'true');
      if(presentation.svg){assert.ok(!/none/i.test(presentation.svg.aspect));assert.ok(!/none/i.test(presentation.svg.imageAspect));assert.ok(Math.abs(presentation.svg.scaleX/presentation.svg.scaleY-1)<0.01,JSON.stringify(presentation));assert.ok(presentation.svg.href,'SVG cover image must retain a usable resource reference');const loaded=await page.evaluate(async href=>{const image=new Image();image.src=href;await image.decode();await new Promise(requestAnimationFrame);return {width:image.naturalWidth,height:image.naturalHeight};},new URL(presentation.svg.href,presentation.svg.base).href);assert.deepEqual(loaded,{width:36,height:54});}
      else{assert.equal(presentation.image.naturalWidth,36);assert.equal(presentation.image.naturalHeight,54);assert.equal(presentation.image.fit,'contain');}
      const painted=await paintedCover(page,presentation.stage);
      if(process.env.SPINEGUARD_UI_SCREENSHOTS){await mkdir(process.env.SPINEGUARD_UI_SCREENSHOTS,{recursive:true});const basename=`detached-${coverKind}-${viewport.width}x${viewport.height}`;await writeFile(join(process.env.SPINEGUARD_UI_SCREENSHOTS,`${basename}.json`),JSON.stringify({...presentation,pixels:painted.pixels},null,2));await writeFile(join(process.env.SPINEGUARD_UI_SCREENSHOTS,`${basename}.png`),painted.png);}
    }
    await page.locator('#reader-next').click();
    await expectChapter(page,'The unique silver river phrase');
    await page.locator('#reader-prev').click();
    await page.waitForFunction(()=>!!document.querySelector('#reader-stage iframe')?.contentDocument?.querySelector('#cover-svg, #cover-html-image'));
    await page.locator('#reader-panels-button').click();
    assert.equal(await page.locator('#reader-panels-button').getAttribute('aria-expanded'),'true');
    await choosePane(page,'contents');
    await page.locator('#reader-toc').getByRole('button',{name:'Second chapter',exact:true}).click();
    await expectChapter(page,'The unique silver river phrase');
    const model=await page.evaluate(()=>window.__readerHarness.lastBook);
    const cover=model.resources.find(resource=>resource.path.endsWith('chapter-one.xhtml'));
    const source=await api('ReadReaderSource',model.sessionId,cover.id,1,1);
    if(coverKind==='svgcover')assert.match(source.content,/preserveAspectRatio="none"/,'original SVG aspect instructions must be preserved in Source');
    else assert.match(source.content,/width:100%;height:100%/,'original image styling must be preserved in Source');
    assert.equal(model.title,state.reports.find(report=>report.path===state.paths[coverKind]).title);
    await unchanged();
  });
}

test('detached Inspect opens its findings drawer and original metadata source',async t=>{
  await json('/__test/reset',{method:'POST'});
  const fixtureState=await json('/__test/state');
  const metadataIndex=fixtureState.reports.find(book=>book.path===fixtureState.paths.normal).issues.findIndex(issue=>issue.code==='METADATA_LANGUAGE');
  const {page}=await openApp(t,{standalone:true,readerKind:'normal',issueIndex:metadataIndex,viewport:{width:560,height:680}});
  await page.waitForFunction(()=>document.querySelector('#reader-source-output')?.textContent?.includes('<package'));
  assert.equal(await page.locator('#reader-panels-button').getAttribute('aria-expanded'),'true');
  assert.equal(await page.locator('[data-reader-pane="findings"]').getAttribute('aria-pressed'),'true');
  assert.equal(await page.locator('#reader-source-output package').count(),0);
  const bounds=await page.evaluate(()=>{const source=document.getElementById('reader-source-output').getBoundingClientRect(),sidebar=document.getElementById('reader-sidebar').getBoundingClientRect();return {source:{left:source.left,right:source.right,width:source.width},sidebar:{left:sidebar.left,right:sidebar.right,width:sidebar.width}};});
  if(process.env.SPINEGUARD_UI_SCREENSHOTS)await page.screenshot({path:join(process.env.SPINEGUARD_UI_SCREENSHOTS,'detached-metadata-source-560x680.png')});
  assert.ok(bounds.source.left>=bounds.sidebar.right-1,'the findings drawer must not cover the source concern: '+JSON.stringify(bounds));
  await page.locator('#reader-panel-close').click();
  const fullWidth=await page.evaluate(()=>{const source=document.getElementById('reader-source-output').getBoundingClientRect(),canvas=document.querySelector('.reader-canvas').getBoundingClientRect();return {source:{left:source.left,right:source.right},canvas:{left:canvas.left,right:canvas.right}};});
  assert.ok(Math.abs(fullWidth.source.left-fullWidth.canvas.left)<=1&&Math.abs(fullWidth.source.right-fullWidth.canvas.right)<=1,'closing the drawer should restore the full source view: '+JSON.stringify(fullWidth));
  await unchanged();
});

test('reused detached Inspect keeps its concern after notice and viewport layout settle',async t=>{
  const {page,state}=await openApp(t,{standalone:true,readerKind:'normal',viewport:{width:760,height:980}});
  await expectChapter(page,'Publisher styles are retained.');
  await page.locator('#reader-panels-button').click();
  await choosePane(page,'contents');
  await page.locator('#reader-toc').getByRole('button',{name:'Second chapter',exact:true}).click();
  await expectChapter(page,'The unique silver river phrase');
  await page.waitForFunction(()=>/Section 2 of 2/.test(document.querySelector('#reader-progress').textContent));
  const linkIndex=state.reports.find(book=>book.path===state.paths.normal).issues.findIndex(issue=>issue.code==='LINK_MISSING');
  await page.evaluate(launch=>window.__readerHarness.emit('reader:open',launch),{path:state.paths.normal,issueIndex:linkIndex,theme:'dark'});
  await page.waitForFunction(()=>document.querySelector('#reader-stage iframe')?.contentDocument?.querySelector('[data-sg-focus="true"]')?.getAttribute('data-sg-ref')==='missing-image.png');
  for(const viewport of [{width:560,height:680},{width:820,height:1000},{width:760,height:980}]){
    await page.setViewportSize(viewport);
    await page.evaluate(()=>{delete window.__concernStableSince;});
    await page.waitForFunction(()=>{
      const expected=[...document.querySelectorAll('#reader-stage iframe')].some(frame=>getComputedStyle(frame).visibility==='visible'&&frame.contentDocument?.querySelector('[data-sg-focus="true"]')?.getAttribute('data-sg-ref')==='missing-image.png')&&/Section 1 of 2/.test(document.querySelector('#reader-progress').textContent);
      if(!expected){delete window.__concernStableSince;return false;}
      window.__concernStableSince??=performance.now();return performance.now()-window.__concernStableSince>=400;
    },null,{timeout:5_000});
  }
  assert.equal(await page.locator('#reader-panels-button').getAttribute('aria-expanded'),'true');
  assert.equal(await page.locator('[data-reader-pane="findings"]').getAttribute('aria-pressed'),'true');
  const concernBounds=await page.evaluate(()=>{const frame=[...document.querySelectorAll('#reader-stage iframe')].find(frame=>frame.contentDocument?.querySelector('[data-sg-focus="true"]'));const frameRect=frame.getBoundingClientRect(),mark=frame.contentDocument.querySelector('[data-sg-focus="true"]').getBoundingClientRect(),sidebar=document.getElementById('reader-sidebar').getBoundingClientRect();return {left:frameRect.left+mark.left,right:frameRect.left+mark.right,top:frameRect.top+mark.top,bottom:frameRect.top+mark.bottom,sidebarRight:sidebar.right};});
  assert.ok(concernBounds.left>=concernBounds.sidebarRight-3,'the findings drawer must not cover the highlighted book element: '+JSON.stringify(concernBounds));
  await unchanged();
});

for(const closeAction of ['button','native-event']){
  test(`detached ${closeAction} close flushes bookmark notes before closing its session and window`,async t=>{
    const {page,state}=await openApp(t,{standalone:true,readerKind:'normal',saveDelays:[250,0]});
    await expectChapter(page,'Publisher styles are retained.');
    await page.waitForFunction(()=>!document.querySelector('#reader-bookmark-button').disabled);
    await page.locator('#reader-bookmark-button').click();
    await page.locator('[data-bookmark-note]').fill(`Saved before ${closeAction} close.`);
    await page.locator('[data-save-bookmark-note]').click();
    const model=await page.evaluate(()=>window.__readerHarness.lastBook);
    if(closeAction==='button')await page.locator('#reader-close').click();
    else await page.evaluate(()=>window.__readerHarness.emit('reader:before-close'));
    await page.evaluate(launch=>window.__readerHarness.emit('reader:open',launch),{path:state.paths.normal,issueIndex:-1,theme:'dark'});
    await page.waitForFunction(()=>window.__readerHarness.calls.some(call=>call.name==='CloseWindow'));
    const calls=await page.evaluate(()=>window.__readerHarness.calls);
    assert.equal(calls.filter(call=>call.name==='OpenReader').length,1,'an IPC open received during close must not reopen or invalidate the session being saved');
    const closeIndex=calls.findIndex(call=>call.name==='CloseWindow');
    const saves=calls.slice(0,closeIndex).filter(call=>call.name==='SaveReaderState');
    assert.ok(saves.length>0&&saves.every(call=>call.done),'all queued saves must finish before native close');
    assert.equal(saves.at(-1).args[1].bookmarks[0].note,`Saved before ${closeAction} close.`);
    assert.ok(calls.slice(0,closeIndex).some(call=>call.name==='CloseReader'&&call.done),'reader session must close before native window');
    assert.notEqual((await fetch(`${origin}${model.packageUrl}`)).status,200);
    const reopened=await api('OpenReader',state.paths.normal);
    assert.equal(reopened.state.bookmarks[0].note,`Saved before ${closeAction} close.`);
    await api('CloseReader',reopened.sessionId);
    await unchanged();
  });
}

for (const appTheme of ['dark', 'light']) {
  test(`reader fits the minimum window in ${appTheme} app theme`, async t => {
    const { page, state } = await openApp(t, { kinds: ['normal'], appTheme, viewport: { width: 980, height: 680 } });
    await openBook(page, state);
    const bounds = await page.evaluate(() => {
      const rect = id => { const r = document.getElementById(id).getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right, bottom: r.bottom }; };
      return { dialog: rect('reader-dialog'), stage: rect('reader-stage'), close: rect('reader-close'), next: rect('reader-next'), overflow: document.documentElement.scrollWidth - innerWidth };
    });
    assert.ok(bounds.stage.width > 250 && bounds.stage.height > 200, JSON.stringify(bounds));
    assert.ok(bounds.dialog.x >= 0 && bounds.dialog.y >= 0 && bounds.dialog.right <= 981 && bounds.dialog.bottom <= 681, JSON.stringify(bounds));
    assert.ok(bounds.next.bottom <= 680 && bounds.close.right <= 980, JSON.stringify(bounds));
    assert.ok(bounds.overflow <= 1, JSON.stringify(bounds));
    if (process.env.SPINEGUARD_UI_SCREENSHOTS) {
      await mkdir(process.env.SPINEGUARD_UI_SCREENSHOTS, { recursive: true });
      await page.screenshot({ path: join(process.env.SPINEGUARD_UI_SCREENSHOTS, `reader-${appTheme}-980x680.png`) });
    }
    await unchanged();
  });
}
