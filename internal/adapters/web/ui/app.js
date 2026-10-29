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
  if (!h.hidden) { h.hidden = true; return; }
  $('toc').hidden = $('settings').hidden = true;
  const rows = await api(`/api/books/${cur.id}/history`);
  h.replaceChildren(Object.assign(document.createElement('h3'), { textContent: 'Reading history' }));
  if (!rows.length) h.append('No sessions yet.');
  for (const s of rows) {
    const d = document.createElement('div');
    d.textContent = `${new Date(s.start).toLocaleString()} — ${Math.max(1, Math.round(s.seconds / 60))} min, ${s.words} words`;
    h.append(d);
  }
  h.hidden = false;
};
$('setBtn').onclick = () => { $('toc').hidden = $('history').hidden = true; $('settings').hidden = !$('settings').hidden; };
$('tocBtn').onclick = () => { $('settings').hidden = $('history').hidden = true; $('toc').hidden = !$('toc').hidden; };

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
  $('library').hidden = false; $('reader').hidden = true; $('toc').hidden = $('settings').hidden = $('history').hidden = true;
  $('tocBtn').hidden = $('histBtn').hidden = true; $('title').textContent = 'docsreader';
  loadLibrary();
}

/* ---------- reader ---------- */
async function openBook(id) {
  const info = await api('/api/books/' + id);
  cur = { id, book: info.book, toc: info.toc, sectionWords: info.sectionWords, anchor: info.hasProgress ? info.progress : null, section: 0, pdf: null };
  $('library').hidden = true; $('reader').hidden = false;
  $('title').textContent = info.book.Title;
  const isPdf = info.book.Format === 'pdf';
  $('page').hidden = isPdf; $('pdfWrap').hidden = !isPdf; $('tocBtn').hidden = isPdf; $('histBtn').hidden = false;
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
  const keep = cur.anchor; // scrolling must not overwrite the position we just restored
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
  if (cur.book.Format === 'epub') {
    const a = currentAnchor(); cur.anchor = a;
    $('pct').textContent = Math.round(a.percent * 100) + '%';
    $('seek').value = Math.round(a.percent * 1000);
    trackFocus();
  }
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
  if ($('reader').hidden || /INPUT|SELECT/.test(e.target.tagName)) return;
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
  window.scrollTo(0, yFrac * c.height / (devicePixelRatio || 1));
  onScroll();
}
function pdfAnchor() {
  const c = $('pdfWrap').firstChild;
  const h = c ? c.getBoundingClientRect().height : 1;
  const y = Math.max(0, Math.min(1000, Math.round(((scrollY - 60) / h) * 1000)));
  return { section: cur.pdfPage, block: 0, offset: y, percent: (cur.pdfPage + y / 1000) / cur.pdf.numPages, quote: '' };
}
function goPdf(n) { renderPdfPage(n); }

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
