const $ = (id) => document.getElementById(id);
const api = async (url, opt) => {
  const r = await fetch(url, opt);
  if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
  return r.status === 204 ? null : r.json();
};
const send = (url, method, body) =>
  api(url, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

/* ---------- settings ---------- */
const defaults = { theme: 'light', font: 'serif', size: 19, lh: 17, width: 68, focus: false };
let prefs = { ...defaults, ...safeParse(localStorage.getItem('prefs')) };
function safeParse(s) { try { return JSON.parse(s) || {}; } catch { return {}; } }
function applyPrefs() {
  document.body.dataset.theme = prefs.theme;
  const p = $('page');
  p.dataset.font = prefs.font;
  p.style.setProperty('--fs', prefs.size + 'px');
  p.style.setProperty('--lh', prefs.lh / 10);
  p.style.setProperty('--w', prefs.width + 'ch');
  p.classList.toggle('focus', prefs.focus);
  for (const k of ['theme', 'font', 'size', 'lh', 'width']) $(k).value = prefs[k];
  $('focus').checked = prefs.focus;
  localStorage.setItem('prefs', JSON.stringify(prefs));
}
for (const k of ['theme', 'font', 'size', 'lh', 'width']) {
  $(k).oninput = () => { prefs[k] = $(k).type === 'range' ? +$(k).value : $(k).value; applyPrefs(); restore(); };
}
$('focus').onchange = () => { prefs.focus = $('focus').checked; applyPrefs(); trackFocus(); };
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
function togglePanel(id) { const open = $(id).hidden; for (const p of panels) $(p).hidden = true; $(id).hidden = !open; return open; }
$('setBtn').onclick = ()=>togglePanel('settings');

$('tocBtn').onclick = () => togglePanel('toc');

/* ---------- library ---------- */
let cur = null; // current reading state
$('home').onclick = showLibrary;
$('upload').onchange = async (e) => {
  for (const f of e.target.files) {
    const fd = new FormData(); fd.append('file', f);
    try { await api('/api/books', { method: 'POST', body: fd }); } catch (err) { alert(f.name + ': ' + err.message); }
  }
  e.target.value = ''; loadLibrary();
};

const pct = (b) => (b.progress ? Math.round(b.progress.percent * 100) : 0);
function ago(iso) {
  const s = (Date.now() - new Date(iso)) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return Math.floor(s / 60) + ' min ago';
  if (s < 86400) return Math.floor(s / 3600) + ' h ago';
  return Math.floor(s / 86400) + ' d ago';
}
function coverEl(b) {
  const d = document.createElement('div'); d.className = 'cover';
  if (b.HasCover) { const i = new Image(); i.src = `/api/books/${b.ID}/cover`; i.alt = ''; d.replaceChildren(i); }
  else d.textContent = b.Title;
  return d;
}
async function loadLibrary() {
  const books = await api('/api/books');
  const shelf = $('shelf'); shelf.replaceChildren();
  for (const b of books) {
    const c = document.createElement('div'); c.className = 'card';
    const meta = document.createElement('div'); meta.className = 'meta';
    const t = document.createElement('b'); t.textContent = b.Title; t.title = b.Title;
    const a = document.createElement('span');
    a.textContent = b.lastRead ? `${pct(b)}% · ${ago(b.lastRead)}` : (b.Author || b.Format.toUpperCase());
    meta.append(t, a);
    const bar = document.createElement('div'); bar.className = 'bar';
    const fill = document.createElement('i'); fill.style.width = pct(b) + '%'; bar.append(fill);
    const del = document.createElement('button'); del.className = 'del'; del.textContent = '×';
    del.onclick = async (e) => { e.stopPropagation(); if (confirm('Remove "' + b.Title + '"?')) { await api('/api/books/' + b.ID, { method: 'DELETE' }); loadLibrary(); } };
    c.append(coverEl(b), bar, meta, del);
    c.onclick = () => openBook(b.ID);
    shelf.append(c);
  }
  const last = books.find((b) => b.progress && b.progress.percent < 0.999);
  const cont = $('continue'); cont.replaceChildren();
  if (last) {
    const h = document.createElement('div'); h.className = 'hero';
    const info = document.createElement('div');
    info.innerHTML = '<small>Continue reading</small><h3 style="margin:.2rem 0"></h3><span></span>';
    info.querySelector('h3').textContent = last.Title;
    info.querySelector('span').textContent = `${pct(last)}% read · ${ago(last.lastRead)}`;
    h.append(coverEl(last), info); h.onclick = () => openBook(last.ID); cont.append(h);
  }
  const st = await api('/api/stats');
  $('stats').textContent = st.totalSeconds
    ? `Read ${Math.round(st.totalSeconds / 60)} min total · ${Math.round(st.wordsPerMin)} words/min · ${st.streakDays}-day streak`
    : 'Your reading stats will appear here.';
}
async function showLibrary() {
  clearTimeout(saveT); await flush(); stopTimer(); cur = null;
  $('library').hidden = false; $('reader').hidden = true; for (const p of panels) $(p).hidden = true;
  $('selbar').hidden = true;
  for (const b of ['tocBtn', 'histBtn', 'markBtn', 'notesBtn', 'findBtn']) $(b).hidden = true; $('title').textContent = 'docsreader';
  loadLibrary();
}

/* ---------- reader ---------- */
async function openBook(id, target) {
  const info = await api('/api/books/' + id);
  cur = { id, book: info.book, toc: info.toc, sectionWords: info.sectionWords, anchor: target || (info.hasProgress ? info.progress : null), section: 0, pdf: null, anns: [] };
  $('library').hidden = true; $('reader').hidden = false;
  $('title').textContent = info.book.Title;
  const isPdf = info.book.Format === 'pdf';
  $('page').hidden = isPdf; $('pdfWrap').hidden = !isPdf; $('tocBtn').hidden = isPdf; $('histBtn').hidden = false;
  $('markBtn').hidden = $('notesBtn').hidden = false; $('findBtn').hidden = isPdf;
  await loadAnns();
  applyPrefs();
  if (isPdf) await openPdf(); else { buildToc(); await loadSection(cur.anchor ? cur.anchor.section : 0, cur.anchor); }
  startTimer();
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
function onScroll() {
  if (!cur || $('reader').hidden) return;
  const a = currentAnchor(); cur.anchor = a;
  $('pct').textContent = cur.book.Format === 'pdf'
    ? `p.${cur.pdfPage + 1}/${cur.pdf.numPages}` : Math.round(a.percent * 100) + '%';
  $('seek').value = Math.round(a.percent * 1000);
  if (cur.book.Format === 'epub') trackFocus();
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
  if ($('reader').hidden || /INPUT|SELECT|TEXTAREA/.test(e.target.tagName)) return;
  if (['ArrowRight', 'PageDown', ' '].includes(e.key)) { e.preventDefault(); step(1); }
  if (['ArrowLeft', 'PageUp'].includes(e.key)) { e.preventDefault(); step(-1); }
  if (e.key === 'Escape') showLibrary();
});

/* ---------- PDF (rendered client-side by vendored pdf.js) ---------- */
async function openPdf() {
  const pdfjs = await import('/vendor/pdf.min.mjs');
  pdfjs.GlobalWorkerOptions.workerSrc = '/vendor/pdf.worker.min.mjs';
  cur.pdf = await pdfjs.getDocument(`/api/books/${cur.id}/file`).promise;
  cur.book.Sections = cur.pdf.numPages;
  cur.pdfPage = cur.anchor ? cur.anchor.section : 0;
  await renderPdfPage(cur.pdfPage, cur.anchor ? cur.anchor.offset / 1000 : 0);
}
async function renderPdfPage(n, yFrac = 0) {
  n = Math.max(0, Math.min(cur.pdf.numPages - 1, n));
  cur.pdfPage = n;
  const page = await cur.pdf.getPage(n + 1);
  const vp0 = page.getViewport({ scale: 1 });
  const scale = Math.min(1.8, (Math.min(innerWidth, 900) - 24) / vp0.width) * (devicePixelRatio || 1);
  const vp = page.getViewport({ scale });
  const c = document.createElement('canvas'); c.width = vp.width; c.height = vp.height;
  c.style.width = vp.width / (devicePixelRatio || 1) + 'px';
  await page.render({ canvasContext: c.getContext('2d'), viewport: vp }).promise;
  $('pdfWrap').replaceChildren(c);
  window.scrollTo(0, 60 + yFrac * c.height / (devicePixelRatio || 1)); // 60 = header offset, matches pdfAnchor
  onScroll();
}
function pdfAnchor() {
  const c = $('pdfWrap').firstChild;
  const h = c ? c.getBoundingClientRect().height : 1;
  const y = Math.max(0, Math.min(1000, Math.round(((scrollY - 60) / h) * 1000)));
  return { section: cur.pdfPage, block: 0, offset: y, percent: (cur.pdfPage + y / 1000) / cur.pdf.numPages, quote: '' };
}
function goPdf(n) { renderPdfPage(n).then(() => { dirty = true; flush(); }); }

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
  if (cur.book.Format === 'epub') renderHighlights();
  if (!$('notes').hidden) renderNotesPanel();
}
const blockEl = (n) => $('page').querySelector(`[data-b="${n}"]`);

// wrapRange wraps characters [from,to) of el's text in <mark> elements.
function wrapRange(el, from, to, cls, id) {
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const todo = []; let pos = 0, n;
  while ((n = w.nextNode())) {
    const len = n.length, s = Math.max(from, pos), e = Math.min(to, pos + len);
    if (s < e) todo.push([n, s - pos, e - pos]);
    pos += len;
  }
  for (const [node, s, e] of todo) {
    const r = document.createRange(); r.setStart(node, s); r.setEnd(node, e);
    const m = document.createElement('mark'); m.className = cls; m.dataset.id = id;
    r.surroundContents(m);
  }
}
function renderHighlights() {
  if (!cur || cur.book.Format !== 'epub') return;
  for (const m of $('page').querySelectorAll('mark.hl')) m.replaceWith(...m.childNodes);
  $('page').normalize();
  for (const a of cur.anns || []) {
    if (a.kind === 'bookmark' || a.start.section !== cur.section) continue;
    const cls = `hl ${a.color || ''} ${a.kind === 'note' ? 'note' : ''}`;
    for (let b = a.start.block; b <= a.endBlock; b++) {
      const el = blockEl(b); if (!el) continue;
      const from = b === a.start.block ? a.start.offset : 0;
      const to = b === a.endBlock ? a.endOffset : el.textContent.length;
      wrapRange(el, from, to, cls, a.id);
    }
  }
}
$('page').addEventListener('click', (e) => {
  const m = e.target.closest('mark.hl'); if (!m) return;
  if ($('notes').hidden) togglePanel('notes');
  renderNotesPanel(m.dataset.id);
});

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
  if (!cur || cur.book.Format !== 'epub' || !sel || sel.isCollapsed || !$('page').contains(sel.anchorNode)) { $('selbar').hidden = true; return; }
  let a = pointOf(sel.anchorNode, sel.anchorOffset), b = pointOf(sel.focusNode, sel.focusOffset);
  if (!a || !b) { $('selbar').hidden = true; return; }
  if (a.block > b.block || (a.block === b.block && a.offset > b.offset)) [a, b] = [b, a];
  pendingSel = { a, b, quote: sel.toString().trim() };
  const rect = sel.getRangeAt(0).getBoundingClientRect();
  const bar = $('selbar'); bar.hidden = false;
  bar.style.top = Math.max(56, rect.top - 46) + 'px';
  bar.style.left = Math.min(innerWidth - 190, Math.max(8, rect.left)) + 'px';
}
document.addEventListener('mouseup', () => setTimeout(checkSelection, 0));
document.addEventListener('keyup', (e) => { if (e.key.startsWith('Arrow') || e.key === 'Shift') checkSelection(); });
document.addEventListener('mousedown', (e) => { if (!$('selbar').contains(e.target)) $('selbar').hidden = true; });

async function addAnn(kind, extra = {}) {
  if (!pendingSel) return;
  const { a, b, quote } = pendingSel;
  const body = { kind, quote, color: 'yellow', endBlock: b.block, endOffset: b.offset, ...extra,
    start: { section: cur.section, block: a.block, offset: a.offset, percent: currentAnchor().percent, quote } };
  await send(`/api/books/${cur.id}/annotations`, 'POST', body);
  pendingSel = null; getSelection().removeAllRanges(); $('selbar').hidden = true;
  await loadAnns();
}
$('hlBtn').onclick = () => addAnn('highlight');
$('noteBtn').onclick = () => {
  const text = prompt('Note');
  if (text && text.trim()) addAnn('note', { note: text.trim() });
};
$('markBtn').onclick = async () => {
  const a = currentAnchor();
  await send(`/api/books/${cur.id}/annotations`, 'POST', { kind: 'bookmark', quote: a.quote || (cur.book.Format === 'pdf' ? `Page ${cur.pdfPage + 1}` : ''), start: a });
  await loadAnns(); flash($('markBtn'));
};
function flash(el) { el.style.outline = '2px solid var(--accent)'; setTimeout(() => (el.style.outline = ''), 600); }

function jumpTo(a) {
  $('notes').hidden = true;
  if (cur.book.Format === 'pdf') { cur.anchor = a; renderPdfPage(a.section, a.offset / 1000); return; }
  cur.anchor = a;
  if (a.section === cur.section) restoreAnchor(a); else loadSection(a.section, a);
}
$('notesBtn').onclick = () => { if (togglePanel('notes')) renderNotesPanel(); };
function renderNotesPanel(focusId) {
  const p = $('notes'); p.replaceChildren();
  const h = document.createElement('h3'); h.textContent = 'Notes & highlights'; p.append(h);
  const ex = document.createElement('a'); ex.href = `/api/books/${cur.id}/export.md`; ex.textContent = 'Export as Markdown'; ex.download = 'notes.md'; p.append(ex);
  if (!cur.anns.length) p.append(Object.assign(document.createElement('p'), { textContent: 'Select text to highlight it, or press the bookmark button.' }));
  for (const a of cur.anns) {
    const d = document.createElement('div'); d.className = 'ann';
    const q = document.createElement('q');
    q.textContent = (a.kind === 'bookmark' ? '🔖 ' : '') + (a.quote || 'Bookmark') ; q.onclick = () => jumpTo(a.start);
    const meta = document.createElement('small'); meta.textContent = `${Math.round(a.start.percent * 100)}% · ${a.kind}`;
    const ta = document.createElement('textarea'); ta.rows = 2; ta.placeholder = 'Add a note…'; ta.value = a.note || '';
    ta.hidden = a.kind === 'bookmark';
    const acts = document.createElement('div'); acts.className = 'acts';
    const save = document.createElement('button'); save.textContent = 'Save note'; save.hidden = a.kind === 'bookmark';
    save.onclick = async () => { await send(`/api/books/${cur.id}/annotations/${a.id}`, 'PATCH', { Note: ta.value }); await loadAnns(); };
    const del = document.createElement('button'); del.textContent = 'Delete';
    del.onclick = async () => { await api(`/api/books/${cur.id}/annotations/${a.id}`, { method: 'DELETE' }); await loadAnns(); };
    acts.append(save, del); d.append(q, meta, ta, acts); p.append(d);
    if (String(a.id) === String(focusId)) { d.style.background = 'var(--bg)'; setTimeout(() => d.scrollIntoView({ block: 'center' }), 0); }
  }
}

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
$('libSearch').oninput = debounce(() =>
  runSearch($('libSearch').value, '', $('libHits'), true, (h) => openBook(h.bookId, hitAnchor(h))), 200);
$('findBtn').onclick = () => { if (togglePanel('find')) $('findInput').focus(); };
$('findInput').oninput = debounce(() =>
  runSearch($('findInput').value, cur.id, $('findHits'), false, (h) => { $('find').hidden = true; jumpTo(hitAnchor(h)); }), 200);
addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === 'f' && cur && cur.book.Format === 'epub') { e.preventDefault(); $('findBtn').click(); }
});
