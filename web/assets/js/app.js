(function () {
  'use strict';

  var content = document.getElementById('tv-content');
  if (!content) { return; }
  var jobTimer = null;
  var inflight = 0;

  /* ---------- theme ---------- */

  function effectiveTheme() {
    var set = document.documentElement.getAttribute('data-theme');
    if (set === 'light' || set === 'dark') { return set; }
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function toggleTheme() {
    var next = effectiveTheme() === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    try { localStorage.setItem('tv-theme', next); } catch (e) { /* ignore */ }
  }

  /* ---------- navigation without full reloads ---------- */

  function setNav(name) {
    document.querySelectorAll('.tv-nav-link').forEach(function (a) {
      a.classList.toggle('is-active', a.getAttribute('data-nav') === name);
    });
  }

  // The language links and the sign-in button lead back to the page they were clicked on, so
  // after an in-page navigation their "next" must name the new page, not the one that was loaded first.
  function syncReturnLinks(url) {
    var here = new URL(url, window.location.href);
    var target = here.pathname + here.search;
    document.querySelectorAll('a.tv-lang-link, a.tv-signin').forEach(function (a) {
      var link = new URL(a.getAttribute('href'), window.location.href);
      link.searchParams.set('next', target);
      a.setAttribute('href', link.pathname + link.search);
    });
  }

  function swap(html, res, push) {
    content.innerHTML = html;
    var title = res.headers.get('X-TV-Title');
    document.title = (title ? decodeURIComponent(title) + ' \u00b7 ' : '') + 'TimeVault';
    setNav(res.headers.get('X-TV-Nav') || '');
    if (push) { history.pushState({ tv: 1 }, '', res.url); }
    syncReturnLinks(res.url);
    init(content);
  }

  function fetchPartial(url) {
    return fetch(url, { credentials: 'same-origin', headers: { 'X-TV-Partial': '1' } }).then(function (res) {
      if (new URL(res.url).pathname === '/login') {
        window.location.assign(res.url);
        return null;
      }
      if ((res.headers.get('Content-Type') || '').indexOf('text/html') < 0) {
        window.location.assign(url);
        return null;
      }
      return res.text().then(function (html) { return { res: res, html: html }; });
    });
  }

  function navigate(url, push, keepScroll) {
    var token = ++inflight;
    document.body.classList.add('is-loading');
    fetchPartial(url).then(function (r) {
      if (!r || token !== inflight) { return; }
      var y = window.scrollY;
      swap(r.html, r.res, push);
      window.scrollTo(0, keepScroll ? y : 0);
    }).catch(function () {
      window.location.assign(url);
    }).then(function () {
      if (token === inflight) { document.body.classList.remove('is-loading'); }
    });
  }

  function skipBoost(a) {
    if (a.target || a.hasAttribute('download') || a.hasAttribute('data-no-boost')) { return true; }
    var u = new URL(a.href, window.location.href);
    if (u.origin !== window.location.origin) { return true; }
    return u.pathname.indexOf('/static/') === 0 || u.pathname === '/lang' || u.pathname === '/logout';
  }

  /* ---------- list helpers ---------- */

  function initSelection(root) {
    root.querySelectorAll('form[data-selection-form]').forEach(function (form) {
      var all = form.querySelector('[data-select-all]');
      var counter = form.querySelector('[data-selection-count]');
      var needs = form.querySelectorAll('[data-needs-selection]');
      function items() { return form.querySelectorAll('[data-select-item]'); }
      function update() {
        var list = items();
        var n = 0;
        list.forEach(function (i) { if (i.checked) { n++; } });
        if (counter) { counter.textContent = String(n); }
        needs.forEach(function (b) { b.disabled = n === 0; });
        if (all) { all.checked = n > 0 && n === list.length; all.indeterminate = n > 0 && n < list.length; }
      }
      if (all) {
        all.addEventListener('change', function () {
          items().forEach(function (i) { i.checked = all.checked; });
          update();
        });
      }
      form.addEventListener('change', function (e) {
        if (e.target && e.target.matches && e.target.matches('[data-select-item]')) { update(); }
      });
      update();
    });
  }

  function initAutosubmit(root) {
    root.querySelectorAll('form[data-autosubmit] select').forEach(function (sel) {
      sel.addEventListener('change', function () {
        if (sel.form.requestSubmit) { sel.form.requestSubmit(); } else { sel.form.submit(); }
      });
    });
  }

  function initTimeline(root) {
    var selected = root.querySelector('[data-timeline] [data-selected]');
    if (selected && selected.scrollIntoView) { selected.scrollIntoView({ inline: 'center', block: 'nearest' }); }
  }

  /* ---------- background jobs ---------- */

  function initJobs(root) {
    if (jobTimer) { clearTimeout(jobTimer); jobTimer = null; }
    var active = root.querySelectorAll('[data-job-id][data-job-active="1"]');
    if (!active.length) { return; }
    var reload = !!root.querySelector('[data-job-reload]');
    var ids = [];
    active.forEach(function (el) {
      var id = el.getAttribute('data-job-id');
      if (ids.indexOf(id) < 0) { ids.push(id); }
    });

    function tick() {
      Promise.all(ids.map(function (id) {
        return fetch('/api/v1/jobs/' + encodeURIComponent(id), { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
          .then(function (res) {
            // An automatic backup that finds nothing changed is forgotten: no job any more means it has finished.
            if (res.status === 404) { return { job: { active: false } }; }
            return res.ok ? res.json() : null;
          });
      })).then(function (results) {
        var finished = results.some(function (r) { return r && r.job && !r.job.active; });
        if (finished || reload) {
          navigate(window.location.href, false, true);
        } else {
          jobTimer = setTimeout(tick, 1500);
        }
      }).catch(function () { jobTimer = setTimeout(tick, 4000); });
    }
    jobTimer = setTimeout(tick, reload ? 1500 : 1000);
  }

  /* ---------- jump to top / bottom of a long page ---------- */

  var fab = document.querySelector('[data-fab]');
  var fabTop = fab && fab.querySelector('[data-fab-top]');
  var fabBottom = fab && fab.querySelector('[data-fab-bottom]');
  var fabFrame = 0;

  // The buttons only appear when the page is clearly longer than the window, and each one only when
  // there is something to jump to.
  function updateFab() {
    fabFrame = 0;
    if (!fab) { return; }
    var doc = document.documentElement;
    var room = doc.scrollHeight - window.innerHeight;
    var y = window.scrollY;
    var longPage = room > 600;
    fabTop.hidden = !(longPage && y > 300);
    fabBottom.hidden = !(longPage && y < room - 300);
    fab.hidden = fabTop.hidden && fabBottom.hidden;
  }

  function scheduleFab() {
    if (!fabFrame) { fabFrame = window.requestAnimationFrame(updateFab); }
  }

  function scrollPage(toBottom) {
    var target = toBottom ? document.documentElement.scrollHeight : 0;
    var calm = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    // A long page would scroll for ages; the point of the buttons is to get there at once.
    var far = Math.abs(target - window.scrollY) > 3 * window.innerHeight;
    window.scrollTo({ top: target, behavior: calm || far ? 'auto' : 'smooth' });
  }

  function initFab() {
    if (!fab) { return; }
    fabTop.addEventListener('click', function () { scrollPage(false); });
    fabBottom.addEventListener('click', function () { scrollPage(true); });
    window.addEventListener('scroll', scheduleFab, { passive: true });
    window.addEventListener('resize', scheduleFab);
    if (window.ResizeObserver) { new ResizeObserver(scheduleFab).observe(content); }
    scheduleFab();
  }

  /* ---------- copy a path ---------- */

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) { return navigator.clipboard.writeText(text); }
    // Plain HTTP on the network is not a secure context, so the clipboard API is not offered there.
    return new Promise(function (resolve, reject) {
      var box = document.createElement('textarea');
      box.value = text;
      box.setAttribute('readonly', '');
      box.style.position = 'fixed';
      box.style.opacity = '0';
      document.body.appendChild(box);
      box.select();
      var ok = false;
      try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
      document.body.removeChild(box);
      if (ok) { resolve(); } else { reject(new Error('copy failed')); }
    });
  }

  function copyFrom(button) {
    var label = button.querySelector('[data-copy-label]');
    var before = label ? label.textContent : '';
    copyText(button.getAttribute('data-copy')).then(function () {
      button.classList.add('is-copied');
      if (label) { label.textContent = button.getAttribute('data-copied') || before; }
      setTimeout(function () {
        button.classList.remove('is-copied');
        if (label) { label.textContent = before; }
      }, 1600);
    }).catch(function () { /* the path stays selectable in the page */ });
  }

  function init(root) {
    initSelection(root);
    initAutosubmit(root);
    initTimeline(root);
    initJobs(root);
    scheduleFab();
  }

  /* ---------- events ---------- */

  document.addEventListener('click', function (e) {
    var t = e.target;
    if (!t || !t.closest) { return; }
    if (t.closest('[data-theme-toggle]')) { toggleTheme(); return; }
    var copy = t.closest('[data-copy]');
    if (copy) { copyFrom(copy); return; }
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) { return; }
    var a = t.closest('a[href]');
    if (!a || skipBoost(a)) { return; }
    var u = new URL(a.href, window.location.href);
    if (u.pathname === window.location.pathname && u.search === window.location.search && u.hash) { return; }
    e.preventDefault();
    navigate(u.href, true);
  });

  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || form.tagName !== 'FORM' || e.defaultPrevented) { return; }
    if ((form.getAttribute('method') || 'get').toLowerCase() !== 'get' || form.hasAttribute('data-no-boost')) { return; }
    var action = new URL(form.getAttribute('action') || window.location.pathname, window.location.href);
    if (action.origin !== window.location.origin) { return; }
    e.preventDefault();
    var params = new URLSearchParams();
    new FormData(form).forEach(function (value, key) {
      if (typeof value === 'string' && value !== '') { params.append(key, value); }
    });
    action.search = params.toString();
    navigate(action.href, true);
  });

  window.addEventListener('popstate', function () { navigate(window.location.href, false); });

  document.addEventListener('keydown', function (e) {
    if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) { return; }
    var tag = (e.target && e.target.tagName) || '';
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') { return; }
    var box = document.querySelector('[data-search-input]');
    if (box) { e.preventDefault(); box.focus(); box.select(); }
  });

  init(content);
  initFab();
})();
