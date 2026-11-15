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

  function loadMore(link) {
    link.classList.add('is-busy');
    fetchPartial(link.href).then(function (r) {
      if (!r) { return; }
      var doc = new DOMParser().parseFromString(r.html, 'text/html');
      var incoming = doc.getElementById('list-rows');
      var target = document.getElementById('list-rows');
      if (incoming && target) {
        while (incoming.firstChild) { target.appendChild(incoming.firstChild); }
      }
      var next = doc.querySelector('a[data-load-more]');
      if (next) { link.setAttribute('href', next.getAttribute('href')); link.classList.remove('is-busy'); } else { link.remove(); }
      var list = target && target.closest('[data-sortable]');
      if (list) { sortRows(list); }
      var form = target && target.closest('form[data-selection-form]');
      if (form) { form.dispatchEvent(new Event('change', { bubbles: true })); }
    }).catch(function () { window.location.assign(link.href); });
  }

  function sortRows(list, key, dir) {
    var host = list.querySelector('#list-rows');
    if (!host) { return; }
    key = key || list.getAttribute('data-sort-key') || 'name';
    dir = dir || Number(list.getAttribute('data-sort-dir')) || 1;
    list.setAttribute('data-sort-key', key);
    list.setAttribute('data-sort-dir', String(dir));
    var rows = Array.prototype.slice.call(host.children);
    rows.sort(function (a, b) {
      var da = a.getAttribute('data-dir') === '1';
      var db = b.getAttribute('data-dir') === '1';
      if (da !== db) { return da ? -1 : 1; }
      var va = a.getAttribute('data-' + key) || '';
      var vb = b.getAttribute('data-' + key) || '';
      var cmp;
      if (key === 'name') { cmp = va.localeCompare(vb, undefined, { numeric: true, sensitivity: 'base' }); }
      else { cmp = (Number(va) || 0) - (Number(vb) || 0); }
      return cmp * dir;
    });
    rows.forEach(function (row) { host.appendChild(row); });
    list.querySelectorAll('[data-sort]').forEach(function (btn) {
      if (btn.getAttribute('data-sort') === key) { btn.setAttribute('aria-sort', dir === 1 ? 'ascending' : 'descending'); }
      else { btn.removeAttribute('aria-sort'); }
    });
  }

  function initSortable(root) {
    root.querySelectorAll('[data-sortable]').forEach(function (list) {
      sortRows(list, 'name', 1);
      list.querySelectorAll('[data-sort]').forEach(function (btn) {
        btn.addEventListener('click', function () {
          var key = btn.getAttribute('data-sort');
          var same = list.getAttribute('data-sort-key') === key;
          var dir = same ? -Number(list.getAttribute('data-sort-dir')) : 1;
          sortRows(list, key, dir);
        });
      });
    });
  }

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
        // loadMore() dispatches a synthetic change on the form itself after it appends rows.
        if (e.target === form || (e.target && e.target.matches && e.target.matches('[data-select-item]'))) { update(); }
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

  function init(root) {
    initSortable(root);
    initSelection(root);
    initAutosubmit(root);
    initTimeline(root);
    initJobs(root);
  }

  /* ---------- events ---------- */

  document.addEventListener('click', function (e) {
    var t = e.target;
    if (!t || !t.closest) { return; }
    if (t.closest('[data-theme-toggle]')) { toggleTheme(); return; }
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) { return; }
    var more = t.closest('a[data-load-more]');
    if (more) { e.preventDefault(); loadMore(more); return; }
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
})();
