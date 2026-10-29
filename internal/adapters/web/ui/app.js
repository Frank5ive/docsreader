const $ = (id) => document.getElementById(id);
const api = async (url, opt) => {
  const r = await fetch(url, opt);
  if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
  return r.status === 204 ? null : r.json();
};
const send = (url, method, body) =>
  api(url, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

/* ---------- settings ---------- */
const systemDark = matchMedia && matchMedia('(prefers-color-scheme: dark)').matches;
const defaults = { theme: systemDark ? 'dark' : 'light', font: 'serif', size: 19, lh: 17, width: 68, focus: false, pdfZoom: 1 };
let prefs = { ...defaults, ...safeParse(localStorage.getItem('prefs')) };
function safeParse(s) { try { return JSON.parse(s) || {}; } catch { return {}; } }
// Preferences are stored only once the user changes something, so the default theme keeps following the OS.
function applyPrefs(persist = false) {
  document.body.dataset.theme = prefs.theme;
  const p = $('page');
  p.dataset.font = prefs.font;
  p.style.setProperty('--fs', prefs.size + 'px');
  p.style.setProperty('--lh', prefs.lh / 10);
  p.style.setProperty('--w', prefs.width + 'ch');
  p.classList.toggle('focus', prefs.focus);
  for (const k of ['theme', 'font', 'size', 'lh', 'width']) $(k).value = prefs[k];
  $('focus').checked = prefs.focus;
  if (persist) localStorage.setItem('prefs', JSON.stringify(prefs));
}
for (const k of ['theme', 'font', 'size', 'lh', 'width']) {
  $(k).oninput = () => { prefs[k] = $(k).type === 'range' ? +$(k).value : $(k).value; applyPrefs(true); restore(); };
}
$('focus').onchange = () => { prefs.focus = $('focus').checked; applyPrefs(true); trackFocus(); };
$('histBtn').onclick = async () => {
  const h = $('history');
  if (!togglePanel('history')) return;
  const rows = await api(`/api/books/${cur.id}/history`);
  h.replaceChildren(Object.assign(document.createElement('h3'), { textContent: 'Reading history' }));
  if (!rows.length) h.append('No sessions yet.');
  for (const s of rows) {
    const d = document.createElement('div');
    d.textContent = `${new Date(s.start).toLocaleString()} — ${Math.max(1, Math.round(s.seconds / 60))} min, ${s.words} words`;
    h.append(d);
  }
};
const panels = ['toc', 'settings', 'history', 'notes', 'find'];
// Watch only the panels (never the scrim itself: writing its attribute would re-trigger the observer forever).
const syncScrim = () => {
  const want = !panels.some((p) => !$(p).hidden);
  if ($('scrim').hidden !== want) $('scrim').hidden = want;
  if (!want) document.body.classList.remove('chrome-hidden');
};
const scrimObserver = new MutationObserver(syncScrim);
for (const p of panels) scrimObserver.observe($(p), { attributes: true, attributeFilter: ['hidden'] });
$('scrim').onclick = () => { for (const p of panels) $(p).hidden = true; };
function togglePanel(id) { const open = $(id).hidden; for (const p of panels) $(p).hidden = true; $(id).hidden = !open; return open; }
$('setBtn').onclick = ()=>togglePanel('settings');

$('tocBtn').onclick = () => togglePanel('toc');

/* ---------- library ---------- */
let cur = null; // current reading state
$('home').onclick = showLibrary;
async function uploadFiles(files) {
  const books = [...files].filter((f) => /\.(epub|pdf)$/i.test(f.name));
  if (!books.length) { toast('Only EPUB and PDF files can be added'); return; }
  for (const f of books) {
    const fd = new FormData(); fd.append('file', f);
    toast(`Adding ${f.name}…`);
    try { await api('/api/books', { method: 'POST', body: fd }); } catch (err) { toast(`${f.name}: ${err.message}`); }
  }
  loadLibrary();
}
$('upload').onchange = async (e) => { await uploadFiles(e.target.files); e.target.value = ''; };
// Drop files anywhere on the library page.
let dragDepth = 0;
const hasFiles = (e) => e.dataTransfer && [...e.dataTransfer.types].includes('Files');
addEventListener('dragenter', (e) => { if (hasFiles(e) && !$('library').hidden) { dragDepth++; $('dropveil').hidden = false; } });
addEventListener('dragleave', (e) => { if (hasFiles(e) && --dragDepth <= 0) { dragDepth = 0; $('dropveil').hidden = true; } });
addEventListener('dragover', (e) => { if (hasFiles(e)) e.preventDefault(); });
addEventListener('drop', (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault(); dragDepth = 0; $('dropveil').hidden = true;
  if (!$('library').hidden) uploadFiles(e.dataTransfer.files);
});

const pct = (b) => (b.progress ? Math.round(b.progress.percent * 100) : 0);
function ago(iso) {
  const s = (Date.now() - new Date(iso)) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return Math.floor(s / 60) + ' min ago';
  if (s < 86400) return Math.floor(s / 3600) + ' h ago';
  return Math.floor(s / 86400) + ' d ago';
}
function hueOf(str) { let h = 0; for (const c of str) h = (h * 31 + c.codePointAt(0)) >>> 0; return h % 360; }
function coverEl(b) {
  const d = document.createElement('div'); d.className = 'cover';
  if (b.HasCover) { const i = new Image(); i.src = `/api/books/${b.ID}/cover`; i.alt = ''; d.append(i); }
  else {
    d.classList.add('gen'); d.style.setProperty('--h', hueOf(b.Title + b.Author));
    const t = document.createElement('div'); t.className = 'ct'; t.textContent = b.Title;
    const a = document.createElement('div'); a.className = 'ca'; a.textContent = b.Author || '';
    d.append(t, a);
  }
  const f = document.createElement('span'); f.className = 'fmt'; f.textContent = b.Format.toUpperCase();
  d.append(f);
  return d;
}
function fmtDur(sec) {
  const m = Math.round(sec / 60);
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`;
}
// Reading speed from real data once there is enough of it, otherwise a typical 230 wpm.
const speedOf = (st) => (st.totalSeconds >= 300 && st.wordsPerMin >= 50 && st.wordsPerMin <= 700 ? st.wordsPerMin : 230);
function etaText(sec) { return sec < 60 ? '<1 min' : '~' + fmtDur(sec); }
function etaOf(b, st) {
  if (!b.WordCount || !b.progress || b.progress.percent >= 0.999) return '';
  const sec = (b.WordCount * (1 - b.progress.percent) / speedOf(st)) * 60;
  return sec < 60 ? 'under 1 min left' : '~' + fmtDur(sec) + ' left';
}
function statCard(label, value) {
  const d = document.createElement('div'); d.className = 'stat';
  const v = document.createElement('b'); v.textContent = value;
  const l = document.createElement('small'); l.textContent = label;
  d.append(v, l); return d;
}
function renderStats(st, times, books) {
  const box = $('stats'); box.replaceChildren();
  if (!st.sessions) { box.textContent = 'Your reading stats will appear here once you have read for a while.'; return; }
  const cards = document.createElement('div'); cards.className = 'cards';
  cards.append(
    statCard('Total reading time', fmtDur(st.totalSeconds)),
    statCard('Reading speed', Math.round(speedOf(st)) + ' wpm'),
    statCard('Current streak', st.streakDays + (st.streakDays === 1 ? ' day' : ' days')),
    statCard('Longest streak', st.longestStreak + (st.longestStreak === 1 ? ' day' : ' days')),
    statCard('Sessions', String(st.sessions)));
  // Last 30 days, oldest first; bar height is relative to the busiest day.
  const days = [];
  for (let i = 29; i >= 0; i--) { const d = new Date(); d.setDate(d.getDate() - i); days.push(d); }
  const key = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  const max = Math.max(60, ...days.map((d) => st.perDay[key(d)] || 0));
  const chart = document.createElement('div'); chart.className = 'chart'; chart.id = 'chart';
  for (const d of days) {
    const sec = st.perDay[key(d)] || 0;
    const bar = document.createElement('i');
    bar.style.height = Math.max(sec ? 4 : 1, (sec / max) * 100) + '%';
    bar.title = `${d.toLocaleDateString()}: ${sec ? fmtDur(sec) : 'no reading'}`;
    bar.dataset.sec = sec;
    if (sec) bar.className = 'on';
    chart.append(bar);
  }
  const h = document.createElement('h3'); h.textContent = 'Last 30 days';
  const per = document.createElement('div'); per.className = 'perbook';
  const ph = document.createElement('h3'); ph.textContent = 'Time per book';
  const rows = books.filter((b) => times[b.ID]).sort((a, b) => times[b.ID].seconds - times[a.ID].seconds);
  for (const b of rows) {
    const r = document.createElement('div');
    const n = document.createElement('span'); n.textContent = b.Title;
    const t = document.createElement('span'); t.textContent = fmtDur(times[b.ID].seconds);
    r.append(n, t); per.append(r);
  }
  box.append(cards, h, chart, ph, per);
}
async function loadLibrary() {
  const [books, st, times] = await Promise.all([api('/api/books'), api('/api/stats'), api('/api/stats/books')]);
  const shelf = $('shelf'); shelf.replaceChildren();
  for (const b of books) {
    const c = document.createElement('div'); c.className = 'card';
    c.dataset.q = (b.Title + ' ' + b.Author).toLowerCase();
    c.dataset.tags = JSON.stringify((b.Tags || []).map((t) => t.toLowerCase()));
    const meta = document.createElement('div'); meta.className = 'meta';
    const t = document.createElement('b'); t.textContent = b.Title; t.title = b.Title;
    const a = document.createElement('span');
    a.textContent = b.lastRead ? `${ago(b.lastRead)}${b.Author ? ' · ' + b.Author : ''}` : (b.Author || 'Not started');
    meta.append(t, a);
    const bar = document.createElement('div'); bar.className = 'bar';
    const fill = document.createElement('i'); fill.style.width = pct(b) + '%'; bar.append(fill);
    const cv = coverEl(b); cv.append(bar);
    if (pct(b) > 0) { const bd = document.createElement('span'); bd.className = 'badge'; bd.textContent = pct(b) + '%'; cv.append(bd); }
    const del = document.createElement('button'); del.className = 'del'; del.textContent = '×';
    del.onclick = async (e) => { e.stopPropagation(); if (confirm('Remove "' + b.Title + '"?')) { await api('/api/books/' + b.ID, { method: 'DELETE' }); loadLibrary(); } };
    c.title = etaOf(b, st);
    const tagRow = document.createElement('div'); tagRow.className = 'tags';
    for (const t of b.Tags || []) { const s = document.createElement('span'); s.textContent = t; tagRow.append(s); }
    const tagBtn = document.createElement('button'); tagBtn.className = 'tagbtn'; tagBtn.textContent = '#'; tagBtn.title = 'Edit tags';
    tagBtn.onclick = (e) => { e.stopPropagation(); editTags(b, c, tagRow); };
    c.append(cv, meta, tagRow, del, tagBtn);
    c.onclick = () => openBook(b.ID);
    shelf.append(c);
  }
  $('empty').hidden = books.length > 0;
  renderTagBar(books);
  filterShelf();
  const last = books.find((b) => b.progress && b.progress.percent < 0.999);
  const cont = $('continue'); cont.replaceChildren();
  if (last) {
    const h = document.createElement('div'); h.className = 'hero';
    const info = document.createElement('div'); info.className = 'info';
    const label = Object.assign(document.createElement('small'), { textContent: 'Continue reading' });
    const title = Object.assign(document.createElement('h3'), { textContent: last.Title });
    const sub = Object.assign(document.createElement('div'), { className: 'sub',
      textContent: [last.Author, `${pct(last)}% read`, etaOf(last, st), ago(last.lastRead)].filter(Boolean).join(' · ') });
    const prog = document.createElement('div'); prog.className = 'progress';
    const pf = document.createElement('i'); pf.style.width = pct(last) + '%'; prog.append(pf);
    const go = Object.assign(document.createElement('button'), { className: 'primary go', textContent: 'Resume' });
    info.append(label, title, sub, prog);
    h.append(coverEl(last), info, go); h.onclick = () => openBook(last.ID); cont.append(h);
  }
  renderStats(st, times, books);
}
async function showLibrary() {
  clearTimeout(saveT); await flush(); stopTimer(); cur = null;
  $('library').hidden = false; $('reader').hidden = true; for (const p of panels) $(p).hidden = true;
  $('selbar').hidden = $('hlbar').hidden = true;
  document.body.classList.remove('reading', 'chrome-hidden'); $('chap').textContent = $('left').textContent = '';
  for (const b of ['tocBtn', 'histBtn', 'markBtn', 'notesBtn', 'findBtn', 'zoomOut', 'zoomLbl', 'zoomIn']) $(b).hidden = true; $('title').textContent = 'docsreader';
  loadLibrary();
}

/* ---------- reader ---------- */
async function openBook(id, target) {
  const info = await api('/api/books/' + id);
  cur = { id, book: info.book, toc: info.toc, sectionWords: info.sectionWords, anchor: target || (info.hasProgress ? info.progress : null), section: 0, pdf: null, anns: [] };
  $('library').hidden = true; $('reader').hidden = false; document.body.classList.add('reading');
  // Only refreshes the label: calling onScroll() here could record a position before the saved one is restored.
  api('/api/stats').then((st) => { if (cur) { cur.wpm = speedOf(st); if (cur.anchor) updateLeft(cur.anchor.percent); } }).catch(() => {});
  $('findInput').value = ''; $('findHits').replaceChildren(); // never carry one book's search into another
  $('title').textContent = info.book.Title;
  const pdfBook = info.book.Format === 'pdf';
  $('page').hidden = pdfBook; $('pdfWrap').hidden = !pdfBook; $('tocBtn').hidden = pdfBook; $('histBtn').hidden = false;
  for (const z of ['zoomOut', 'zoomLbl', 'zoomIn']) $(z).hidden = !pdfBook;
  $('markBtn').hidden = $('notesBtn').hidden = false; $('findBtn').hidden = false;
  await loadAnns();
  applyPrefs();
  if (pdfBook) await openPdf(); else { buildToc(); await loadSection(cur.anchor ? cur.anchor.section : 0, cur.anchor); }
  startTimer();
  if (!localStorage.getItem('hintSeen')) { toast('Select text to highlight or add a note · press B to bookmark · Ctrl+F to search'); localStorage.setItem('hintSeen', '1'); }
}
function buildToc() {
  const t = $('toc'); t.replaceChildren();
  for (const c of cur.toc) {
    const a = document.createElement('a'); a.textContent = c.Title || 'Untitled'; a.style.paddingLeft = c.Depth * 14 + 'px';
    a.onclick = () => { t.hidden = true; loadSection(c.Section, null); };
    t.append(a);
  }
}
async function loadSection(n, anchor) {
  n = Math.max(0, Math.min(cur.book.Sections - 1, n));
  const { html } = await api(`/api/books/${cur.id}/section/${n}`);
  cur.section = n;
  $('page').innerHTML = html;
  fixLinks();
  renderHighlights();
  window.scrollTo(0, 0);
  if (anchor && anchor.section === n) restoreAnchor(anchor); else onScroll();
  cur.anchor = cur.anchor || null;
}
function fixLinks() {
  for (const a of $('page').querySelectorAll('a[href]')) {
    const h = a.getAttribute('href');
    if (/^https?:/i.test(h)) { a.target = '_blank'; a.rel = 'noopener noreferrer'; }
    else a.removeAttribute('href');
  }
}
const blocks = () => $('page').querySelectorAll('[data-b]');
function restoreAnchor(a) {
  const el = $('page').querySelector(`[data-b="${a.block}"]`);
  if (el) {
    const r = rangeAt(el, a.offset);
    const top = (r ? r.getBoundingClientRect() : el.getBoundingClientRect()).top;
    window.scrollTo(0, top + scrollY - 90);
  }
  const keep = a.percent > 0 ? cur.anchor : null; // keep a saved position exact; a jump target (no percent) is re-derived
  onScroll();
  if (keep) cur.anchor = { ...keep };
}
function restore() { if (cur && cur.book.Format === 'epub' && cur.anchor && cur.anchor.section === cur.section) restoreAnchor(cur.anchor); }

// rangeAt returns a collapsed Range at the given character offset inside el.
function rangeAt(el, offset) {
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  let n, left = offset, last = null;
  while ((n = w.nextNode())) {
    last = n;
    if (left <= n.length) { const r = document.createRange(); r.setStart(n, left); r.collapse(true); return r; }
    left -= n.length;
  }
  if (!last) return null;
  const r = document.createRange(); r.setStart(last, last.length); r.collapse(true); return r;
}
// offsetIn returns the character offset of the first visible character of el.
function offsetIn(el) {
  const top = 90;
  const caret = document.caretRangeFromPoint
    ? document.caretRangeFromPoint(innerWidth / 2, top + 4)
    : (() => { const p = document.caretPositionFromPoint(innerWidth / 2, top + 4); if (!p) return null; const r = document.createRange(); r.setStart(p.offsetNode, p.offset); return r; })();
  if (!caret || !el.contains(caret.startContainer)) return 0;
  const pre = document.createRange(); pre.selectNodeContents(el); pre.setEnd(caret.startContainer, caret.startOffset);
  return pre.toString().length;
}
// Percent weights each section by its word count so it tracks real progress.
function percentOf(section, frac) {
  const w = cur.sectionWords;
  if (!w || !w.length) return Math.min(1, (section + frac) / cur.book.Sections);
  const total = w.reduce((a, b) => a + b, 0) || 1;
  let before = 0; for (let i = 0; i < section; i++) before += w[i];
  return Math.min(1, (before + frac * w[section]) / total);
}
function currentAnchor() {
  if (cur.book.Format === 'pdf') return pdfAnchor();
  let block = 0, offset = 0, quote = '';
  for (const el of blocks()) {
    if (el.getBoundingClientRect().bottom > 90) {
      block = +el.dataset.b; offset = offsetIn(el);
      quote = el.textContent.slice(offset, offset + 80).trim(); break;
    }
  }
  const frac = Math.min(1, scrollY / Math.max(1, document.documentElement.scrollHeight - innerHeight));
  return { section: cur.section, block, offset, percent: percentOf(cur.section, frac), quote };
}
function trackFocus() {
  if (!prefs.focus) return;
  let found = false;
  for (const el of blocks()) {
    const on = !found && el.getBoundingClientRect().bottom > innerHeight * 0.35;
    el.classList.toggle('cur', on); if (on) found = true;
  }
}
let saveT = 0, dirty = false;
function updateLeft(percent) {
  const words = cur.book.WordCount;
  $('left').textContent = words && percent < 0.999
    ? ` · ${etaText(words * (1 - percent) / (cur.wpm || 230) * 60)} left` : '';
}
function onScroll() {
  if (!cur || $('reader').hidden || (cur.book.Format === 'pdf' && !(cur.pages && cur.pages.length))) return; // PDF still loading
  const a = currentAnchor(); cur.anchor = a;
  $('pct').textContent = cur.book.Format === 'pdf'
    ? `p.${cur.pdfPage + 1}/${cur.pdf.numPages}` : Math.round(a.percent * 100) + '%';
  $('seek').value = Math.round(a.percent * 1000);
  if (cur.book.Format === 'epub') {
    trackFocus();
    // Current chapter: the last contents entry at or before this section.
    const ch = (cur.toc || []).filter((c) => c.Section <= cur.section).pop();
    $('chap').textContent = ch ? ch.Title : '';
  }
  updateLeft(a.percent);
  updateMarkBtn();
  dirty = true; clearTimeout(saveT); saveT = setTimeout(flush, 1000);
  activity();
}
// flush saves the position. keepalive lets the request outlive the page, and
// the returned promise lets callers wait for it (e.g. before reloading the library).
function flush() {
  if (!cur || !dirty || !cur.anchor) return Promise.resolve();
  dirty = false;
  return fetch(`/api/books/${cur.id}/progress`, {
    method: 'PUT', keepalive: true, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(cur.anchor),
  }).catch(() => { dirty = true; });
}
addEventListener('scroll', onScroll, { passive: true });
addEventListener('visibilitychange', () => { if (document.hidden) { flush(); beat(); } });
addEventListener('pagehide', () => { flush(); beat(); });

$('prev').onclick = () => step(-1);
$('next').onclick = () => step(1);
$('seek').onchange = () => seekTo($('seek').value / 1000);
function step(d) {
  if (cur.book.Format === 'pdf') return goPdf(cur.pdfPage + d);
  const page = innerHeight * 0.9;
  if (d > 0 && innerHeight + scrollY >= document.documentElement.scrollHeight - 4) {
    if (cur.section < cur.book.Sections - 1) loadSection(cur.section + 1, null);
  } else if (d < 0 && scrollY <= 0) {
    if (cur.section > 0) loadSection(cur.section - 1, null).then(() => scrollTo(0, document.documentElement.scrollHeight));
  } else scrollBy({ top: d * page, behavior: 'smooth' });
}
function seekTo(p) {
  if (cur.book.Format === 'pdf') return goPdf(Math.round(p * (cur.book.Sections - 1)));
  const n = Math.min(cur.book.Sections - 1, Math.floor(p * cur.book.Sections));
  loadSection(n, null);
}
addEventListener('keydown', (e) => {
  if ($('reader').hidden || /INPUT|SELECT|TEXTAREA/.test(e.target.tagName) || e.ctrlKey || e.metaKey || e.altKey) return;
  const pdf = isPdf(); // PDFs scroll continuously, so Space and PageUp/PageDown keep their native behaviour
  if (e.key === 'ArrowRight' || (!pdf && (e.key === 'PageDown' || e.key === ' '))) { e.preventDefault(); step(1); }
  if (e.key === 'ArrowLeft' || (!pdf && e.key === 'PageUp')) { e.preventDefault(); step(-1); }
  if (e.key === 'Escape') { if (panels.some((p) => !$(p).hidden)) $('scrim').click(); else showLibrary(); }
  if (e.key === 'b') toggleBookmark();
  if (pdf && (e.key === '+' || e.key === '=')) zoomStep(1);
  if (pdf && e.key === '-') zoomStep(-1);
  if (pdf && e.key === '0') setZoom(1);
});

/* ---------- PDF: continuous scroll and zoom (rendered client-side by vendored pdf.js) ---------- */
const PDF_ZOOMS = [0.5, 0.75, 1, 1.25, 1.5, 2, 3]; // multiples of "fit to width"
const PDF_TOP = 60; // viewport y (px) that counts as the reading line, just below the toolbar
const clampPage = (n) => Math.max(0, Math.min(cur.pdf.numPages - 1, n | 0));

async function openPdf() {
  const book = cur; // the reader may be closed while pdf.js loads
  const pdfjs = await import('/vendor/pdf.min.mjs');
  pdfjs.GlobalWorkerOptions.workerSrc = '/vendor/pdf.worker.min.mjs';
  const pdf = await pdfjs.getDocument(`/api/books/${book.id}/file`).promise;
  const base = (await pdf.getPage(1)).getViewport({ scale: 1 }); // page size used for placeholders until a page is drawn
  if (cur !== book) return;
  cur.pdfjs = pdfjs; cur.pdf = pdf; cur.pdfBase = base; cur.book.Sections = pdf.numPages;
  await layoutPdf({ page: clampPage(cur.anchor ? cur.anchor.section : 0), frac: cur.anchor ? cur.anchor.offset / 1000 : 0 });
}
function pdfScale() {
  const fit = (Math.min(innerWidth, 900) - 24) / cur.pdfBase.width;
  return Math.max(0.2, Math.min(5, fit * prefs.pdfZoom));
}
// layoutPdf lays out one placeholder per page and draws only the pages near the
// viewport, so a thousand-page book costs no more than a short one.
// Nothing is awaited between creating the placeholders and scrolling to `at`, so
// no scroll event can record a position in between.
async function layoutPdf(at) {
  const seq = (cur.layoutSeq = (cur.layoutSeq || 0) + 1);
  if (cur.pdfObserver) cur.pdfObserver.disconnect();
  for (const pg of cur.pages || []) if (pg.task) pg.task.cancel();
  cur.pdfScale = pdfScale();
  const wrap = $('pdfWrap'); wrap.replaceChildren();
  cur.pages = [];
  for (let n = 0; n < cur.pdf.numPages; n++) {
    const el = document.createElement('div'); el.className = 'pdfPage'; el.dataset.page = n;
    el.style.width = cur.pdfBase.width * cur.pdfScale + 'px'; el.style.height = cur.pdfBase.height * cur.pdfScale + 'px';
    el.style.setProperty('--scale-factor', cur.pdfScale);
    wrap.append(el); cur.pages.push({ el, tok: 0, rendered: null, task: null });
  }
  cur.pdfObserver = new IntersectionObserver((entries) => {
    if (!cur || seq !== cur.layoutSeq) return;
    for (const e of entries) { const n = +e.target.dataset.page; if (e.isIntersecting) renderPdfPage(n); else releasePdfPage(n); }
  }, { rootMargin: '1500px 0px' });
  for (const pg of cur.pages) cur.pdfObserver.observe(pg.el);
  updateZoomLabel();
  await gotoPdf(at.page, at.frac || 0);
}
function renderPdfPage(n) {
  const pg = cur && cur.pages && cur.pages[n];
  if (!pg) return Promise.resolve();
  if (pg.rendered) return pg.rendered;
  const tok = ++pg.tok, book = cur, seq = cur.layoutSeq;
  const alive = () => cur === book && pg.tok === tok && seq === cur.layoutSeq; // false once released or re-laid out
  pg.rendered = (async () => {
    const page = await book.pdf.getPage(n + 1);
    if (!alive()) return;
    const dpr = devicePixelRatio || 1, scale = book.pdfScale;
    const vp = page.getViewport({ scale }), hi = page.getViewport({ scale: scale * dpr });
    const c = document.createElement('canvas');
    c.width = hi.width; c.height = hi.height; c.style.width = vp.width + 'px'; c.style.height = vp.height + 'px';
    pg.task = page.render({ canvasContext: c.getContext('2d'), viewport: hi });
    try { await pg.task.promise; } catch (e) { if (e && e.name === 'RenderingCancelledException') return; throw e; }
    if (!alive()) return;
    const tl = document.createElement('div'); tl.className = 'textLayer';
    pg.el.style.width = vp.width + 'px'; pg.el.style.height = vp.height + 'px';
    pg.el.replaceChildren(c, tl);
    await new book.pdfjs.TextLayer({ textContentSource: page.streamTextContent(), container: tl, viewport: vp }).render();
    if (!alive()) return;
    // Number the text spans in reading order so selections are stored as (block, offset) like EPUB paragraphs.
    let b = 0;
    for (const sp of tl.querySelectorAll('span:not(.markedContent)')) if (sp.textContent) sp.dataset.b = b++;
    renderHighlights(n);
  })();
  return pg.rendered;
}
function releasePdfPage(n) {
  const pg = cur && cur.pages && cur.pages[n];
  if (!pg || !pg.rendered) return;
  pg.tok++; pg.rendered = null;
  if (pg.task) pg.task.cancel();
  pg.task = null; pg.el.replaceChildren();
}
// gotoPdf scrolls to a page (and a fraction down it, or to an exact text span).
async function gotoPdf(page, frac = 0, block = null) {
  page = clampPage(page);
  const el = cur.pages[page].el, r = el.getBoundingClientRect();
  scrollTo(scrollX, Math.max(0, r.top + scrollY + frac * r.height - PDF_TOP));
  if (block != null) {
    await renderPdfPage(page);
    const span = el.querySelector(`[data-b="${block}"]`);
    if (span) scrollTo(scrollX, Math.max(0, span.getBoundingClientRect().top + scrollY - 90));
  }
  onScroll();
}
function goPdf(n) { return gotoPdf(n).then(() => { dirty = true; flush(); }); }
function pdfCurrentPage() {
  const last = cur.pages.length - 1, bottom = (k) => cur.pages[k].el.getBoundingClientRect().bottom;
  let i = Math.min(cur.pdfPage || 0, last);
  while (i < last && bottom(i) <= PDF_TOP) i++;
  while (i > 0 && bottom(i - 1) > PDF_TOP) i--;
  return i;
}
function pdfAnchor() {
  const i = (cur.pdfPage = pdfCurrentPage());
  const r = cur.pages[i].el.getBoundingClientRect();
  const y = Math.max(0, Math.min(1000, Math.round(((PDF_TOP - r.top) / r.height) * 1000)));
  return { section: i, block: 0, offset: y, percent: (i + y / 1000) / cur.pdf.numPages, quote: '' };
}
function updateZoomLabel() { $('zoomLbl').textContent = Math.round(prefs.pdfZoom * 100) + '%'; }
async function setZoom(z) {
  if (!cur || !isPdf() || !cur.pages || !PDF_ZOOMS.includes(z) || z === prefs.pdfZoom) return;
  const a = pdfAnchor(); // keep the same spot on the page
  prefs.pdfZoom = z; applyPrefs(true);
  await layoutPdf({ page: a.section, frac: a.offset / 1000 });
}
function zoomStep(d) {
  const i = PDF_ZOOMS.indexOf(prefs.pdfZoom);
  setZoom(PDF_ZOOMS[Math.max(0, Math.min(PDF_ZOOMS.length - 1, (i < 0 ? PDF_ZOOMS.indexOf(1) : i) + d))]);
}
$('zoomIn').onclick = () => zoomStep(1);
$('zoomOut').onclick = () => zoomStep(-1);
$('zoomLbl').onclick = () => setZoom(1);
// A different window width changes "fit to width"; re-lay out only if the scale really changed.
addEventListener('resize', debounce(() => {
  if (!cur || !isPdf() || !cur.pages || Math.abs(pdfScale() - cur.pdfScale) < 0.01) return;
  const a = pdfAnchor(); layoutPdf({ page: a.section, frac: a.offset / 1000 });
}, 250));

/* ---------- reading time (idle-aware) ---------- */
let lastAct = 0, active = 0, timer = 0;
function activity() { lastAct = Date.now(); }
function startTimer() {
  stopTimer(); active = 0; activity();
  timer = setInterval(() => { if (Date.now() - lastAct < 30000 && !document.hidden) active += 5; if (active >= 30) beat(); }, 5000);
}
function stopTimer() { clearInterval(timer); beat(); }
function beat() {
  if (!cur || active <= 0) return;
  // Words read = forward progress since the last beat, capped at a plausible 600 wpm.
  const p = cur.anchor ? cur.anchor.percent : 0;
  const fwd = Math.max(0, p - (cur.lastPct ?? p));
  cur.lastPct = p;
  const words = Math.min(Math.round(fwd * (cur.book.WordCount || 0)), Math.round(active * 10));
  const body = JSON.stringify({ Seconds: active, Words: words });
  active = 0;
  navigator.sendBeacon(`/api/books/${cur.id}/heartbeat`, new Blob([body], { type: 'application/json' }));
}

applyPrefs();
loadLibrary();

/* ---------- annotations: bookmarks, highlights, notes ---------- */
async function loadAnns() {
  cur.anns = await api(`/api/books/${cur.id}/annotations`);
  renderHighlights();
  updateMarkBtn();
  if (!$('notes').hidden) renderNotesPanel();
}
const isPdf = () => cur.book.Format === 'pdf';
const root = () => (isPdf() ? $('pdfWrap') : $('page'));
const sectionNow = () => (isPdf() ? cur.pdfPage : cur.section);

// wrapRange wraps characters [from,to) of el's text in <mark> elements.
function wrapRange(el, from, to, cls, id) {
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const todo = []; let pos = 0, n;
  while ((n = w.nextNode())) {
    const len = n.length, s = Math.max(from, pos), e = Math.min(to, pos + len);
    if (s < e && !n.parentElement.closest(`mark.hl[data-id="${id}"]`)) todo.push([n, s - pos, e - pos]);
    pos += len;
  }
  for (const [node, s, e] of todo) {
    const r = document.createRange(); r.setStart(node, s); r.setEnd(node, e);
    const m = document.createElement('mark'); m.className = cls; m.dataset.id = id;
    r.surroundContents(m);
  }
}
// renderHighlights redraws marks: the whole EPUB section, or for PDFs the text
// layer of one page (onlyPage) or of every drawn page.
function renderHighlights(onlyPage = null) {
  if (!cur) return;
  const targets = isPdf()
    ? (cur.pages || []).map((pg, n) => ({ el: pg.el.querySelector('.textLayer'), section: n })).filter((t) => t.el && (onlyPage == null || t.section === onlyPage))
    : [{ el: $('page'), section: cur.section }];
  for (const { el, section } of targets) {
    for (const m of el.querySelectorAll('mark.hl')) m.replaceWith(...m.childNodes);
    el.normalize();
    for (const a of cur.anns || []) {
      if (a.kind === 'bookmark' || a.start.section !== section) continue;
      const cls = `hl ${a.color || ''} ${a.kind === 'note' ? 'note' : ''}`;
      for (let b = a.start.block; b <= a.endBlock; b++) {
        const be = el.querySelector(`[data-b="${b}"]`); if (!be) continue;
        const from = b === a.start.block ? a.start.offset : 0;
        const to = b === a.endBlock ? a.endOffset : be.textContent.length;
        wrapRange(be, from, to, cls, a.id);
      }
    }
  }
}
document.addEventListener('click', (e) => {
  const m = e.target.closest('mark.hl');
  if (!m || !cur || !getSelection().isCollapsed) { if (!$('hlbar').contains(e.target)) $('hlbar').hidden = true; return; }
  // Clicking a highlight offers Remove / Notes right beside it.
  const bar = $('hlbar'), r = m.getBoundingClientRect();
  const ids = new Set(); for (let x = m; x; x = x.parentElement && x.parentElement.closest('mark.hl')) ids.add(x.dataset.id);
  bar.dataset.ids = [...ids].join(','); bar.hidden = false;
  bar.style.top = Math.max(56, r.top - 46) + 'px';
  bar.style.left = Math.min(innerWidth - 230, Math.max(8, r.left)) + 'px';
});
$('hlRemove').onclick = async () => {
  const ids = $('hlbar').dataset.ids.split(','); $('hlbar').hidden = true;
  for (const id of ids) await api(`/api/books/${cur.id}/annotations/${id}`, { method: 'DELETE' });
  await loadAnns(); toast(ids.length > 1 ? `${ids.length} highlights removed` : 'Highlight removed');
};
$('hlNotes').onclick = () => {
  const id = $('hlbar').dataset.ids.split(',')[0]; $('hlbar').hidden = true;
  if ($('notes').hidden) togglePanel('notes');
  renderNotesPanel(id);
};

// Selection -> anchor coordinates (block index + character offset).
function pointOf(node, off) {
  const el = (node.nodeType === 3 ? node.parentElement : node).closest('[data-b]');
  if (!el) return null;
  const r = document.createRange(); r.selectNodeContents(el); r.setEnd(node, off);
  return { block: +el.dataset.b, offset: r.toString().length };
}
let pendingSel = null;
function checkSelection() {
  const sel = getSelection();
  if (!cur || !sel || sel.isCollapsed || sel.rangeCount === 0 || !root().contains(sel.anchorNode) || !root().contains(sel.focusNode)) { $('selbar').hidden = true; return; }
  const range = sel.getRangeAt(0); // always in document order
  // A highlight lives on one EPUB section or one PDF page; a selection spanning pages is clamped to its first page.
  let scope = $('page'), section = cur.section;
  if (isPdf()) {
    const pg = (cur.pages || []).findIndex((p) => range.intersectsNode(p.el));
    if (pg < 0) { $('selbar').hidden = true; return; }
    scope = cur.pages[pg].el; section = pg;
  }
  const spans = [...scope.querySelectorAll('[data-b]')].filter((el) => range.intersectsNode(el));
  if (!spans.length) { $('selbar').hidden = true; return; }
  const inScope = (n) => scope.contains(n.nodeType === 3 ? n.parentElement : n);
  const a = (inScope(range.startContainer) && pointOf(range.startContainer, range.startOffset)) || { block: +spans[0].dataset.b, offset: 0 };
  const b = (inScope(range.endContainer) && pointOf(range.endContainer, range.endOffset)) || { block: +spans.at(-1).dataset.b, offset: spans.at(-1).textContent.length };
  if (a.block === b.block && a.offset === b.offset) { $('selbar').hidden = true; return; }
  pendingSel = { a, b, section, quote: sel.toString().replace(/\s+/g, ' ').trim() };
  const rect = range.getBoundingClientRect();
  const bar = $('selbar'); bar.hidden = false; $('noteForm').hidden = true; $('selBtns').hidden = false;
  bar.style.top = Math.max(56, rect.top - 52) + 'px';
  bar.style.left = Math.min(innerWidth - 260, Math.max(8, rect.left)) + 'px';
}
document.addEventListener('mouseup', (e) => { if (!$('selbar').contains(e.target)) setTimeout(checkSelection, 0); });
document.addEventListener('keyup', (e) => { if (e.key.startsWith('Arrow') || e.key === 'Shift') checkSelection(); });
document.addEventListener('mousedown', (e) => { if (!$('selbar').contains(e.target)) $('selbar').hidden = true; });

async function addAnn(kind, extra = {}) {
  if (!pendingSel) return;
  const { a, b, quote, section: sec } = pendingSel;
  const covered = (cur.anns || []).some((x) => x.kind !== 'bookmark' && x.start.section === sec &&
    (x.start.block < a.block || (x.start.block === a.block && x.start.offset <= a.offset)) &&
    (x.endBlock > b.block || (x.endBlock === b.block && x.endOffset >= b.offset)));
  if (covered && kind === 'highlight') { getSelection().removeAllRanges(); $('selbar').hidden = true; toast('Already highlighted'); return; }
  const body = { kind, quote, color: 'yellow', endBlock: b.block, endOffset: b.offset, ...extra,
    start: { section: sec, block: a.block, offset: a.offset, percent: isPdf() ? sec / cur.pdf.numPages : currentAnchor().percent, quote } };
  await send(`/api/books/${cur.id}/annotations`, 'POST', body);
  pendingSel = null; getSelection().removeAllRanges(); $('selbar').hidden = true;
  await loadAnns();
}
$('hlBtn').onclick = () => addAnn('highlight');
$('noteBtn').onclick = () => { $('selBtns').hidden = true; $('noteForm').hidden = false; $('noteText').value = ''; $('noteText').focus(); };
$('noteCancel').onclick = () => { $('selbar').hidden = true; };
$('noteSave').onclick = () => { const t = $('noteText').value.trim(); if (t) addAnn('note', { note: t }); };
$('noteText').onkeydown = (e) => { e.stopPropagation(); if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) $('noteSave').click(); if (e.key === 'Escape') $('noteCancel').click(); };
const bookmarkHere = () => {
  const a = currentAnchor();
  return (cur.anns || []).find((x) => x.kind === 'bookmark' && x.start.section === a.section && (isPdf() || x.start.block === a.block));
};
function updateMarkBtn() {
  if (!cur || (isPdf() && !cur.pages)) return; // PDF not laid out yet: no page to compare
  $('markBtn').classList.toggle('on', !!bookmarkHere());
}
async function toggleBookmark() {
  if (!cur) return;
  const have = bookmarkHere();
  if (have) await api(`/api/books/${cur.id}/annotations/${have.id}`, { method: 'DELETE' });
  else {
    const a = currentAnchor();
    await send(`/api/books/${cur.id}/annotations`, 'POST', { kind: 'bookmark', quote: a.quote || (isPdf() ? `Page ${a.section + 1}` : 'Bookmark'), start: a });
  }
  await loadAnns(); toast(have ? 'Bookmark removed' : 'Bookmarked');
}
$('markBtn').onclick = toggleBookmark;
function toast(msg) {
  const t = $('toast'); t.textContent = msg; t.hidden = false;
  clearTimeout(toast.t); toast.t = setTimeout(() => (t.hidden = true), 2200);
}
function flash(el) { el.style.outline = '2px solid var(--accent)'; setTimeout(() => (el.style.outline = ''), 600); }

function jumpTo(a, exact = false) {
  $('notes').hidden = true;
  if (isPdf()) { cur.anchor = a; gotoPdf(a.section, a.offset / 1000, exact ? a.block : null); return; }
  cur.anchor = a;
  if (a.section === cur.section) restoreAnchor(a); else loadSection(a.section, a);
}
$('notesBtn').onclick = () => { if (togglePanel('notes')) renderNotesPanel(); };
let notesFilter = 'all';
const annWhere = (a) => (isPdf() ? `p.${a.start.section + 1}` : `${Math.round(a.start.percent * 100)}%`);
// renderNotesPanel lists annotations in reading order, grouped by chapter or page,
// with a filter by kind, colour pickers, and inline note editing.
function renderNotesPanel(focusId) {
  const p = $('notes'), el = (t, cls, txt) => Object.assign(document.createElement(t), { className: cls || '', textContent: txt || '' });
  p.replaceChildren();
  p.append(el('h3', '', 'Notes & highlights'));
  const count = (k) => cur.anns.filter((a) => k === 'all' || a.kind === k).length;
  const chips = el('div', 'chips');
  for (const k of ['all', 'highlight', 'note', 'bookmark']) {
    const c = el('button', 'chip' + (notesFilter === k ? ' on' : ''), `${k === 'all' ? 'All' : k[0].toUpperCase() + k.slice(1) + 's'} ${count(k)}`);
    c.dataset.kind = k; c.onclick = () => { notesFilter = k; renderNotesPanel(); };
    chips.append(c);
  }
  p.append(chips);
  const ex = el('a', '', 'Export as Markdown'); ex.href = `/api/books/${cur.id}/export.md`; ex.download = 'notes.md'; p.append(ex);
  const list = cur.anns.filter((a) => notesFilter === 'all' || a.kind === notesFilter)
    .sort((x, y) => x.start.section - y.start.section || x.start.block - y.start.block || x.start.offset - y.start.offset);
  if (!cur.anns.length) p.append(el('p', 'sum', 'Select text to highlight it, or press the bookmark button (B).'));
  else if (!list.length) p.append(el('p', 'sum', 'Nothing of this kind yet.'));
  let lastGroup = null;
  for (const a of list) {
    const group = isPdf() ? `Page ${a.start.section + 1}` : `Chapter ${a.start.section + 1}`;
    if (group !== lastGroup) { p.append(el('div', 'grp', group)); lastGroup = group; }
    const d = el('div', 'ann ' + a.kind);
    const q = el('q', '', (a.kind === 'bookmark' ? '🔖 ' : '') + (a.quote || 'Bookmark'));
    q.onclick = () => jumpTo(a.start, a.kind !== 'bookmark');
    const meta = el('div', 'meta'); meta.append(el('small', '', `${annWhere(a)} · ${a.kind}`));
    const ta = el('textarea'); ta.rows = 2; ta.placeholder = 'Add a note…'; ta.value = a.note || ''; ta.hidden = a.kind === 'bookmark';
    const acts = el('div', 'acts');
    const save = el('button', '', 'Save note'); save.hidden = a.kind === 'bookmark';
    save.onclick = async () => { await send(`/api/books/${cur.id}/annotations/${a.id}`, 'PATCH', { Note: ta.value }); await loadAnns(); };
    const del = el('button', 'danger', 'Delete');
    del.onclick = async () => { await api(`/api/books/${cur.id}/annotations/${a.id}`, { method: 'DELETE' }); await loadAnns(); };
    acts.append(save, del); d.append(q, meta, ta, acts); p.append(d);
    if (String(a.id) === String(focusId)) { d.classList.add('focus'); setTimeout(() => d.scrollIntoView({ block: 'center' }), 0); }
  }
}

/* ---------- keyboard shortcut help ---------- */
const SHORTCUTS = [
  ['Reading', [['Next page or chapter', '→'], ['Previous page or chapter', '←'], ['Scroll down (EPUB)', 'Space'], ['Scroll up (EPUB)', 'PageUp'], ['Bookmark this spot', 'B'], ['Back to library', 'Esc']]],
  ['PDF', [['Zoom in', '+'], ['Zoom out', '-'], ['Reset zoom', '0']]],
  ['Find', [['Search in this book', 'Ctrl', 'F']]],
  ['Help', [['Show or hide this list', '?']]],
];
function buildHelp() {
  const body = $('helpBody'); body.replaceChildren();
  for (const [title, rows] of SHORTCUTS) {
    body.append(Object.assign(document.createElement('h4'), { textContent: title }));
    for (const [what, ...keys] of rows) {
      const r = Object.assign(document.createElement('div'), { className: 'sc' });
      const k = document.createElement('span');
      for (const key of keys) k.append(Object.assign(document.createElement('kbd'), { textContent: key }));
      r.append(Object.assign(document.createElement('span'), { textContent: what }), k); body.append(r);
    }
  }
}
function toggleHelp(show = $('helpModal').hidden) {
  if (show && !$('helpBody').children.length) buildHelp();
  $('helpModal').hidden = !show;
}
$('helpBtn').onclick = () => toggleHelp();
$('helpClose').onclick = () => toggleHelp(false);
$('helpModal').onclick = (e) => { if (e.target === $('helpModal')) toggleHelp(false); };
// Capture phase so Escape closes the dialog before the reader's own handler sees it.
addEventListener('keydown', (e) => {
  if (/INPUT|SELECT|TEXTAREA/.test(e.target.tagName) || e.ctrlKey || e.metaKey || e.altKey) return;
  if (e.key === '?') { e.preventDefault(); toggleHelp(); }
  else if (e.key === 'Escape' && !$('helpModal').hidden) { e.preventDefault(); e.stopImmediatePropagation(); toggleHelp(false); }
}, true);

/* ---------- search ---------- */
function debounce(fn, ms) { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; }
function hitEl(h, showTitle) {
  const d = document.createElement('a'); d.className = 'hit';
  if (showTitle) { const s = document.createElement('small'); s.textContent = h.title; d.append(s); }
  const sn = document.createElement('span'); sn.innerHTML = h.snippet; // server escapes everything except <mark>
  d.append(sn); return d;
}
async function runSearch(q, book, into, showTitle, open) {
  into.replaceChildren();
  if (q.trim().length < 2) return;
  const hits = await api(`/api/search?q=${encodeURIComponent(q)}&book=${book || ''}`);
  if (!hits.length) { into.textContent = 'No matches.'; return; }
  for (const h of hits) { const el = hitEl(h, showTitle); el.onclick = () => open(h); into.append(el); }
}
const hitAnchor = (h) => ({ section: h.section, block: h.block, offset: 0, percent: 0, quote: '' });
let activeTag = null;
function filterShelf() {
  const q = $('libSearch').value.trim().toLowerCase();
  for (const c of $('shelf').children) {
    const okText = !q || q.split(/\s+/).every((w) => c.dataset.q.includes(w));
    const okTag = !activeTag || JSON.parse(c.dataset.tags).includes(activeTag);
    c.hidden = !(okText && okTag);
  }
}
function renderTagBar(books) {
  const all = [...new Set(books.flatMap((b) => (b.Tags || []).map((t) => t.toLowerCase())))].sort();
  if (activeTag && !all.includes(activeTag)) activeTag = null;
  const bar = $('tagbar'); bar.replaceChildren();
  if (!all.length) return;
  for (const t of ['All', ...all]) {
    const chip = document.createElement('button'); chip.className = 'chip'; chip.textContent = t === 'All' ? 'All' : '#' + t;
    chip.classList.toggle('on', t === 'All' ? !activeTag : t === activeTag);
    chip.onclick = () => { activeTag = t === 'All' ? null : t; renderTagBar(books); filterShelf(); };
    bar.append(chip);
  }
}
// editTags swaps the tag row for an input; Enter saves, Escape cancels.
function editTags(b, card, row) {
  const input = document.createElement('input'); input.className = 'tagedit'; input.placeholder = 'tags, separated by commas';
  input.value = (b.Tags || []).join(', ');
  input.onclick = (e) => e.stopPropagation();
  input.onkeydown = async (e) => {
    e.stopPropagation();
    if (e.key === 'Escape') loadLibrary();
    if (e.key === 'Enter') { await send(`/api/books/${b.ID}/tags`, 'PUT', { Tags: input.value.split(',') }); loadLibrary(); }
  };
  row.replaceChildren(input); input.focus();
}
$('libSearch').oninput = () => { filterShelf(); searchLibrary(); };
const searchLibrary = debounce(() =>
  runSearch($('libSearch').value, '', $('libHits'), true, (h) => openBook(h.bookId, hitAnchor(h))), 200);
$('findBtn').onclick = () => { if (togglePanel('find')) $('findInput').focus(); };
$('findInput').oninput = debounce(() =>
  runSearch($('findInput').value, cur.id, $('findHits'), false, (h) => { $('find').hidden = true; jumpTo(hitAnchor(h)); }), 200);
addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === 'f' && cur && !$('reader').hidden) { e.preventDefault(); $('findBtn').click(); }
});

// Surface unexpected errors instead of leaving a silently blank reader.
addEventListener('unhandledrejection', (e) => toast('Error: ' + (e.reason && e.reason.message ? e.reason.message : e.reason)));

/* ---------- immersive reading: toolbars step aside while you read ---------- */
// Only real user scrolling (wheel / touch) hides them, never programmatic jumps.
const inReader = () => !$('reader').hidden && $('scrim').hidden;
addEventListener('wheel', (e) => { if (inReader() && Math.abs(e.deltaY) > 2) document.body.classList.toggle('chrome-hidden', e.deltaY > 0); }, { passive: true });
let touchY = 0;
addEventListener('touchstart', (e) => { touchY = e.touches[0].clientY; }, { passive: true });
addEventListener('touchmove', (e) => {
  if (!inReader()) return;
  const y = e.touches[0].clientY;
  if (Math.abs(y - touchY) > 8) { document.body.classList.toggle('chrome-hidden', y < touchY); touchY = y; }
}, { passive: true });
addEventListener('mousemove', (e) => { if (e.clientY < 64 || e.clientY > innerHeight - 80) document.body.classList.remove('chrome-hidden'); }, { passive: true });
