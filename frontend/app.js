'use strict';

(() => {
  const icons = {
    library:'<path d="M4 4h4v16H4zM10 4h4v16h-4zM16 5l3-1 5 15-4 1z"/>',
    book:'<path d="M12 5C9 3 5 3 2 4v15c3-1 7-1 10 1 3-2 7-2 10-1V4c-3-1-7-1-10 1Zm0 0v15"/>',
    settings:'<path d="m9 3-.7 2.3-2 .9L4 5.7 2 9l1.6 1.8v2.4L2 15l2 3.3 2.3-.5 2 .9L9 21h4l.7-2.3 2-.9 2.3.5 2-3.3-1.6-1.8v-2.4L20 9l-2-3.3-2.3.5-2-.9L13 3Z"/><circle cx="11" cy="12" r="3"/>',
    folder:'<path d="M3 7V5a1 1 0 0 1 1-1h5l2 3h9a1 1 0 0 1 1 1v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/>',
    plus:'<path d="M12 5v14M5 12h14"/>', search:'<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
    'check-circle':'<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
    alert:'<path d="m10.3 4.8-8 13.7A1.7 1.7 0 0 0 3.8 21h16.4a1.7 1.7 0 0 0 1.5-2.5l-8-13.7a2 2 0 0 0-3.4 0Z"/><path d="M12 9v5M12 17h.01"/>',
    error:'<circle cx="12" cy="12" r="9"/><path d="M12 7v6M12 17h.01"/>',
    info:'<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/>',
    check:'<path d="m5 12 4 4L19 6"/>', close:'<path d="m6 6 12 12M6 18 18 6"/>',
    export:'<path d="M12 15V3m0 0-4 4m4-4 4 4M5 12v7a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-7"/>',
    download:'<path d="M12 3v12m0 0-5-5m5 5 5-5M4 15v5h16v-5"/>',
    trash:'<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/>',
    lock:'<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/>',
    help:'<circle cx="12" cy="12" r="9"/><path d="M9.4 8.5a2.7 2.7 0 1 1 4.4 2.3C12 12 12 12 12 14M12 17h.01"/>',
    shield:'<path d="m12 2 8 3v7c0 5-5 8-8 10-3-2-8-5-8-10V5l8-3Z"/><path d="m8 11 3 3 5-6"/>',
    wrench:'<path d="M14 6a5 5 0 0 0-6 6L3 17a2.8 2.8 0 0 0 4 4l5-5a5 5 0 0 0 6-6l-3 3-4-4 3-3Z"/>',
    refresh:'<path d="M20 8V3l-3 3a8 8 0 1 0 3 9M20 8h-5"/>',
    external:'<path d="M14 3h7v7M21 3l-9 9M10 4H5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2v-5"/>',
    chevron:'<path d="m9 5 7 7-7 7"/>'
  };
  const $ = id => document.getElementById(id);
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const icon = name => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[name] || icons.info}</svg>`;
  const number = value => Number(value || 0).toLocaleString();
  const bytes = value => { const n=Number(value||0); if(n<1024)return `${n} B`; if(n<1048576)return `${(n/1024).toFixed(1)} KB`; return `${(n/1048576).toFixed(1)} MB`; };
  const defaults = {wordsPerPage:250,readingWPM:250,recursive:true,epubcheckJar:'',javaPath:'java',runEpubcheck:false,theme:'dark'};
  const repairDefaults = {fixLinks:true,fixDuplicateIds:true,removeScripts:false,fixMetadata:true,language:'',generateIdentifier:false,fixDocumentMarkup:true,normalizeEntities:false};
  const state = {reports:[],settings:{...defaults},selected:null,filter:'all',query:'',running:false,repairing:false,cancelling:false,repairedPath:'',version:'1.4.0',progress:{index:0,total:0},queued:[],reviewPath:'',repairPlan:null,reviewReport:null,reviewSHA:'',reviewKinds:new Set(),reviewError:'',reviewOptions:{...repairDefaults},reviewSuggestions:{},previewing:false,previewWorker:false,previewTimer:null,reviewGeneration:0,view:'list',group:'none',sort:'recent',selecting:false,selectedBooks:new Set(),bulkOpen:false,bulkWorking:false,bulkPhase:'preview',bulkPaths:[],bulkItems:[],bulkIncluded:new Set(),bulkResult:null,bulkError:'',bulkCancelled:false,bulkCancelling:false,bulkProgress:{}};
  const collator=new Intl.Collator('en',{numeric:true,sensitivity:'base'});
  const coverAttempts=new Set(),coverQueued=new Set(),coverQueue=[];
  let coverObserver=null,coverWorkers=0;
  const api = new Proxy({}, {get:(_,name)=>(...args)=>{
    const app=window.spineguardPreview || window.go?.main?.App;
    if(!app?.[name])return Promise.reject(new Error('The desktop connection is unavailable. Open SpineGuard from its application icon.'));
    try{return Promise.resolve(app[name](...args));}catch(error){return Promise.reject(error);}
  }});

  function hydrateIcons(root=document){root.querySelectorAll('[data-icon]').forEach(el=>{el.innerHTML=icon(el.dataset.icon);});}
  function errorText(error){return typeof error==='string'?error:(error?.message||'Something went wrong. Please try again.');}
  function toast(message,kind='success',timeout=6500){
    const el=document.createElement('div');el.className=`toast ${kind}`;el.setAttribute('role',kind==='error'?'alert':'status');
    el.innerHTML=`<span>${icon(kind==='error'?'error':'check-circle')}</span><div class="toast-text">${esc(message)}</div><button class="icon-button" aria-label="Dismiss notification">${icon('close')}</button>`;
    el.querySelector('button').addEventListener('click',()=>el.remove());$('toast-stack').append(el);setTimeout(()=>el.remove(),timeout);
  }
  function setTheme(value){
    const resolved=value==='system'?(window.matchMedia('(prefers-color-scheme: light)').matches?'light':'dark'):value;
    document.documentElement.dataset.theme=resolved==='light'?'light':'dark';
  }
  function workBlocked(){return state.running||state.repairing||!!state.reviewPath||state.previewWorker||state.bulkOpen||state.bulkWorking||!!window.SpineGuardReader?.isOpen();}
  function loadLibraryPreferences(){
    try{const saved=JSON.parse(localStorage.getItem('spineguard-library-view')||'{}');if(['list','covers'].includes(saved.view))state.view=saved.view;if(['none','author','series','health'].includes(saved.group))state.group=saved.group;if(['title','author','series','recent'].includes(saved.sort))state.sort=saved.sort;}catch{}
  }
  function saveLibraryPreferences(){try{localStorage.setItem('spineguard-library-view',JSON.stringify({view:state.view,group:state.group,sort:state.sort}));}catch{}}
  function setBusy(running){
    state.running=running;$('scan-progress').classList.toggle('hidden',!running);
    for(const id of ['add-button','folder-button','empty-add-button','clear-button','rescan-library-button'])$(id).disabled=workBlocked();
    $('rescan-library-button').disabled=$('rescan-library-button').disabled||!state.reports.length;
    $('cancel-button').disabled=state.cancelling;$('cancel-button').textContent=state.cancelling?'Cancelling…':'Cancel scan';
    $('export-button').disabled=!state.reports.length||workBlocked();
    $('settings-nav').disabled=workBlocked();
    if(running){$('progress-title').textContent='Checking your books…';$('progress-fill').style.width='0%';}
    renderSelection();renderDetail();if(!workBlocked())void pumpCovers();
  }
  function upsert(report){
    if(!report?.path)return;
    const index=state.reports.findIndex(book=>book.path===report.path);
    if(index>=0){const previous=state.reports[index];if(!report.coverData&&previous.scannedAt===report.scannedAt)report={...report,coverData:previous.coverData};state.reports[index]=report;}else state.reports.push(report);
    if(!state.selected)state.selected=report.path;
  }
  function label(report){
    if(report.status==='unreadable')return 'Cannot read';
    if(report.status==='error')return 'Issues detected';
    if(report.status==='warning')return 'Needs attention';
    return 'No detected issues';
  }
  function badge(report){return `<span class="health-badge ${esc(report.status)}">${icon(report.status==='healthy'?'check-circle':report.status==='warning'?'alert':'error')}${esc(label(report))}</span>`;}
  function hasAttention(book){return book.status==='warning'||book.status==='error';}
  function estimatedPages(book){return Math.ceil(Number(book.stats?.words||0)/Number(state.settings.wordsPerPage||250));}
  function estimatedMinutes(book){return Math.ceil(Number(book.stats?.words||0)/Number(state.settings.readingWPM||250));}
  function visibleReports(){
    return state.reports.filter(book=>{
      if(state.filter==='healthy'&&book.status!=='healthy')return false;
      if(state.filter==='attention'&&!hasAttention(book))return false;
      if(state.filter==='unreadable'&&book.status!=='unreadable')return false;
      return !state.query||[book.title,book.author,book.series,book.seriesIndex,book.filename].join(' ').toLocaleLowerCase().includes(state.query);
    }).sort(compareBooks);
  }
  function textKey(value){return String(value||'').trim().replace(/\s+/g,' ').toLocaleLowerCase();}
  function compareSeriesIndex(a,b){
    const left=String(a||'').trim(),right=String(b||'').trim();if(!left||!right)return left?-1:right?1:0;
    if(/^[+-]?\d+(?:\.\d+)?$/.test(left)&&/^[+-]?\d+(?:\.\d+)?$/.test(right)){const delta=Number(left)-Number(right);if(Number.isFinite(delta)&&delta)return delta;}
    return collator.compare(left,right);
  }
  function compareBooks(a,b){
    const title=()=>collator.compare(a.title||a.filename||'',b.title||b.filename||'');
    if(state.sort==='author')return collator.compare(textKey(a.author),textKey(b.author))||title();
    if(state.sort==='series'){const series=collator.compare(textKey(a.series)||'\uffff',textKey(b.series)||'\uffff');return series||compareSeriesIndex(a.seriesIndex,b.seriesIndex)||title();}
    if(state.sort==='recent'){const delta=(Date.parse(b.scannedAt)||0)-(Date.parse(a.scannedAt)||0);return delta||state.reports.indexOf(a)-state.reports.indexOf(b);}
    return title();
  }
  function groupedReports(books){
    if(state.group==='none')return [{name:'',books}];
    const groups=new Map();
    for(const book of books){const raw=state.group==='author'?String(book.author||'').trim().replace(/\s+/g,' '):state.group==='series'?String(book.series||'').trim().replace(/\s+/g,' '):label(book);const name=raw||(state.group==='author'?'Unknown author':'No series'),key=state.group==='health'?book.status:textKey(raw)||'\uffff';if(!groups.has(key))groups.set(key,{name,books:[],key});groups.get(key).books.push(book);}
    return Array.from(groups.values()).sort((a,b)=>state.group==='health'?(['error','unreadable','warning','healthy'].indexOf(a.key)-['error','unreadable','warning','healthy'].indexOf(b.key)):collator.compare(a.key,b.key));
  }
  function safeCoverData(value){return typeof value==='string'&&value.length<4000000&&/^data:image\/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/=\s]+$/.test(value);}
  function cover(book,large=false){
    const seed=Array.from(book.title||book.filename||'S').reduce((n,c)=>n+c.codePointAt(0),0);
    return `<span class="book-cover cover-tone-${seed%6}${large?' large':''}" data-cover-path="${esc(book.path)}">${safeCoverData(book.coverData)?`<img src="${esc(book.coverData)}" alt="Book cover" loading="lazy" decoding="async">`:esc(Array.from(book.title||book.filename||'S')[0].toLocaleUpperCase())}</span>`;
  }
  function observeCovers(){
    coverObserver?.disconnect();
    if(!('IntersectionObserver' in window))return;
    coverObserver=new IntersectionObserver(entries=>{for(const entry of entries){if(!entry.isIntersecting)continue;coverObserver.unobserve(entry.target);const book=state.reports.find(book=>book.path===entry.target.dataset.coverPath);if(!book||safeCoverData(book.coverData))continue;const key=book.path+'\x00'+(book.scannedAt||'');if(coverAttempts.has(key)||coverQueued.has(key))continue;coverQueued.add(key);coverQueue.push({path:book.path,scannedAt:book.scannedAt,key});}void pumpCovers();},{rootMargin:'120px'});
    document.querySelectorAll('.book-cover[data-cover-path]').forEach(element=>coverObserver.observe(element));
  }
  async function pumpCovers(){
    while(coverWorkers<2&&coverQueue.length&&!workBlocked()){
      const task=coverQueue.shift();coverQueued.delete(task.key);const book=state.reports.find(book=>book.path===task.path);
      if(!book||book.scannedAt!==task.scannedAt||safeCoverData(book.coverData)||coverAttempts.has(task.key))continue;
      coverAttempts.add(task.key);coverWorkers++;
      void (async()=>{try{const data=await api.GetBookCover(task.path);const current=state.reports.find(book=>book.path===task.path);if(current&&current.scannedAt===task.scannedAt&&safeCoverData(data)){current.coverData=data;document.querySelectorAll('.book-cover[data-cover-path]').forEach(element=>{if(element.dataset.coverPath===task.path)element.innerHTML=`<img src="${esc(data)}" alt="Book cover" loading="lazy" decoding="async">`;});}}catch{}finally{coverWorkers--;void pumpCovers();}})();
    }
  }
  function renderBook(book){
    const index=state.reports.indexOf(book),selected=state.selectedBooks.has(book.path),isGrid=state.view==='covers';
    return `<div class="book-entry${isGrid?' cover-entry':''}${selected?' included':''}">${state.selecting?`<label class="book-select"><input type="checkbox" data-select-book-index="${index}" aria-label="Select ${esc(book.title||book.filename)}" ${selected?'checked':''} ${workBlocked()?'disabled':''}></label>`:''}<button class="book-row${isGrid?' book-card':''}${book.path===state.selected?' selected':''}" data-book-index="${index}" aria-label="View ${esc(book.title||book.filename)}" aria-pressed="${book.path===state.selected}"><span class="book-summary">${cover(book)}<span class="book-title-text"><span class="book-title" title="${esc(book.title||book.filename)}">${esc(book.title||book.filename||'Untitled book')}</span><span class="book-author" title="${esc(book.author||book.filename)}">${esc(book.author||'Unknown author')}</span>${book.series?`<span class="book-series">${esc(book.series)}${book.seriesIndex?` · ${esc(book.seriesIndex)}`:''}</span>`:''}</span></span><span>${badge(book)}</span><span class="row-number">${book.status==='unreadable'?'—':number(book.stats?.words)}</span><span class="row-number">${book.status==='unreadable'?'—':number(book.stats?.estimatedPages)}</span></button></div>`;
  }
  function renderSelection(){
    const busy=workBlocked();
    if(!state.reports.length)state.selecting=false;
    for(const path of state.selectedBooks)if(!state.reports.some(book=>book.path===path))state.selectedBooks.delete(path);
    $('select-books-button').disabled=busy||!state.reports.length;$('select-books-button').setAttribute('aria-pressed',String(state.selecting));$('select-books-button').textContent=state.selecting?'Done selecting':'Select books';
    $('book-selection-bar').classList.toggle('hidden',!state.selecting);$('selected-book-count').textContent=`${number(state.selectedBooks.size)} selected`;
    $('select-visible-books').disabled=busy||!visibleReports().length;$('clear-selected-books').disabled=busy||!state.selectedBooks.size;$('review-selected-books').disabled=busy||!state.selectedBooks.size;
    for(const id of ['library-view-list','library-view-covers','library-group','library-sort'])$(id).disabled=busy;
    document.querySelectorAll('[data-select-book-index]').forEach(checkbox=>{checkbox.disabled=busy;});
  }
  function render(){
    state.reports.forEach(book=>{if(book.stats){book.stats.estimatedPages=estimatedPages(book);book.stats.readingMinutes=estimatedMinutes(book);}});
    const total=state.reports.length,healthy=state.reports.filter(b=>b.status==='healthy').length,attention=state.reports.filter(hasAttention).length,unreadable=state.reports.filter(b=>b.status==='unreadable').length;
    $('nav-count').textContent=number(total);$('total-books').textContent=number(total);$('healthy-books').textContent=number(healthy);$('attention-books').textContent=number(attention+unreadable);
    $('all-count').textContent=number(total);$('healthy-count').textContent=number(healthy);$('attention-count').textContent=number(attention);$('unreadable-count').textContent=number(unreadable);
    $('empty-state').classList.toggle('hidden',total>0);$('export-button').disabled=!total||workBlocked();$('clear-button').disabled=!total||workBlocked();
    $('library-footer-text').textContent=total?`${number(total)} ${total===1?'book':'books'} · ${number(state.reports.reduce((sum,b)=>sum+Number(b.stats?.words||0),0))} words`:'Ready for your first book';
    $('rescan-library-button').disabled=!total||workBlocked();$('library-display-bar').classList.toggle('hidden',!total);
    const visible=visibleReports();$('no-results').classList.toggle('hidden',!total||visible.length>0);
    $('book-rows').classList.toggle('cover-grid',state.view==='covers');$('book-table-head').classList.toggle('hidden',state.view==='covers');
    $('book-rows').innerHTML=groupedReports(visible).map(group=>`${group.name?`<h3 class="book-group-heading">${esc(group.name)}<span>${number(group.books.length)}</span></h3>`:''}${group.books.map(renderBook).join('')}`).join('');
    $('library-view-list').setAttribute('aria-pressed',String(state.view==='list'));$('library-view-covers').setAttribute('aria-pressed',String(state.view==='covers'));$('library-group').value=state.group;$('library-sort').value=state.sort;
    document.querySelectorAll('[data-filter]').forEach(button=>button.classList.toggle('active',button.dataset.filter===state.filter));
    renderSelection();renderDetail();observeCovers();
  }
  function renderDetail(){
    const panel=$('book-detail'),source=state.reports.find(item=>item.path===state.selected);
    const book=source?{...source,stats:{...source.stats,estimatedPages:estimatedPages(source),readingMinutes:estimatedMinutes(source)}}:undefined;
    if(!book){panel.classList.add('hidden');return;}
    panel.classList.remove('hidden');const stats=book.stats||{},issues=Array.isArray(book.issues)?book.issues:[];
    const repairable=issues.some(issue=>issue.repairable),busy=workBlocked();
    const healthCopy=book.status==='healthy'?'The checks that ran found no issues. Explore the book’s stats below.':book.status==='unreadable'?'This book could not be fully read. Review the findings before adding it to your reader.':`${number(book.errors)} ${book.errors===1?'error':'errors'} and ${number(book.warnings)} ${book.warnings===1?'warning':'warnings'} found.${repairable?' Some findings have a repair available.':' Review the findings below for next steps.'}`;
    const epubcheck=book.epubcheck;
    panel.innerHTML=`<div class="detail-kicker">BOOK INSPECTOR<button class="icon-button" data-detail-action="close" aria-label="Close book inspector">${icon('close')}</button></div><div class="detail-hero">${cover(book,true)}<div><h2>${esc(book.title||book.filename||'Untitled book')}</h2><p class="detail-author">${esc(book.author||'Unknown author')}</p><div class="book-tags"><span class="neutral-badge">EPUB ${esc(book.epubVersion||'?')}</span>${book.language?`<span class="neutral-badge">${esc(book.language.toLocaleUpperCase())}</span>`:''}<span class="neutral-badge">${esc(bytes(book.fileBytes))}</span></div></div></div><div class="health-summary ${esc(book.status)}">${badge(book)}<p>${esc(healthCopy)}</p></div>${epubcheck?`<div class="epubcheck-result"><strong>EPUBCheck · ${esc(epubcheck.status||'Not run')}${epubcheck.version?` · ${esc(epubcheck.version)}`:''}</strong>${esc(epubcheck.detail||'Formal standards validation.')}</div>`:''}<div class="detail-stats"><div class="detail-stat"><strong>${book.status==='unreadable'?'—':number(stats.words)}</strong><span>Words in reading order</span></div><div class="detail-stat"><strong>${book.status==='unreadable'?'—':number(stats.estimatedPages)}</strong><span>Estimated pages</span></div><div class="detail-stat"><strong>${number(stats.chapters)}</strong><span>Reading-order documents</span></div><div class="detail-stat"><strong>${readingTime(stats.readingMinutes)}</strong><span>Estimated reading time</span></div></div><p class="estimate-note">${number(state.settings.wordsPerPage)} words per estimated page · ${stats.sourcePages?`${number(stats.sourcePages)} source page markers`:'No source page markers supplied'}</p><div class="detail-actions"><button class="button secondary" data-detail-action="read" ${busy?'disabled':''}>${icon('book')}Read book</button><button class="button ${repairable?'primary':'secondary'}" data-detail-action="repair" ${busy||!issues.length?'disabled':''} title="${issues.length?'Review proposed repairs and reader cleanup':'No repairs needed: no findings were detected'}">${state.repairing?'<span class="spinner"></span>':icon('wrench')}${state.repairing?'Repairing…':'Review repairs'}</button><button class="button secondary" data-detail-action="rescan" ${busy?'disabled':''}>${icon('refresh')}Rescan</button><button class="icon-button" data-detail-action="reveal" title="Show original file" aria-label="Show original file">${icon('folder')}</button></div>${issues.length?'<p class="detail-note">Review changes before saving a separate copy. Your original is preserved.</p>':''}<div class="issue-heading"><h3>Findings</h3><div class="issue-counts"><span class="errors">${number(book.errors)} errors</span><span class="warnings">${number(book.warnings)} warnings</span></div></div><div class="issue-list">${issues.length?issues.slice().sort((a,b)=>({error:0,warning:1,info:2}[a.severity]??3)-({error:0,warning:1,info:2}[b.severity]??3)).map(issue=>`<article class="issue ${esc(issue.severity)}"><div class="issue-top"><span class="issue-icon">${icon(issue.severity==='error'?'error':issue.severity==='warning'?'alert':'info')}</span><h4>${esc(issue.title||issue.code)}</h4></div>${issue.detail?`<p>${esc(issue.detail)}</p>`:''}${issue.file?`<span class="issue-file">${esc(issue.file)}${issue.line?`:${number(issue.line)}`:''}</span>`:''}${repairExplanation(issue)?`<p class="repair-explanation">${esc(repairExplanation(issue))}</p>`:''}<div class="issue-meta"><span>${esc(issue.code||issue.severity)}</span><button class="issue-inspect" data-reader-issue="${issues.indexOf(issue)}" ${busy?'disabled':''}>Inspect</button>${issue.repairable?`<span class="repairable">${icon('wrench')}${issue.optionalRepair?(issue.repairKind==='reader_cleanup'||issue.code==='CONTENT_SCRIPT'?'Optional reader cleanup':'Optional repair'):'Repair available'}</span>`:''}</div></article>`).join(''):`<div class="no-issues"><span>${icon('check-circle')}</span>No findings from the checks that ran.</div>`}</div><details class="metadata-disclosure"><summary>File & publication details</summary><dl class="metadata-grid"><dt>File</dt><dd>${esc(book.filename)}</dd><dt>Location</dt><dd>${esc(book.path)}</dd><dt>Identifier</dt><dd>${esc(book.identifier||'Not supplied')}</dd><dt>Images / fonts</dt><dd>${number(stats.images)} / ${number(stats.fonts)}</dd><dt>Archive entries</dt><dd>${number(stats.files)}</dd><dt>Expanded size</dt><dd>${esc(bytes(stats.uncompressedBytes))}</dd><dt>Largest chapter</dt><dd>${esc(bytes(stats.largestChapterBytes))}</dd><dt>Last checked</dt><dd>${formatDate(book.scannedAt)}</dd><dt>Scan time</dt><dd>${number(book.durationMs)} ms</dd></dl></details>`;
    if(book.series){const series=document.createElement('p');series.className='detail-series';series.textContent=book.series+(book.seriesIndex?' · '+book.seriesIndex:'');panel.querySelector('.detail-author').after(series);}
  }
  function repairExplanation(issue){
    if(issue.repairReason)return issue.repairReason;
    if(issue.code==='CONTENT_SCRIPT')return 'Optional reader cleanup can remove scripts from a separate copy. Interactive content may stop working.';
    if(issue.code==='LINK_MISSING'||issue.code==='LINK_FRAGMENT')return 'Review repairs to look for one proven destination. Ambiguous or absent targets are left for manual correction.';
    if(issue.code==='CONTENT_DUPLICATE_ID')return 'Review repairs to recheck source identifiers. Renaming is offered only when references and styling can be preserved.';
    if(issue.code==='XML_DTD_CHECK_INCOMPLETE')return 'Run EPUBCheck for this assessment. SpineGuard preserves DTD-dependent content.';
    if(issue.code==='METADATA_LANGUAGE'||issue.code==='METADATA_LANGUAGE_INVALID')return 'Confirm the language in Review repairs. A detected language remains a suggestion until you select it.';
    if(issue.code==='METADATA_IDENTIFIER')return 'Review repairs to recover an existing identifier or explicitly create one when no identifier can be recovered.';
    if(issue.code?.startsWith('METADATA_'))return 'Missing publication metadata needs a confirmed value. SpineGuard does not guess it.';
    if(issue.code==='FONT_OBFUSCATION')return 'Recognized font obfuscation is preserved; no cleanup is needed.';
    return '';
  }
  function formatDate(value){const date=new Date(value);return Number.isNaN(date.getTime())?'—':esc(date.toLocaleString([], {dateStyle:'medium',timeStyle:'short'}));}
  function readingTime(value){const minutes=Number(value||0);if(minutes<60)return `${number(minutes)} min`;return `${Math.floor(minutes/60)}h ${minutes%60}m`;}

  async function scan(paths){
    paths=(Array.isArray(paths)?paths:[]).filter(path=>typeof path==='string'&&path.trim());if(!paths.length)return;
    if(workBlocked()){state.queued.push(...paths);toast('Added to the queue. These books will be checked after the current task.');return;}
    state.cancelling=false;state.progress={index:0,total:0};setBusy(true);$('progress-path').textContent=paths.length===1?paths[0]:`${number(paths.length)} selected paths`;
    try{const reports=await api.ScanPaths(paths);if(Array.isArray(reports))reports.forEach(upsert);render();if(!state.cancelling)toast(reports?.length?`Checked ${number(reports.length)} ${reports.length===1?'book':'books'}.`:'No EPUB files were found in the selected location.');else toast('Scan stopped. Completed results are in your library.');}
    catch(error){toast(errorText(error),'error',10000);}
    finally{state.cancelling=false;setBusy(false);render();drainQueue();}
  }
  function drainQueue(){if(!workBlocked()&&state.queued.length){const queued=[...new Set(state.queued)];state.queued=[];void scan(queued);}}
  async function chooseFiles(){try{const paths=await api.SelectFiles();if(paths?.length)await scan(paths);}catch(error){toast(errorText(error),'error');}}
  async function chooseFolder(){try{const path=await api.SelectFolder();if(path)await scan([path]);}catch(error){toast(errorText(error),'error');}}
  function repair(){
    const book=state.reports.find(item=>item.path===state.selected);if(!book||workBlocked())return;
    state.reviewPath=book.path;state.repairPlan=null;state.reviewError='';
    state.reviewOptions={...repairDefaults};state.reviewSuggestions={};state.reviewReport=null;state.reviewSHA='';state.reviewKinds=new Set();
    for(const [key,value]of Object.entries(state.reviewOptions)){const field=$('repair-options').elements.namedItem(key);if(field.type==='checkbox')field.checked=value;else field.value=value;}
    $('repair-language-select').value='';$('repair-custom-language').classList.add('hidden');
    $('repair-advanced').open=false;$('repair-technical').open=false;
    $('repair-review-title').textContent=book.title||book.filename||'Your book';
    $('repair-review-dialog').showModal();setBusy(false);requestRepairPreview();
  }
  function requestRepairPreview(delay=0){
    if(!state.reviewPath||state.repairing)return;
    clearTimeout(state.previewTimer);state.previewTimer=null;
    state.reviewGeneration++;state.repairPlan=null;state.reviewError='';state.previewing=true;
    renderRepairReview();
    if(delay){state.previewTimer=setTimeout(()=>{state.previewTimer=null;if(state.reviewPath&&!state.previewWorker)void runRepairPreview();},delay);}
    else if(!state.previewWorker)void runRepairPreview();
  }
  async function runRepairPreview(){
    state.previewWorker=true;
    try{
      while(state.reviewPath){
        if(state.previewTimer)break;
        const generation=state.reviewGeneration,path=state.reviewPath,options={...state.reviewOptions};
        try{
          const plan=await api.PreviewRepair(path,options);
          if(!state.reviewPath)return;
          if(generation!==state.reviewGeneration||path!==state.reviewPath)continue;
          if(!plan||!Array.isArray(plan.actions))throw new Error('The repair review was incomplete. Refresh it before saving.');
          state.repairPlan=plan;state.reviewError='';
          state.reviewReport=plan.report||state.reviewReport;
          if(state.reviewSHA!==plan.sourceSha256)state.reviewKinds=new Set();
          state.reviewSHA=plan.sourceSha256;
          for(const action of plan.actions)state.reviewKinds.add(action.kind);
          state.reviewSuggestions=plan.suggestions&&typeof plan.suggestions==='object'?plan.suggestions:{};
          if(plan.report?.path){upsert(plan.report);render();}
        }catch(error){
          if(!state.reviewPath)return;
          if(generation!==state.reviewGeneration||path!==state.reviewPath)continue;
          state.repairPlan=null;state.reviewError=errorText(error);
        }
        state.previewing=false;renderRepairReview();break;
      }
    }finally{state.previewWorker=false;if(!state.reviewPath){setBusy(false);render();drainQueue();}}
  }
  function languageName(tag){
    try{return new Intl.DisplayNames(['en'],{type:'language'}).of(tag)||tag;}catch{return tag;}
  }
  function selectRepairLanguage(tag){
    const select=$('repair-language-select');
    if(tag&&!Array.from(select.options).some(option=>option.value===tag)){
      const option=document.createElement('option');option.value=tag;option.textContent=languageName(tag);select.add(option,select.lastElementChild);
    }
    select.value=tag;$('repair-language').value=tag;state.reviewOptions.language=tag;
    $('repair-custom-language').classList.add('hidden');
  }
  function repairChoiceContext(){
    const issues=Array.isArray(state.reviewReport?.issues)?state.reviewReport.issues:[];
    const eligible=issue=>issue.repairable===true;
    return {
      issues,
      language:issues.some(issue=>/^METADATA_LANGUAGE(?:_INVALID)?$/.test(issue.code)&&eligible(issue)),
      identity:issues.some(issue=>issue.code==='METADATA_IDENTIFIER'&&issue.optionalRepair&&eligible(issue)),
      scripts:issues.some(issue=>['CONTENT_SCRIPT','CONTENT_EVENT_HANDLER','CONTENT_JAVASCRIPT_URL'].includes(issue.code)&&eligible(issue)),
    };
  }
  function simpleRepairActions(actions){
    // Repacking accompanies every edit; show it as a fix only when it is the only action.
    const visible=actions.some(action=>action.kind!=='packaging')?actions.filter(action=>action.kind!=='packaging'):actions;
    const labels={links:'Fix broken links',duplicate_ids:'Fix book structure',document_markup:'Fix formatting problems',metadata:'Correct book details',entities:'Standardize special characters',reader_cleanup:'Remove interactive features',packaging:'Fix EPUB file structure',xml:'Fix document formatting'};
    return [...new Set(visible.map(action=>labels[action.kind]||'Apply additional repairs'))];
  }
  function manualRepairHelp(issue){
    const off={LINK_MISSING:'fixLinks',LINK_FRAGMENT:'fixLinks',CONTENT_DUPLICATE_ID:'fixDuplicateIds',OPF_GUIDE_EMPTY:'fixDocumentMarkup',SVG_PRESERVE_ASPECT_RATIO:'fixDocumentMarkup',METADATA_LANGUAGE:'fixMetadata',METADATA_LANGUAGE_INVALID:'fixMetadata',METADATA_IDENTIFIER:'fixMetadata',NCX_IDENTIFIER_MISMATCH:'fixMetadata'}[issue.code];
    if(issue.repairable&&off&&!state.reviewOptions[off])return 'This fix is switched off. Turn it back on in Advanced options to include it.';
    if(issue.code==='LINK_MISSING'||issue.code==='LINK_FRAGMENT')return 'The correct destination could not be found. A corrected copy of the book may be needed.';
    if(/^METADATA_/.test(issue.code)||issue.code==='NCX_IDENTIFIER_MISMATCH')return 'These book details need to be corrected in an EPUB editor or by the book’s provider.';
    if(issue.code==='CONTENT_DUPLICATE_ID')return 'This needs a manual edit to avoid changing links or appearance.';
    if(issue.code==='XML_DTD_CHECK_INCOMPLETE')return 'Run EPUBCheck from Preferences for a complete check of this content.';
    if(issue.code==='CHAPTER_LARGE'||issue.code==='CONTENT_LARGE')return 'This is a compatibility warning. SpineGuard cannot safely simplify this chapter.';
    return 'SpineGuard cannot fix this automatically. Try a fresh download or ask the book’s provider for a corrected copy.';
  }
  function renderRepairReview(){
    const plan=state.repairPlan,actions=Array.isArray(plan?.actions)?plan.actions:[],unresolved=Array.isArray(plan?.unresolved)?plan.unresolved.filter(issue=>issue.severity!=='info'):[];
    const blocked=plan?.blockedReason||'',pending=state.previewing,context=repairChoiceContext();
    const metadataDisabled=!state.reviewOptions.fixMetadata||state.repairing||!!blocked;
    $('repair-review-dialog').setAttribute('aria-busy',String(pending||state.repairing));
    $('save-repair-button').disabled=pending||state.repairing||!!state.reviewError||!!blocked||!actions.length||!plan?.sourceSha256;
    $('save-repair-button').innerHTML=state.repairing?'<span class="spinner"></span>Saving copy…':`${icon('wrench')}Save repaired copy`;
    $('refresh-repair-preview').disabled=pending||state.repairing;
    $('refresh-repair-preview').classList.toggle('hidden',!state.reviewError);
    $('repair-options').disabled=state.repairing||!!blocked;
    $('repair-language-choice').classList.toggle('hidden',!context.language||!!blocked);
    $('repair-identity-choice').classList.toggle('hidden',!context.identity||!!blocked);
    $('repair-script-choice').classList.toggle('hidden',!context.scripts||!!blocked);
    $('repair-language').disabled=metadataDisabled;$('repair-language-select').disabled=metadataDisabled;
    $('repair-options').elements.namedItem('generateIdentifier').disabled=metadataDisabled;
    const detected=typeof state.reviewSuggestions.language==='string'?state.reviewSuggestions.language:'';
    const sources=Array.isArray(state.reviewSuggestions.languageSources)?state.reviewSuggestions.languageSources:[];
    $('language-suggestion').classList.toggle('hidden',!detected);
    $('use-detected-language').textContent=`Use ${languageName(detected)}`;
    $('use-detected-language').disabled=!detected||metadataDisabled||pending;
    $('language-suggestion-source').textContent=detected?(sources.length?`Language suggestion: ${detected}. Found in: ${sources.join(', ')}`:`Language suggestion: ${detected}, from the book’s language markup.`):'';
    const categories={
      links:context.issues.some(issue=>['LINK_MISSING','LINK_FRAGMENT'].includes(issue.code)&&issue.repairable),
      duplicate_ids:context.issues.some(issue=>issue.code==='CONTENT_DUPLICATE_ID'&&issue.repairable),
      document_markup:context.issues.some(issue=>['OPF_GUIDE_EMPTY','SVG_PRESERVE_ASPECT_RATIO'].includes(issue.code)&&issue.repairable),
      metadata:context.issues.some(issue=>(/^METADATA_/.test(issue.code)||issue.code==='NCX_IDENTIFIER_MISMATCH')&&issue.repairable),
    };
    document.querySelectorAll('[data-repair-category]').forEach(label=>label.classList.toggle('hidden',!categories[label.dataset.repairCategory]&&!state.reviewKinds.has(label.dataset.repairCategory)));
    $('repair-advanced').classList.toggle('hidden',!state.reviewReport||!!blocked);
    $('repair-review-dialog').querySelectorAll('[data-close]').forEach(button=>{button.disabled=state.repairing;});
    if(pending||state.reviewError)$('repair-technical').classList.add('hidden');
    if(pending){$('repair-plan-summary').innerHTML='<div class="busy-indicator" role="status"><span class="spinner"></span>Checking available repairs…</div>';$('repair-plan-actions').innerHTML='';$('repair-plan-unresolved').innerHTML='';return;}
    if(state.reviewError){$('repair-plan-summary').innerHTML=`<div class="repair-review-message error" role="alert"><strong>Could not prepare repairs</strong><p>${esc(state.reviewError)}</p><p>Correct your choice or refresh the review to try again.</p></div>`;$('repair-plan-actions').innerHTML='';$('repair-plan-unresolved').innerHTML='';return;}
    const needsLanguage=context.language&&state.reviewOptions.fixMetadata&&!state.reviewOptions.language;
    const switchedOff=Object.entries(categories).some(([kind,available])=>available&&!state.reviewOptions[{links:'fixLinks',duplicate_ids:'fixDuplicateIds',document_markup:'fixDocumentMarkup',metadata:'fixMetadata'}[kind]]);
    const heading=blocked?'This copy cannot be repaired automatically':actions.length?'Ready to save a repaired copy':needsLanguage?'Choose the book’s language':switchedOff?'Repairs are switched off':context.identity?'A book identifier is missing':context.scripts?'Optional change available':unresolved.length?'These problems need a manual fix':'Nothing to fix';
    const help=blocked?'Try a fresh download or ask the book’s provider for a corrected copy.':actions.length?'We’ll apply the fixes below and check the new copy.':needsLanguage?'Confirm the language below to fix the missing or invalid book detail.':switchedOff?'Turn fixes back on in Advanced options to include them.':context.identity?'You can add one to a separate copy below.':context.scripts?'You can remove interactive features from a separate copy.':unresolved.length?'SpineGuard cannot repair them safely. See the next steps below.':'The current check found no problems that need a repair.';
    $('repair-plan-summary').innerHTML=`<div class="repair-review-message${blocked?' error':''}"><strong>${esc(heading)}</strong><p>${esc(help)}</p></div>`;
    const simpleActions=simpleRepairActions(actions);
    $('repair-plan-actions').innerHTML=simpleActions.length?`<h3>Fixes to apply</h3><ul class="repair-simple-list">${simpleActions.map(label=>`<li><span>${icon('check')}</span>${esc(label)}</li>`).join('')}</ul>`:'';
    const isChoice=issue=>!blocked&&issue.repairable===true&&((context.language&&/^METADATA_LANGUAGE(?:_INVALID)?$/.test(issue.code))||(context.identity&&issue.optionalRepair&&issue.code==='METADATA_IDENTIFIER')||(context.scripts&&['CONTENT_SCRIPT','CONTENT_EVENT_HANDLER','CONTENT_JAVASCRIPT_URL'].includes(issue.code)));
    const manual=unresolved.filter(issue=>!isChoice(issue));
    const groupedManual=new Map();
    for(const issue of manual){const key=issue.code+'\x00'+(issue.title||'')+'\x00'+manualRepairHelp(issue);if(!groupedManual.has(key))groupedManual.set(key,{issue,count:0});groupedManual.get(key).count++;}
    $('repair-plan-unresolved').innerHTML=manual.length&&!blocked?`<h3>Still needs attention <span class="neutral-badge">${number(manual.length)}</span></h3><div class="review-unresolved-list">${Array.from(groupedManual.values()).map(({issue,count})=>`<article class="review-unresolved"><strong>${esc(issue.title||'Book problem')}${count>1?` <span class="neutral-badge">${number(count)}</span>`:''}</strong><p>${esc(manualRepairHelp(issue))}</p></article>`).join('')}</div>`:'';
    const details=actions.length?`<h3>Exact changes</h3><ul class="repair-plan-list">${actions.map(action=>`<li><strong>${esc(action.description||action.kind||'Repair')}</strong>${action.file?`<span class="issue-file">${esc(action.file)}</span>`:''}${action.requiresOptIn?`<span class="repair-count">Selected optional change</span>`:''}</li>`).join('')}</ul>`:'';
    const remaining=unresolved.length?`<h3>Remaining findings (${number(unresolved.length)})</h3><div class="review-unresolved-list">${unresolved.map(issue=>`<article class="review-unresolved"><strong>${esc(issue.title||issue.code||'Finding')}</strong>${issue.file?`<span class="issue-file">${esc(issue.file)}</span>`:''}<p>${esc(repairExplanation(issue)||issue.detail||'No automatic correction is available.')}</p></article>`).join('')}</div>`:'';
    $('repair-technical-content').innerHTML=`${blocked?`<p class="review-section-note">${esc(blocked)}</p>`:''}${plan?.report?.epubcheck?'<p class="review-section-note">Preview predictions cover built-in checks. EPUBCheck is run again on the saved copy.</p>':''}${details}${remaining}`;
    $('repair-technical').classList.toggle('hidden',!details&&!remaining&&!blocked&&!detected&&!plan?.report?.epubcheck);
  }
  async function saveReviewedRepair(){
    const plan=state.repairPlan,path=state.reviewPath;
    if(!path||!plan||state.previewing||state.repairing||plan.blockedReason||!plan.actions?.length||!plan.sourceSha256||state.reviewError)return;
    const options={...state.reviewOptions,expectedSha256:plan.sourceSha256};
    state.repairing=true;setBusy(false);
    renderRepairReview();
    try{
      const result=await api.RepairBookWithOptions(path,options);
      if(!result?.outputPath)return;
      $('repair-review-dialog').close();
      state.repairedPath=result.outputPath;
      upsert(result.report);
      state.selected=result.report?.path||state.selected;
      render();
      const remaining=Number(result.report?.errors||0)+Number(result.report?.warnings||0);
      const outcome=remaining
        ?` ${number(remaining)} ${remaining===1?'finding still needs':'findings still need'} attention.`
        :' No errors or warnings were detected by the checks that ran.';
      $('repair-result').innerHTML=`
        <div class="repair-result-banner"><span>${icon('check-circle')}</span><div>
          <h3>A fresh copy, safely saved.</h3>
          <p>Your original file has not been changed. The repaired book was checked again.${outcome}</p>
        </div></div>
        <h3 class="result-section-title">What changed</h3>
        <ul class="repair-changes">${simpleRepairActions(plan.actions||[]).map(change=>`<li><span>${icon('check')}</span>${esc(change)}</li>`).join('')||'<li>No automatic changes were necessary.</li>'}</ul>
        ${(result.changes||[]).length?`<details class="repair-disclosure"><summary>Technical details</summary><ul class="repair-changes">${result.changes.map(change=>`<li>${esc(change)}</li>`).join('')}</ul></details>`:''}
        <h3 class="result-section-title">Saved copy</h3>
        <div class="repair-path">${esc(result.outputPath)}</div>
        ${remaining?'<p class="detail-note repair-followup">Review the repaired copy’s findings in the library before sideloading.</p>':''}`;
      $('result-dialog').showModal();
    }
    catch(error){state.reviewError=errorText(error);state.repairPlan=null;}
    finally{state.repairing=false;setBusy(false);render();if(state.reviewPath)renderRepairReview();drainQueue();}
  }
  function bulkReady(item){return !item?.error&&!item?.unreviewed&&!item?.plan?.blockedReason&&Array.isArray(item?.plan?.actions)&&item.plan.actions.length>0&&/^[a-f0-9]{64}$/i.test(item.plan.sourceSha256||'');}
  function bulkStatus(item){
    if(item.unreviewed)return 'Not checked';
    if(item.error)return 'Could not check';
    if(item.plan?.blockedReason)return 'Cannot repair';
    if(bulkReady(item))return 'Ready';
    return item.plan?.report?.issues?.some(issue=>issue.severity!=='info')?'Needs individual review':'No repairs needed';
  }
  function renderBulk(){
    const working=state.bulkWorking,result=state.bulkResult;
    $('bulk-repair-dialog').setAttribute('aria-busy',String(working));
    $('bulk-close-button').disabled=working;$('bulk-done-button').disabled=working;
    $('bulk-progress').classList.toggle('hidden',!working);
    $('bulk-cancel-button').classList.toggle('hidden',!working);$('bulk-cancel-button').disabled=state.bulkCancelling;
    $('bulk-cancel-button').textContent=state.bulkCancelling?'Stopping…':state.bulkPhase==='repair'?'Stop saving':'Stop review';
    $('bulk-save-button').classList.toggle('hidden',!!result);$('bulk-save-button').disabled=working||!!state.bulkError||!state.bulkItems.some(item=>bulkReady(item)&&state.bulkIncluded.has(item.path));
    const retryPaths=result?.items?.filter(item=>item.status!=='saved').map(item=>item.path)||[];
    $('bulk-refresh-button').classList.toggle('hidden',working||!(state.bulkError||state.bulkCancelled||retryPaths.length));
    $('bulk-refresh-button').textContent=result?'Review remaining':'Review again';
    const progress=state.bulkProgress,total=Number(progress.total||state.bulkPaths.length),index=Math.min(Number(progress.index||0),total);
    $('bulk-progress-title').textContent=state.bulkPhase==='repair'?`Saving copies · ${number(index)} of ${number(total)}`:`Reviewing books · ${number(index)} of ${number(total)}`;
    $('bulk-progress-path').textContent=state.reports.find(book=>book.path===progress.path)?.title||progress.path||'';
    if(result){
      const items=Array.isArray(result.items)?result.items:[],saved=items.filter(item=>item.status==='saved'),failed=items.filter(item=>item.status==='failed'),cancelled=items.filter(item=>item.status==='cancelled');
      $('bulk-summary').innerHTML=`<div class="repair-review-message"><strong>${result.cancelled?'Saving stopped':'Copies saved'}</strong><p>${number(saved.length)} saved · ${number(failed.length)} failed · ${number(cancelled.length)} not saved. Your original books are unchanged.</p>${saved.length?'<button class="button secondary small" data-bulk-reveal-first>Show saved copies</button>':''}</div>`;
      $('bulk-items').innerHTML=items.map((item,index)=>{const book=state.reports.find(book=>book.path===item.path),remaining=Number(item.report?.errors||0)+Number(item.report?.warnings||0);return `<article class="bulk-book-item"><div class="bulk-book-title"><strong>${esc(book?.title||book?.filename||item.path)}</strong><span class="bulk-status ${item.status==='saved'?'ready':''}">${item.status==='saved'?'Saved':item.status==='failed'?'Failed':'Not saved'}</span></div><p>${item.status==='saved'?(remaining?`${number(remaining)} ${remaining===1?'finding remains':'findings remain'} in the repaired copy.`:'No issues detected by the checks that ran.'):esc(item.error||(item.status==='cancelled'?'Stopped before this copy was saved.':'This copy could not be saved.'))}</p>${item.outputPath?`<button class="button ghost small" data-bulk-reveal-index="${index}">Show file</button>`:''}</article>`;}).join('');
      return;
    }
    const ready=state.bulkItems.filter(bulkReady),included=ready.filter(item=>state.bulkIncluded.has(item.path)).length,skipped=state.bulkPaths.length-ready.length;
    $('bulk-summary').innerHTML=state.bulkError?`<div class="repair-review-message error" role="alert"><strong>Could not complete bulk repair</strong><p>${esc(state.bulkError)}</p><p>Review the books again before retrying.</p></div>`:working?'<p class="review-section-note">Checking each book for available fixes. Nothing is saved during this review.</p>':`<div class="repair-review-message"><strong>${state.bulkCancelled?'Review stopped':`${number(included)} ${included===1?'book':'books'} selected for repair`}</strong><p>${skipped?`${number(skipped)} ${skipped===1?'book needs':'books need'} no automatic repair or a separate review. `:''}Only the selected fixes will be applied. Language choices and optional cleanup stay in each book’s individual review.</p></div>`;
    $('bulk-items').innerHTML=state.bulkItems.map((item,index)=>{const ready=bulkReady(item),status=bulkStatus(item),book=state.reports.find(book=>book.path===item.path),title=item.title||book?.title||book?.filename||item.path,fixes=ready?simpleRepairActions(item.plan.actions):[],remaining=(item.plan?.unresolved||[]).filter(issue=>issue.severity!=='info').length;
      const reason=item.unreviewed?'This book was not checked. Review again to include it.':item.error||item.plan?.blockedReason||(ready?(remaining?'Some findings will remain for individual review.':'The repaired copy will be checked again.'):status==='No repairs needed'?'No supported changes are needed.':'Confirm missing details or review the remaining findings for this book.');
      return `<article class="bulk-book-item"><div class="bulk-book-title"><label class="bulk-include"><input type="checkbox" data-bulk-index="${index}" aria-label="Include ${esc(title)}" ${ready&&state.bulkIncluded.has(item.path)?'checked':''} ${!ready||working?'disabled':''}><strong>${esc(title)}</strong></label><span class="bulk-status ${ready?'ready':''}">${esc(status)}</span></div>${fixes.length?`<ul class="bulk-fixes">${fixes.map(fix=>`<li>${esc(fix)}</li>`).join('')}</ul>`:''}<p>${esc(reason)}</p>${status!=='No repairs needed'?`<button class="button ghost small" data-bulk-review-index="${index}" ${working?'disabled':''}>Review individually</button>`:''}</article>`;
    }).join('');
  }
  async function previewBulk(paths){
    if(state.bulkWorking)return;
    state.bulkPaths=[...new Set(paths)];state.bulkItems=[];state.bulkIncluded=new Set();state.bulkResult=null;state.bulkError='';state.bulkCancelled=false;state.bulkCancelling=false;state.bulkWorking=true;state.bulkPhase='preview';state.bulkProgress={index:0,total:state.bulkPaths.length};
    setBusy(false);renderBulk();
    try{
      const result=await api.PreviewBulkRepair(state.bulkPaths);
      if(!result||!Array.isArray(result.items))throw new Error('The bulk review was incomplete. Review the books again.');
      const seen=new Set();
      state.bulkItems=result.items.filter(item=>item&&state.bulkPaths.includes(item.path)&&!seen.has(item.path)&&(seen.add(item.path)||true));
      for(const path of state.bulkPaths)if(!seen.has(path)){const book=state.reports.find(book=>book.path===path);state.bulkItems.push({path,title:book?.title||book?.filename||path,unreviewed:true});}
      state.bulkCancelled=!!result.cancelled;
      for(const item of state.bulkItems){if(item.plan?.report?.path)upsert(item.plan.report);if(bulkReady(item))state.bulkIncluded.add(item.path);}
    }catch(error){state.bulkError=errorText(error);state.bulkItems=[];state.bulkIncluded.clear();}
    finally{state.bulkWorking=false;state.bulkCancelling=false;setBusy(false);render();renderBulk();}
  }
  function startBulk(){
    if(workBlocked()||!state.selectedBooks.size)return;
    const paths=state.reports.filter(book=>state.selectedBooks.has(book.path)).map(book=>book.path);if(!paths.length)return;
    state.bulkOpen=true;$('bulk-repair-dialog').showModal();void previewBulk(paths);
  }
  async function saveBulk(){
    if(state.bulkWorking||state.bulkError||state.bulkResult)return;
    const items=state.bulkItems.filter(item=>bulkReady(item)&&state.bulkIncluded.has(item.path)).map(item=>({path:item.path,options:{...repairDefaults,expectedSha256:item.plan.sourceSha256}}));if(!items.length)return;
    state.bulkWorking=true;state.bulkPhase='repair';state.bulkCancelling=false;state.bulkProgress={index:0,total:items.length};setBusy(false);renderBulk();
    try{
      const result=await api.RepairBooks(items);
      if(!result||!Array.isArray(result.items))throw new Error('The bulk repair result was incomplete. Review the books again.');
      // Cancelling the native folder picker leaves this reviewed selection available.
      if(!result.directory&&!result.items.length)return;
      state.bulkResult=result;state.bulkCancelled=!!result.cancelled;
      for(const item of result.items)if(item.status==='saved'){if(item.report?.path)upsert(item.report);state.selectedBooks.delete(item.path);}
    }catch(error){state.bulkError=errorText(error);state.bulkIncluded.clear();}
    finally{state.bulkWorking=false;state.bulkCancelling=false;setBusy(false);render();renderBulk();}
  }
  async function stopBulk(){
    if(!state.bulkWorking||state.bulkCancelling)return;state.bulkCancelling=true;renderBulk();
    try{await api.CancelScan();}catch(error){state.bulkCancelling=false;toast(errorText(error),'error');renderBulk();}
  }
  function openSettings(){
    const form=$('settings-form');for(const [key,value]of Object.entries({...defaults,...state.settings})){const field=form.elements.namedItem(key);if(!field)continue;if(field.type==='checkbox')field.checked=!!value;else field.value=value??'';}$('settings-dialog').showModal();
  }
  async function saveSettings(event){
    event.preventDefault();const form=event.currentTarget;if(!form.reportValidity())return;
    const next={...state.settings,wordsPerPage:Number(form.elements.wordsPerPage.value),readingWPM:Number(form.elements.readingWPM.value),recursive:form.elements.recursive.checked,runEpubcheck:form.elements.runEpubcheck.checked,epubcheckJar:form.elements.epubcheckJar.value.trim(),javaPath:form.elements.javaPath.value.trim()||'java',theme:form.elements.theme.value};
    if(next.runEpubcheck&&!next.epubcheckJar){toast('Choose epubcheck.jar before enabling EPUBCheck.','error');form.elements.epubcheckJar.focus();return;}
    const button=form.querySelector('[type=submit]');button.disabled=true;
    try{state.settings={...next,...await api.SaveSettings(next)};setTheme(state.settings.theme);$('settings-dialog').close();state.reports=state.reports.map(book=>({...book,stats:{...book.stats,estimatedPages:estimatedPages(book),readingMinutes:estimatedMinutes(book)}}));render();toast('Preferences saved. Reading estimates updated.');}catch(error){toast(errorText(error),'error');}finally{button.disabled=false;}
  }
  async function exportReports(format){$('export-menu').classList.add('hidden');$('export-button').setAttribute('aria-expanded','false');try{const saved=await api.ExportReports(format);if(saved)toast(`Saved ${format.toLocaleUpperCase()} report.`);}catch(error){toast(errorText(error),'error');}}
  async function reveal(path){try{await api.RevealFile(path);}catch(error){toast(errorText(error),'error');}}

  function wireEvents(){
    document.addEventListener('spineguard:reader-state',()=>{setBusy(state.running);if(!workBlocked())drainQueue();});
    $('book-rows').addEventListener('dblclick',event=>{const row=event.target.closest('[data-book-index]');const book=row&&state.reports[Number(row.dataset.bookIndex)];if(book&&!workBlocked())void window.SpineGuardReader?.open(book.path);});
    $('add-button').addEventListener('click',chooseFiles);$('empty-add-button').addEventListener('click',chooseFiles);$('folder-button').addEventListener('click',chooseFolder);
    $('rescan-library-button').addEventListener('click',()=>{if(!workBlocked())void scan(state.reports.map(book=>book.path));});
    for(const view of ['list','covers'])$(`library-view-${view}`).addEventListener('click',()=>{if(workBlocked())return;state.view=view;saveLibraryPreferences();render();});
    $('library-group').addEventListener('change',event=>{state.group=event.target.value;if(state.group==='series')state.sort='series';saveLibraryPreferences();render();});
    $('library-sort').addEventListener('change',event=>{state.sort=event.target.value;saveLibraryPreferences();render();});
    $('select-books-button').addEventListener('click',()=>{if(workBlocked())return;state.selecting=!state.selecting;render();});
    $('select-visible-books').addEventListener('click',()=>{if(workBlocked())return;visibleReports().forEach(book=>state.selectedBooks.add(book.path));render();});
    $('clear-selected-books').addEventListener('click',()=>{if(workBlocked())return;state.selectedBooks.clear();render();});
    $('review-selected-books').addEventListener('click',startBulk);
    $('book-rows').addEventListener('change',event=>{const checkbox=event.target.closest('[data-select-book-index]');if(!checkbox||workBlocked())return;const book=state.reports[Number(checkbox.dataset.selectBookIndex)];if(!book)return;if(checkbox.checked)state.selectedBooks.add(book.path);else state.selectedBooks.delete(book.path);checkbox.closest('.book-entry').classList.toggle('included',checkbox.checked);renderSelection();});
    $('settings-nav').addEventListener('click',openSettings);$('help-button').addEventListener('click',()=>$('help-dialog').showModal());$('library-nav').addEventListener('click',()=>{state.filter='all';state.query='';$('search-input').value='';render();});
    $('settings-form').addEventListener('submit',saveSettings);
    $('browse-epubcheck').addEventListener('click',async()=>{try{const path=await api.SelectEPUBCheck();if(path)$('settings-form').elements.epubcheckJar.value=path;}catch(error){toast(errorText(error),'error');}});
    document.querySelectorAll('[data-close]').forEach(button=>button.addEventListener('click',()=>$(button.dataset.close).close()));
    document.querySelectorAll('[data-filter]').forEach(button=>button.addEventListener('click',()=>{state.filter=button.dataset.filter;render();}));
    $('search-input').addEventListener('input',event=>{state.query=event.target.value.trim().toLocaleLowerCase();render();});
    $('book-rows').addEventListener('click',event=>{const row=event.target.closest('[data-book-index]');if(!row)return;state.selected=state.reports[Number(row.dataset.bookIndex)]?.path;render();});
    $('book-detail').addEventListener('click',event=>{const book=state.reports.find(item=>item.path===state.selected);const concern=event.target.closest('[data-reader-issue]');if(concern){if(book&&!workBlocked())void window.SpineGuardReader?.open(book.path,Number(concern.dataset.readerIssue));return;}const button=event.target.closest('[data-detail-action]');if(!button)return;switch(button.dataset.detailAction){case'read':if(book&&!workBlocked())void window.SpineGuardReader?.open(book.path);break;case'close':state.selected=null;render();break;case'rescan':if(book)void scan([book.path]);break;case'repair':void repair();break;case'reveal':if(book)void reveal(book.path);break;}});
    $('cancel-button').addEventListener('click',async()=>{state.cancelling=true;state.queued=[];$('cancel-button').disabled=true;$('cancel-button').textContent='Cancelling…';try{await api.CancelScan();}catch(error){toast(errorText(error),'error');}});
    $('export-button').addEventListener('click',event=>{event.stopPropagation();const open=$('export-menu').classList.toggle('hidden')===false;$('export-button').setAttribute('aria-expanded',String(open));});
    document.querySelectorAll('[data-export]').forEach(button=>button.addEventListener('click',()=>exportReports(button.dataset.export)));
    document.addEventListener('click',event=>{if(!event.target.closest('.export-container')){$('export-menu').classList.add('hidden');$('export-button').setAttribute('aria-expanded','false');}});
    $('clear-button').addEventListener('click',()=>$('confirm-dialog').showModal());$('confirm-clear-button').addEventListener('click',async()=>{try{await api.ClearReports();state.reports=[];state.selected=null;state.filter='all';state.query='';$('search-input').value='';render();$('confirm-dialog').close();toast('Reports cleared. Your books are untouched.');}catch(error){toast(errorText(error),'error');}});
    $('reveal-repair-button').addEventListener('click',()=>reveal(state.repairedPath));
    $('repair-options').addEventListener('change',event=>{
      if(event.target.id==='repair-language-select')return;
      for(const key of Object.keys(repairDefaults)){const field=$('repair-options').elements.namedItem(key);state.reviewOptions[key]=field.type==='checkbox'?field.checked:field.value.trim();}
      if(event.target.name==='language'){if(state.previewTimer)requestRepairPreview();}
      else requestRepairPreview();
    });
    $('repair-language').addEventListener('input',event=>{state.reviewOptions.language=event.target.value.trim();requestRepairPreview(300);});
    $('repair-language-select').addEventListener('change',event=>{
      if(event.target.value==='custom'){$('repair-custom-language').classList.remove('hidden');$('repair-language').focus();return;}
      selectRepairLanguage(event.target.value);requestRepairPreview();
    });
    $('use-detected-language').addEventListener('click',()=>{
      const language=state.reviewSuggestions.language;if(typeof language!=='string'||!language||!state.reviewOptions.fixMetadata||state.repairing)return;
      selectRepairLanguage(language);requestRepairPreview();
    });
    $('refresh-repair-preview').addEventListener('click',()=>requestRepairPreview());
    $('save-repair-button').addEventListener('click',saveReviewedRepair);
    $('repair-review-dialog').addEventListener('cancel',event=>{if(state.repairing)event.preventDefault();});
    $('repair-review-dialog').addEventListener('close',()=>{clearTimeout(state.previewTimer);state.previewTimer=null;state.reviewPath='';state.repairPlan=null;state.reviewError='';state.reviewSuggestions={};state.previewing=false;state.reviewGeneration++;setBusy(false);render();drainQueue();});
    $('bulk-save-button').addEventListener('click',saveBulk);$('bulk-cancel-button').addEventListener('click',stopBulk);
    for(const id of ['bulk-close-button','bulk-done-button'])$(id).addEventListener('click',()=>{if(!state.bulkWorking)$('bulk-repair-dialog').close();});
    $('bulk-repair-dialog').addEventListener('cancel',event=>{if(state.bulkWorking)event.preventDefault();});
    $('bulk-repair-dialog').addEventListener('close',()=>{state.bulkOpen=false;state.bulkIncluded.clear();state.bulkError='';state.bulkResult=null;setBusy(false);render();drainQueue();});
    $('bulk-refresh-button').addEventListener('click',()=>{const paths=state.bulkResult?state.bulkResult.items.filter(item=>item.status!=='saved').map(item=>item.path):state.bulkPaths;void previewBulk(paths);});
    $('bulk-items').addEventListener('change',event=>{const checkbox=event.target.closest('[data-bulk-index]');if(!checkbox||state.bulkWorking)return;const item=state.bulkItems[Number(checkbox.dataset.bulkIndex)];if(!bulkReady(item))return;const focused=document.activeElement===checkbox,path=item.path;if(checkbox.checked)state.bulkIncluded.add(path);else state.bulkIncluded.delete(path);renderBulk();if(focused){const index=state.bulkItems.findIndex(entry=>entry.path===path),replacement=$('bulk-items').querySelector(`[data-bulk-index="${index}"]`);if(replacement&&!replacement.disabled)replacement.focus({preventScroll:true});}});
    $('bulk-items').addEventListener('click',event=>{
      const individual=event.target.closest('[data-bulk-review-index]');if(individual&&!state.bulkWorking){const item=state.bulkItems[Number(individual.dataset.bulkReviewIndex)];if(!item)return;$('bulk-repair-dialog').close();state.bulkOpen=false;state.selected=item.path;render();repair();return;}
      const revealButton=event.target.closest('[data-bulk-reveal-index]');if(revealButton){const item=state.bulkResult?.items?.[Number(revealButton.dataset.bulkRevealIndex)];if(item?.outputPath)void reveal(item.outputPath);}
    });
    $('bulk-summary').addEventListener('click',event=>{if(event.target.closest('[data-bulk-reveal-first]')){const first=state.bulkResult?.items?.find(item=>item.status==='saved'&&item.outputPath);if(first)void reveal(first.outputPath);}});
    document.addEventListener('keydown',event=>{const editing=/INPUT|TEXTAREA|SELECT/.test(document.activeElement?.tagName);if((event.ctrlKey||event.metaKey)&&event.key.toLocaleLowerCase()==='o'){event.preventDefault();if(!state.running&&!state.repairing&&!document.querySelector('dialog[open]'))void chooseFiles();}else if(event.key==='/'&&!editing&&!document.querySelector('dialog[open]')){event.preventDefault();$('search-input').focus();}else if(event.key==='Escape'){$('export-menu').classList.add('hidden');$('drop-overlay').classList.add('hidden');}});
    let dragDepth=0;document.addEventListener('dragenter',event=>{if(event.dataTransfer?.types?.includes('Files')){event.preventDefault();dragDepth++;$('drop-overlay').classList.remove('hidden');}});document.addEventListener('dragleave',()=>{dragDepth=Math.max(0,dragDepth-1);if(!dragDepth)$('drop-overlay').classList.add('hidden');});document.addEventListener('dragover',event=>{event.preventDefault();});document.addEventListener('drop',event=>{event.preventDefault();dragDepth=0;$('drop-overlay').classList.add('hidden');});window.addEventListener('blur',()=>{dragDepth=0;$('drop-overlay').classList.add('hidden');});
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change',()=>{if(state.settings.theme==='system')setTheme('system');});
  }
  function wireRuntime(){
    const runtime=window.runtime;
    if(!runtime)return;
    runtime.EventsOn?.('scan:progress',progress=>{if(progress?.report)upsert(progress.report);state.progress=progress||{};if(progress?.total){$('progress-title').textContent=`Checking book ${number(Math.min(progress.index||1,progress.total))} of ${number(progress.total)}`;$('progress-fill').style.width=`${Math.min(100,Math.max(0,((progress.index||0)/progress.total)*100))}%`;}$('progress-path').textContent=progress?.path||'';render();});
    runtime.EventsOn?.('scan:state',event=>{if(event?.running&&!state.running)setBusy(true);});
    runtime.EventsOn?.('bulk:progress',progress=>{if(!state.bulkOpen||!state.bulkWorking)return;state.bulkProgress=progress||{};if(progress?.report)upsert(progress.report);const item=progress?.previewItem;if(item?.path&&state.bulkPhase==='preview'){const index=state.bulkItems.findIndex(entry=>entry.path===item.path);if(index>=0)state.bulkItems[index]=item;else state.bulkItems.push(item);}renderBulk();});
    runtime.OnFileDrop?.((_x,_y,paths)=>{ $('drop-overlay').classList.add('hidden');void scan(paths);},true);
    runtime.EventsOn?.('app:open',paths=>{if(Array.isArray(paths))void scan(paths);});
    runtime.EventsOn?.('app:warning',message=>{if(message)toast(typeof message==='string'?message:(message.detail||message.message||'The library could not be saved.'),'error',14000);});
  }
  async function init(){
    loadLibraryPreferences();hydrateIcons();wireEvents();wireRuntime();render();
    $('settings-form').elements.wordsPerPage.min='100';
    $('settings-form').elements.readingWPM.min='80';
    if(/Mac/i.test(navigator.platform))$('open-shortcut').textContent='⌘ O';
    if(window.spineguardPreview){const banner=document.createElement('div');banner.className='preview-banner';banner.textContent='Preview data — desktop interaction adapter';document.querySelector('.main-content').prepend(banner);}
    try{const result=await api.GetState();state.settings={...defaults,...(result?.settings||{})};state.version=result?.version||'1.4.0';state.reports=Array.isArray(result?.reports)?result.reports:[];state.selected=state.reports[0]?.path||null;setTheme(state.settings.theme);$('app-version').textContent=`v${state.version}`;render();if(result?.warning)toast(result.warning,'error',14000);}
    catch(error){toast(errorText(error),'error',15000);}
  }
  void init();
})();
