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
  const state = {reports:[],settings:{...defaults},selected:null,filter:'all',query:'',running:false,repairing:false,cancelling:false,repairedPath:'',version:'1.1.0',progress:{index:0,total:0},queued:[],reviewPath:'',repairPlan:null,reviewError:'',reviewOptions:{fixLinks:true,fixDuplicateIds:true,removeScripts:false},previewing:false,previewWorker:false,reviewGeneration:0};
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
  function setBusy(running){
    state.running=running;$('scan-progress').classList.toggle('hidden',!running);
    for(const id of ['add-button','folder-button','empty-add-button','clear-button','rescan-library-button'])$(id).disabled=running||state.repairing||!!state.reviewPath||state.previewWorker;
    $('rescan-library-button').disabled=$('rescan-library-button').disabled||!state.reports.length;
    $('cancel-button').disabled=state.cancelling;$('cancel-button').textContent=state.cancelling?'Cancelling…':'Cancel scan';
    $('export-button').disabled=!state.reports.length||running||state.repairing||!!state.reviewPath||state.previewWorker;
    if(running){$('progress-title').textContent='Checking your books…';$('progress-fill').style.width='0%';}
    renderDetail();
  }
  function upsert(report){
    if(!report?.path)return;
    const index=state.reports.findIndex(book=>book.path===report.path);
    if(index>=0)state.reports[index]=report;else state.reports.push(report);
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
      return !state.query||[book.title,book.author,book.filename].join(' ').toLocaleLowerCase().includes(state.query);
    });
  }
  function cover(book,large=false){
    const seed=Array.from(book.title||book.filename||'S').reduce((n,c)=>n+c.codePointAt(0),0);
    const safeCover=typeof book.coverData==='string'&&book.coverData.length<4000000&&/^data:image\/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/=\s]+$/.test(book.coverData);
    return `<span class="book-cover cover-tone-${seed%6}${large?' large':''}">${safeCover?`<img src="${esc(book.coverData)}" alt="Book cover">`:esc(Array.from(book.title||book.filename||'S')[0].toLocaleUpperCase())}</span>`;
  }
  function render(){
    state.reports.forEach(book=>{if(book.stats){book.stats.estimatedPages=estimatedPages(book);book.stats.readingMinutes=estimatedMinutes(book);}});
    const total=state.reports.length,healthy=state.reports.filter(b=>b.status==='healthy').length,attention=state.reports.filter(hasAttention).length,unreadable=state.reports.filter(b=>b.status==='unreadable').length;
    $('nav-count').textContent=number(total);$('total-books').textContent=number(total);$('healthy-books').textContent=number(healthy);$('attention-books').textContent=number(attention+unreadable);
    $('all-count').textContent=number(total);$('healthy-count').textContent=number(healthy);$('attention-count').textContent=number(attention);$('unreadable-count').textContent=number(unreadable);
    $('empty-state').classList.toggle('hidden',total>0);$('export-button').disabled=!total||state.running||state.repairing||!!state.reviewPath||state.previewWorker;$('clear-button').disabled=!total||state.running||state.repairing||!!state.reviewPath||state.previewWorker;
    $('library-footer-text').textContent=total?`${number(total)} ${total===1?'book':'books'} · ${number(state.reports.reduce((sum,b)=>sum+Number(b.stats?.words||0),0))} words`:'Ready for your first book';
    $('rescan-library-button').disabled=!total||state.running||state.repairing||!!state.reviewPath||state.previewWorker;
    const visible=visibleReports();$('no-results').classList.toggle('hidden',!total||visible.length>0);
    $('book-rows').innerHTML=visible.map(book=>`<button class="book-row${book.path===state.selected?' selected':''}" data-book-index="${state.reports.indexOf(book)}" aria-label="View ${esc(book.title||book.filename)}" aria-pressed="${book.path===state.selected}"><span class="book-summary">${cover(book)}<span class="book-title-text"><span class="book-title" title="${esc(book.title||book.filename)}">${esc(book.title||book.filename||'Untitled book')}</span><span class="book-author" title="${esc(book.author||book.filename)}">${esc(book.author||book.filename||'Unknown author')}</span></span></span><span>${badge(book)}</span><span class="row-number">${book.status==='unreadable'?'—':number(book.stats?.words)}</span><span class="row-number">${book.status==='unreadable'?'—':number(book.stats?.estimatedPages)}</span></button>`).join('');
    document.querySelectorAll('[data-filter]').forEach(button=>button.classList.toggle('active',button.dataset.filter===state.filter));
    renderDetail();
  }
  function renderDetail(){
    const panel=$('book-detail'),source=state.reports.find(item=>item.path===state.selected);
    const book=source?{...source,stats:{...source.stats,estimatedPages:estimatedPages(source),readingMinutes:estimatedMinutes(source)}}:undefined;
    if(!book){panel.classList.add('hidden');return;}
    panel.classList.remove('hidden');const stats=book.stats||{},issues=Array.isArray(book.issues)?book.issues:[];
    const repairable=issues.some(issue=>issue.repairable),busy=state.running||state.repairing;
    const healthCopy=book.status==='healthy'?'The checks that ran found no issues. Explore the book’s stats below.':book.status==='unreadable'?'This book could not be fully read. Review the findings before adding it to your reader.':`${number(book.errors)} ${book.errors===1?'error':'errors'} and ${number(book.warnings)} ${book.warnings===1?'warning':'warnings'} found.${repairable?' Some findings have a repair available.':' Review the findings below for next steps.'}`;
    const epubcheck=book.epubcheck;
    panel.innerHTML=`<div class="detail-kicker">BOOK INSPECTOR<button class="icon-button" data-detail-action="close" aria-label="Close book inspector">${icon('close')}</button></div><div class="detail-hero">${cover(book,true)}<div><h2>${esc(book.title||book.filename||'Untitled book')}</h2><p class="detail-author">${esc(book.author||'Unknown author')}</p><div class="book-tags"><span class="neutral-badge">EPUB ${esc(book.epubVersion||'?')}</span>${book.language?`<span class="neutral-badge">${esc(book.language.toLocaleUpperCase())}</span>`:''}<span class="neutral-badge">${esc(bytes(book.fileBytes))}</span></div></div></div><div class="health-summary ${esc(book.status)}">${badge(book)}<p>${esc(healthCopy)}</p></div>${epubcheck?`<div class="epubcheck-result"><strong>EPUBCheck · ${esc(epubcheck.status||'Not run')}${epubcheck.version?` · ${esc(epubcheck.version)}`:''}</strong>${esc(epubcheck.detail||'Formal standards validation.')}</div>`:''}<div class="detail-stats"><div class="detail-stat"><strong>${book.status==='unreadable'?'—':number(stats.words)}</strong><span>Words in reading order</span></div><div class="detail-stat"><strong>${book.status==='unreadable'?'—':number(stats.estimatedPages)}</strong><span>Estimated pages</span></div><div class="detail-stat"><strong>${number(stats.chapters)}</strong><span>Reading-order documents</span></div><div class="detail-stat"><strong>${readingTime(stats.readingMinutes)}</strong><span>Estimated reading time</span></div></div><p class="estimate-note">${number(state.settings.wordsPerPage)} words per estimated page · ${stats.sourcePages?`${number(stats.sourcePages)} source page markers`:'No source page markers supplied'}</p><div class="detail-actions"><button class="button ${repairable?'primary':'secondary'}" data-detail-action="repair" ${busy||!issues.length?'disabled':''} title="${issues.length?'Review proposed repairs and reader cleanup':'No repairs needed: no findings were detected'}">${state.repairing?'<span class="spinner"></span>':icon('wrench')}${state.repairing?'Repairing…':'Review repairs'}</button><button class="button secondary" data-detail-action="rescan" ${busy?'disabled':''}>${icon('refresh')}Rescan</button><button class="icon-button" data-detail-action="reveal" title="Show original file" aria-label="Show original file">${icon('folder')}</button></div>${issues.length?'<p class="detail-note">Review changes before saving a separate copy. Your original is preserved.</p>':''}<div class="issue-heading"><h3>Findings</h3><div class="issue-counts"><span class="errors">${number(book.errors)} errors</span><span class="warnings">${number(book.warnings)} warnings</span></div></div><div class="issue-list">${issues.length?issues.slice().sort((a,b)=>({error:0,warning:1,info:2}[a.severity]??3)-({error:0,warning:1,info:2}[b.severity]??3)).map(issue=>`<article class="issue ${esc(issue.severity)}"><div class="issue-top"><span class="issue-icon">${icon(issue.severity==='error'?'error':issue.severity==='warning'?'alert':'info')}</span><h4>${esc(issue.title||issue.code)}</h4></div>${issue.detail?`<p>${esc(issue.detail)}</p>`:''}${issue.file?`<span class="issue-file">${esc(issue.file)}${issue.line?`:${number(issue.line)}`:''}</span>`:''}${repairExplanation(issue)?`<p class="repair-explanation">${esc(repairExplanation(issue))}</p>`:''}<div class="issue-meta"><span>${esc(issue.code||issue.severity)}</span>${issue.repairable?`<span class="repairable">${icon('wrench')}${issue.optionalRepair?'Optional reader cleanup':'Repair available'}</span>`:''}</div></article>`).join(''):`<div class="no-issues"><span>${icon('check-circle')}</span>No findings from the checks that ran.</div>`}</div><details class="metadata-disclosure"><summary>File & publication details</summary><dl class="metadata-grid"><dt>File</dt><dd>${esc(book.filename)}</dd><dt>Location</dt><dd>${esc(book.path)}</dd><dt>Identifier</dt><dd>${esc(book.identifier||'Not supplied')}</dd><dt>Images / fonts</dt><dd>${number(stats.images)} / ${number(stats.fonts)}</dd><dt>Archive entries</dt><dd>${number(stats.files)}</dd><dt>Expanded size</dt><dd>${esc(bytes(stats.uncompressedBytes))}</dd><dt>Largest chapter</dt><dd>${esc(bytes(stats.largestChapterBytes))}</dd><dt>Last checked</dt><dd>${formatDate(book.scannedAt)}</dd><dt>Scan time</dt><dd>${number(book.durationMs)} ms</dd></dl></details>`;
  }
  function repairExplanation(issue){
    if(issue.repairReason)return issue.repairReason;
    if(issue.code==='CONTENT_SCRIPT')return 'Optional reader cleanup can remove scripts from a separate copy. Interactive content may stop working.';
    if(issue.code==='LINK_MISSING'||issue.code==='LINK_FRAGMENT')return 'Review repairs to look for one proven destination. Ambiguous or absent targets are left for manual correction.';
    if(issue.code==='CONTENT_DUPLICATE_ID')return 'Review repairs to recheck source identifiers. Renaming is offered only when references and styling can be preserved.';
    if(issue.code==='XML_DTD_CHECK_INCOMPLETE')return 'Run EPUBCheck for this assessment. SpineGuard preserves DTD-dependent content.';
    if(issue.code?.startsWith('METADATA_'))return 'Missing publication metadata needs a confirmed value. SpineGuard does not guess it.';
    if(issue.code==='FONT_OBFUSCATION')return 'Recognized font obfuscation is preserved; no cleanup is needed.';
    return '';
  }
  function formatDate(value){const date=new Date(value);return Number.isNaN(date.getTime())?'—':esc(date.toLocaleString([], {dateStyle:'medium',timeStyle:'short'}));}
  function readingTime(value){const minutes=Number(value||0);if(minutes<60)return `${number(minutes)} min`;return `${Math.floor(minutes/60)}h ${minutes%60}m`;}

  async function scan(paths){
    paths=(Array.isArray(paths)?paths:[]).filter(path=>typeof path==='string'&&path.trim());if(!paths.length)return;
    if(state.running||state.repairing||state.reviewPath||state.previewWorker){state.queued.push(...paths);toast('Added to the queue. These books will be checked after the current task.');return;}
    state.cancelling=false;state.progress={index:0,total:0};setBusy(true);$('progress-path').textContent=paths.length===1?paths[0]:`${number(paths.length)} selected paths`;
    try{const reports=await api.ScanPaths(paths);if(Array.isArray(reports))reports.forEach(upsert);render();if(!state.cancelling)toast(reports?.length?`Checked ${number(reports.length)} ${reports.length===1?'book':'books'}.`:'No EPUB files were found in the selected location.');else toast('Scan stopped. Completed results are in your library.');}
    catch(error){toast(errorText(error),'error',10000);}
    finally{state.cancelling=false;setBusy(false);render();drainQueue();}
  }
  function drainQueue(){if(!state.running&&!state.repairing&&!state.reviewPath&&!state.previewWorker&&state.queued.length){const queued=[...new Set(state.queued)];state.queued=[];void scan(queued);}}
  async function chooseFiles(){try{const paths=await api.SelectFiles();if(paths?.length)await scan(paths);}catch(error){toast(errorText(error),'error');}}
  async function chooseFolder(){try{const path=await api.SelectFolder();if(path)await scan([path]);}catch(error){toast(errorText(error),'error');}}
  function repair(){
    const book=state.reports.find(item=>item.path===state.selected);if(!book||state.running||state.repairing)return;
    state.reviewPath=book.path;state.repairPlan=null;state.reviewError='';
    state.reviewOptions={fixLinks:true,fixDuplicateIds:true,removeScripts:false};
    for(const [key,value]of Object.entries(state.reviewOptions))$('repair-options').elements.namedItem(key).checked=value;
    $('repair-review-title').textContent=book.title||book.filename||'Your book';
    $('repair-review-dialog').showModal();setBusy(false);requestRepairPreview();
  }
  function requestRepairPreview(){
    if(!state.reviewPath||state.repairing)return;
    state.reviewGeneration++;state.repairPlan=null;state.reviewError='';state.previewing=true;
    renderRepairReview();if(!state.previewWorker)void runRepairPreview();
  }
  async function runRepairPreview(){
    state.previewWorker=true;
    try{
      while(state.reviewPath){
        const generation=state.reviewGeneration,path=state.reviewPath,options={...state.reviewOptions};
        try{
          const plan=await api.PreviewRepair(path,options);
          if(!state.reviewPath)return;
          if(generation!==state.reviewGeneration||path!==state.reviewPath)continue;
          if(!plan||!Array.isArray(plan.actions))throw new Error('The repair review was incomplete. Refresh it before saving.');
          state.repairPlan=plan;state.reviewError='';
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
  function renderRepairReview(){
    const plan=state.repairPlan,actions=Array.isArray(plan?.actions)?plan.actions:[],unresolved=Array.isArray(plan?.unresolved)?plan.unresolved:[];
    const blocked=plan?.blockedReason||'',pending=state.previewing;
    $('repair-review-dialog').setAttribute('aria-busy',String(pending||state.repairing));
    $('save-repair-button').disabled=pending||state.repairing||!!state.reviewError||!!blocked||!actions.length||!plan?.sourceSha256;
    $('save-repair-button').innerHTML=state.repairing?'<span class="spinner"></span>Saving copy…':`${icon('wrench')}Save repaired copy`;
    $('refresh-repair-preview').disabled=pending||state.repairing;
    $('repair-options').disabled=state.repairing;
    $('repair-review-dialog').querySelectorAll('[data-close]').forEach(button=>{button.disabled=state.repairing;});
    if(pending){$('repair-plan-summary').innerHTML='<div class="busy-indicator" role="status"><span class="spinner"></span>Checking the current book and preparing changes…</div>';$('repair-plan-actions').innerHTML='';$('repair-plan-unresolved').innerHTML='';return;}
    if(state.reviewError){$('repair-plan-summary').innerHTML=`<div class="repair-review-message error" role="alert"><strong>Review needs to be refreshed</strong><p>${esc(state.reviewError)}</p><p>Refresh the review to check the current file before trying again.</p></div>`;$('repair-plan-actions').innerHTML='';$('repair-plan-unresolved').innerHTML='';return;}
    $('repair-plan-summary').innerHTML=blocked
      ?`<div class="repair-review-message error"><strong>This copy cannot be repaired automatically</strong><p>${esc(blocked)}</p></div>`
      :actions.length
        ?`<div class="repair-review-message"><strong>${number(actions.length)} proposed ${actions.length===1?'change':'changes'}</strong><p>Only the changes below will be applied. The new copy will be checked again; unresolved findings remain visible.</p></div>`
        :'<div class="repair-review-message"><strong>No selected automatic changes are available</strong><p>Review the findings below. You can select optional reader cleanup when scripts are present, or correct unresolved content with an EPUB editor.</p></div>';
    if(plan?.report?.epubcheck)$('repair-plan-summary').innerHTML+='<p class="review-section-note">Preview predictions cover built-in checks. EPUBCheck is run again on the saved copy.</p>';
    $('repair-plan-actions').innerHTML=actions.length?`<h3>Proposed changes</h3><ul class="repair-plan-list">${actions.map(action=>`<li><div class="repair-plan-heading"><strong>${esc(action.description||action.kind||'Repair')}</strong>${action.requiresOptIn?'<span class="neutral-badge cleanup-badge">READER CLEANUP</span>':''}</div>${action.file?`<span class="issue-file">${esc(action.file)}</span>`:''}${Number(action.count)>1?`<span class="repair-count">${number(action.count)} changes in this action</span>`:''}</li>`).join('')}</ul>`:'';
    $('repair-plan-unresolved').innerHTML=unresolved.length?`<h3>Findings left for review <span class="neutral-badge">${number(unresolved.length)}</span></h3><p class="review-section-note">These findings do not have a selected automatic fix. Compatibility advisories and incomplete checks may need a different action.</p><div class="review-unresolved-list">${unresolved.map(issue=>`<article class="review-unresolved"><strong>${esc(issue.title||issue.code||'Finding')}</strong>${issue.file?`<span class="issue-file">${esc(issue.file)}</span>`:''}<p>${esc(repairExplanation(issue)||issue.detail||'No proven automatic correction is available. Review this finding in an EPUB editor or obtain a corrected copy.')}</p></article>`).join('')}</div>`:!blocked&&actions.length?'<p class="review-section-note">No unresolved findings are predicted by the built-in checks for the selected changes. The saved copy will still be rescanned.</p>':'';
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
        <ul class="repair-changes">${(result.changes||[]).map(change=>`<li><span>${icon('check')}</span>${esc(change)}</li>`).join('')||'<li>No automatic changes were necessary.</li>'}</ul>
        <h3 class="result-section-title">Saved copy</h3>
        <div class="repair-path">${esc(result.outputPath)}</div>
        ${remaining?'<p class="detail-note repair-followup">Review the repaired copy’s findings in the library before sideloading.</p>':''}`;
      $('result-dialog').showModal();
    }
    catch(error){state.reviewError=errorText(error);state.repairPlan=null;}
    finally{state.repairing=false;setBusy(false);render();if(state.reviewPath)renderRepairReview();drainQueue();}
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
    $('add-button').addEventListener('click',chooseFiles);$('empty-add-button').addEventListener('click',chooseFiles);$('folder-button').addEventListener('click',chooseFolder);
    $('rescan-library-button').addEventListener('click',()=>{if(!state.running&&!state.repairing&&!state.reviewPath)void scan(state.reports.map(book=>book.path));});
    $('settings-nav').addEventListener('click',openSettings);$('help-button').addEventListener('click',()=>$('help-dialog').showModal());$('library-nav').addEventListener('click',()=>{state.filter='all';state.query='';$('search-input').value='';render();});
    $('settings-form').addEventListener('submit',saveSettings);
    $('browse-epubcheck').addEventListener('click',async()=>{try{const path=await api.SelectEPUBCheck();if(path)$('settings-form').elements.epubcheckJar.value=path;}catch(error){toast(errorText(error),'error');}});
    document.querySelectorAll('[data-close]').forEach(button=>button.addEventListener('click',()=>$(button.dataset.close).close()));
    document.querySelectorAll('[data-filter]').forEach(button=>button.addEventListener('click',()=>{state.filter=button.dataset.filter;render();}));
    $('search-input').addEventListener('input',event=>{state.query=event.target.value.trim().toLocaleLowerCase();render();});
    $('book-rows').addEventListener('click',event=>{const row=event.target.closest('[data-book-index]');if(!row)return;state.selected=state.reports[Number(row.dataset.bookIndex)]?.path;render();});
    $('book-detail').addEventListener('click',event=>{const button=event.target.closest('[data-detail-action]');if(!button)return;const book=state.reports.find(item=>item.path===state.selected);switch(button.dataset.detailAction){case'close':state.selected=null;render();break;case'rescan':if(book)void scan([book.path]);break;case'repair':void repair();break;case'reveal':if(book)void reveal(book.path);break;}});
    $('cancel-button').addEventListener('click',async()=>{state.cancelling=true;state.queued=[];$('cancel-button').disabled=true;$('cancel-button').textContent='Cancelling…';try{await api.CancelScan();}catch(error){toast(errorText(error),'error');}});
    $('export-button').addEventListener('click',event=>{event.stopPropagation();const open=$('export-menu').classList.toggle('hidden')===false;$('export-button').setAttribute('aria-expanded',String(open));});
    document.querySelectorAll('[data-export]').forEach(button=>button.addEventListener('click',()=>exportReports(button.dataset.export)));
    document.addEventListener('click',event=>{if(!event.target.closest('.export-container')){$('export-menu').classList.add('hidden');$('export-button').setAttribute('aria-expanded','false');}});
    $('clear-button').addEventListener('click',()=>$('confirm-dialog').showModal());$('confirm-clear-button').addEventListener('click',async()=>{try{await api.ClearReports();state.reports=[];state.selected=null;state.filter='all';state.query='';$('search-input').value='';render();$('confirm-dialog').close();toast('Reports cleared. Your books are untouched.');}catch(error){toast(errorText(error),'error');}});
    $('reveal-repair-button').addEventListener('click',()=>reveal(state.repairedPath));
    $('repair-options').addEventListener('change',()=>{for(const key of ['fixLinks','fixDuplicateIds','removeScripts'])state.reviewOptions[key]=$('repair-options').elements.namedItem(key).checked;requestRepairPreview();});
    $('refresh-repair-preview').addEventListener('click',requestRepairPreview);
    $('save-repair-button').addEventListener('click',saveReviewedRepair);
    $('repair-review-dialog').addEventListener('cancel',event=>{if(state.repairing)event.preventDefault();});
    $('repair-review-dialog').addEventListener('close',()=>{state.reviewPath='';state.repairPlan=null;state.reviewError='';state.previewing=false;state.reviewGeneration++;setBusy(false);render();drainQueue();});
    document.addEventListener('keydown',event=>{const editing=/INPUT|TEXTAREA|SELECT/.test(document.activeElement?.tagName);if((event.ctrlKey||event.metaKey)&&event.key.toLocaleLowerCase()==='o'){event.preventDefault();if(!state.running&&!state.repairing&&!document.querySelector('dialog[open]'))void chooseFiles();}else if(event.key==='/'&&!editing&&!document.querySelector('dialog[open]')){event.preventDefault();$('search-input').focus();}else if(event.key==='Escape'){$('export-menu').classList.add('hidden');$('drop-overlay').classList.add('hidden');}});
    let dragDepth=0;document.addEventListener('dragenter',event=>{if(event.dataTransfer?.types?.includes('Files')){event.preventDefault();dragDepth++;$('drop-overlay').classList.remove('hidden');}});document.addEventListener('dragleave',()=>{dragDepth=Math.max(0,dragDepth-1);if(!dragDepth)$('drop-overlay').classList.add('hidden');});document.addEventListener('dragover',event=>{event.preventDefault();});document.addEventListener('drop',event=>{event.preventDefault();dragDepth=0;$('drop-overlay').classList.add('hidden');});window.addEventListener('blur',()=>{dragDepth=0;$('drop-overlay').classList.add('hidden');});
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change',()=>{if(state.settings.theme==='system')setTheme('system');});
  }
  function wireRuntime(){
    const runtime=window.runtime;
    if(!runtime)return;
    runtime.EventsOn?.('scan:progress',progress=>{if(progress?.report)upsert(progress.report);state.progress=progress||{};if(progress?.total){$('progress-title').textContent=`Checking book ${number(Math.min(progress.index||1,progress.total))} of ${number(progress.total)}`;$('progress-fill').style.width=`${Math.min(100,Math.max(0,((progress.index||0)/progress.total)*100))}%`;}$('progress-path').textContent=progress?.path||'';render();});
    runtime.EventsOn?.('scan:state',event=>{if(event?.running&&!state.running)setBusy(true);});
    runtime.OnFileDrop?.((_x,_y,paths)=>{ $('drop-overlay').classList.add('hidden');void scan(paths);},true);
    runtime.EventsOn?.('app:open',paths=>{if(Array.isArray(paths))void scan(paths);});
    runtime.EventsOn?.('app:warning',message=>{if(message)toast(typeof message==='string'?message:(message.detail||message.message||'The library could not be saved.'),'error',14000);});
  }
  async function init(){
    hydrateIcons();wireEvents();wireRuntime();render();
    $('settings-form').elements.wordsPerPage.min='100';
    $('settings-form').elements.readingWPM.min='80';
    if(/Mac/i.test(navigator.platform))$('open-shortcut').textContent='⌘ O';
    if(window.spineguardPreview){const banner=document.createElement('div');banner.className='preview-banner';banner.textContent='Preview data — desktop interaction adapter';document.querySelector('.main-content').prepend(banner);}
    try{const result=await api.GetState();state.settings={...defaults,...(result?.settings||{})};state.version=result?.version||'1.1.0';state.reports=Array.isArray(result?.reports)?result.reports:[];state.selected=state.reports[0]?.path||null;setTheme(state.settings.theme);$('app-version').textContent=`v${state.version}`;render();if(result?.warning)toast(result.warning,'error',14000);}
    catch(error){toast(errorText(error),'error',15000);}
  }
  void init();
})();
