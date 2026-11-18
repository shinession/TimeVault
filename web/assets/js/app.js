(function () {
  'use strict';

  var content = document.getElementById('tv-content');
  if (!content) { return; }
  // The site may be served under a path prefix behind a reverse proxy; the page says which (see basepath.go).
  var baseMeta = document.querySelector('meta[name="tv-base"]');
  var BASE = baseMeta ? (baseMeta.getAttribute('content') || '') : '';
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
    setNotice(res.headers.get('X-TV-Notice'));
    if (push) { history.pushState({ tv: 1 }, '', res.url); }
    syncReturnLinks(res.url);
    init(content);
  }

  // The sentence under the header (a new version is about to be installed) is part of the page, not of the content
  // that is swapped, so every answer says what it should be now.
  function setNotice(encoded) {
    var el = document.querySelector('[data-tv-notice]');
    if (!el) { return; }
    var text = '';
    try { text = encoded ? decodeURIComponent(encoded) : ''; } catch (e) { text = ''; }
    el.textContent = text;
    el.hidden = text === '';
  }

  function fetchPartial(url) {
    return fetch(url, { credentials: 'same-origin', headers: { 'X-TV-Partial': '1' } }).then(function (res) {
      if (new URL(res.url).pathname === BASE + '/login') {
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
    var path = u.pathname;
    if (BASE) {
      // Another site on the same host is not one of our pages.
      if (path !== BASE && path.indexOf(BASE + '/') !== 0) { return true; }
      path = path.slice(BASE.length) || '/';
    }
    return path.indexOf('/static/') === 0 || path === '/lang' || path === '/logout';
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
        return fetch(BASE + '/api/v1/jobs/' + encodeURIComponent(id), { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
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

  /* ---------- single sign-on ---------- */

  var ssoRun = null;

  function ssoShow(box, key) {
    var out = box.querySelector('[data-sso-status]');
    if (!out) { return; }
    var text = box.getAttribute('data-msg-' + key) || '';
    out.textContent = text;
    out.classList.toggle('tv-alert-error', key !== 'waiting');
    out.classList.toggle('tv-alert-info', key === 'waiting');
    out.hidden = text === '';
  }

  function ssoPost(url, body) {
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      cache: 'no-store',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body
    }).then(function (res) {
      return res.json().then(function (data) { return { status: res.status, data: data || {} }; });
    });
  }

  function startSso(button) {
    var box = button.closest('[data-sso-box]');
    if (!box || ssoRun) { return; }
    // The browser only allows a window that the click itself opens; its address is set once the server has
    // issued the token for this sign-in.
    var popup = window.open('', 'tv-sso', 'width=520,height=640');
    var run = ssoRun = { stopped: false, timer: 0 };
    button.disabled = true;
    ssoShow(box, 'waiting');

    function finish(key) {
      run.stopped = true;
      clearTimeout(run.timer);
      ssoRun = null;
      button.disabled = false;
      if (popup && !popup.closed) { try { popup.close(); } catch (e) { /* already gone */ } }
      ssoShow(box, key);
    }

    ssoPost(box.getAttribute('data-start'), 'next=' + encodeURIComponent(box.getAttribute('data-next') || '/')).then(function (r) {
      if (r.status !== 200 || !r.data.token) { finish(r.status === 429 ? 'busy' : 'error'); return; }
      if (!popup) { finish('popup'); return; }
      popup.location.href = r.data.url;
      var token = r.data.token;
      var limit = Math.ceil((r.data.expires_in || 300) / 2);
      var tries = 0;
      var closedLooks = 0;
      (function look() {
        if (run.stopped) { return; }
        if (++tries > limit) { finish('timeout'); return; }
        // The window closes itself a moment after it has recorded the sign-in, so a closed window is only
        // given up on after a few more looks.
        if (popup.closed && ++closedLooks > 3) { finish('cancelled'); return; }
        ssoPost(box.getAttribute('data-poll'), 'token=' + encodeURIComponent(token)).then(function (p) {
          var status = p.data.status;
          if (status === 'ok' && p.data.next) {
            run.stopped = true;
            window.location.href = p.data.next;
          } else if (status === 'pending') {
            run.timer = setTimeout(look, 2000);
          } else {
            finish(status || 'error');
          }
        }).catch(function () { finish('error'); });
      })();
    }).catch(function () { finish('error'); });
  }

  /* ---------- pictures of the accounts ---------- */

  // The page says where the pictures are (the tv-avatar meta tag, with {id} for the employee ID): an address of the
  // host that keeps them, which the browser reads like any picture. That application sends them as text/plain, which
  // an image element does not mind, and answers a missing picture with 202 and a placeholder of 480x320 pixels, which
  // an image element shows like any other, so a picture of that size is taken for none (the pictures of people are
  // far smaller). A picture is asked for once while the page lives. A missing one is remembered for ten minutes, so
  // that a list of people does not ask again and again, and a host that did not answer for a moment is tried again
  // soon. Only pictures that are about to be seen are asked for.
  var avatarMeta = document.querySelector('meta[name="tv-avatar"]');
  var AVATAR = avatarMeta ? (avatarMeta.getAttribute('content') || '') : '';
  var AVATAR_PLACEHOLDER_WIDTH = 480;
  var AVATAR_PLACEHOLDER_HEIGHT = 320;
  var AVATAR_MISS_MS = 10 * 60 * 1000;
  var AVATAR_PARALLEL = 4;
  var avatarCache = {};
  var avatarQueue = [];
  var avatarBusy = 0;
  var avatarObserver = null;

  function avatarMissed(id) {
    try {
      var at = Number(sessionStorage.getItem('tv-avatar-miss:' + id));
      return at > 0 && Date.now() - at < AVATAR_MISS_MS;
    } catch (e) { return false; }
  }

  function rememberAvatarMiss(id) {
    try { sessionStorage.setItem('tv-avatar-miss:' + id, String(Date.now())); } catch (e) { /* ignore */ }
  }

  // Resolves with the address of the picture when there is one, and with null when there is not.
  function readAvatar(id) {
    return new Promise(function (resolve) {
      var url = AVATAR.replace('{id}', encodeURIComponent(id));
      var img = new Image();
      img.onload = function () {
        var placeholder = img.naturalWidth === AVATAR_PLACEHOLDER_WIDTH && img.naturalHeight === AVATAR_PLACEHOLDER_HEIGHT;
        resolve(img.naturalWidth > 0 && !placeholder ? url : null);
      };
      img.onerror = function () { resolve(null); };
      img.src = url;
    });
  }

  function pumpAvatars() {
    while (avatarBusy < AVATAR_PARALLEL && avatarQueue.length) {
      avatarBusy++;
      avatarQueue.shift()().then(function () { avatarBusy--; pumpAvatars(); });
    }
  }

  function avatarFor(id) {
    if (!avatarCache[id]) {
      avatarCache[id] = avatarMissed(id) ? Promise.resolve(null) : new Promise(function (resolve) {
        avatarQueue.push(function () {
          return readAvatar(id).then(function (url) {
            if (!url) { rememberAvatarMiss(id); }
            resolve(url);
          });
        });
        pumpAvatars();
      });
    }
    return avatarCache[id];
  }

  // The browser has the picture already, the same page asked for it a moment ago, so this does not ask the host again.
  function showAvatar(el, url) {
    var img = document.createElement('img');
    img.alt = '';
    img.decoding = 'async';
    // A picture that cannot be shown leaves the default one, which stays in the element under it.
    img.addEventListener('error', function () { img.remove(); el.classList.remove('has-photo'); });
    img.src = url;
    el.appendChild(img);
    el.classList.add('has-photo');
  }

  function loadAvatar(el) {
    avatarFor(el.getAttribute('data-avatar')).then(function (url) { if (url) { showAvatar(el, url); } });
  }

  function initAvatars(root) {
    if (!AVATAR) { return; }
    var pending = root.querySelectorAll('[data-avatar]:not([data-avatar-seen])');
    if (!pending.length) { return; }
    if (!('IntersectionObserver' in window)) {
      pending.forEach(function (el) { el.setAttribute('data-avatar-seen', '1'); loadAvatar(el); });
      return;
    }
    if (!avatarObserver) {
      avatarObserver = new IntersectionObserver(function (entries) {
        entries.forEach(function (entry) {
          if (!entry.isIntersecting) { return; }
          avatarObserver.unobserve(entry.target);
          loadAvatar(entry.target);
        });
      }, { rootMargin: '160px' });
    }
    pending.forEach(function (el) { el.setAttribute('data-avatar-seen', '1'); avatarObserver.observe(el); });
  }

  /* ---------- access editor (folder settings) ---------- */

  // Each level of the editor lists who has it: "guest", "everyone" and people. A group already includes the people, so
  // choosing one clears and locks the people of that level, and the two groups exclude each other. The server checks
  // the same rules; this only keeps the form from being filled in wrongly, and filters a long list of people.
  function initAccess(root) {
    root.querySelectorAll('[data-access-level]').forEach(function (level) {
      var groups = Array.prototype.slice.call(level.querySelectorAll('[data-access-group]'));
      var people = Array.prototype.slice.call(level.querySelectorAll('[data-access-person]'));
      var count = level.querySelector('[data-access-count]');
      var filter = level.querySelector('[data-access-filter]');

      function sync() {
        var group = groups.filter(function (g) { return g.checked; });
        people.forEach(function (p) {
          if (group.length) { p.checked = false; }
          p.disabled = group.length > 0;
        });
        if (count) {
          var n = group.length ? group.length : people.filter(function (p) { return p.checked; }).length;
          count.textContent = count.textContent.replace(/\d+/, String(n));
        }
      }

      groups.forEach(function (g) {
        g.addEventListener('change', function () {
          if (g.checked) { groups.forEach(function (other) { if (other !== g) { other.checked = false; } }); }
          sync();
        });
      });
      people.forEach(function (p) { p.addEventListener('change', sync); });

      if (filter) {
        filter.hidden = false;
        filter.addEventListener('input', function () {
          var q = filter.value.trim().toLowerCase();
          level.querySelectorAll('[data-person]').forEach(function (row) {
            row.hidden = q !== '' && row.getAttribute('data-person').indexOf(q) < 0;
          });
        });
        // Enter in the box finds somebody; it must not save the form, and the permissions in it, as a text field does.
        filter.addEventListener('keydown', function (e) {
          if (e.key === 'Enter') { e.preventDefault(); }
        });
      }
      sync();
    });
  }

  // A page that opens a form (the settings page after "Edit") brings it into view; navigation resets the scroll
  // position right after the content is swapped in, hence the timeout.
  function initScrollTo(root) {
    var el = root.querySelector('[data-scroll-into-view]');
    if (el && el.scrollIntoView) { setTimeout(function () { el.scrollIntoView({ block: 'start' }); }, 0); }
  }

  function init(root) {
    initSelection(root);
    initAutosubmit(root);
    initTimeline(root);
    initJobs(root);
    initAccess(root);
    initAvatars(root);
    initScrollTo(root);
    scheduleFab();
  }

  /* ---------- events ---------- */

  document.addEventListener('click', function (e) {
    var t = e.target;
    if (!t || !t.closest) { return; }
    if (t.closest('[data-theme-toggle]')) { toggleTheme(); return; }
    var sso = t.closest('[data-sso-login]');
    if (sso) { e.preventDefault(); startSso(sso); return; }
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
  initAvatars(document.querySelector('.tv-header') || content);
  initFab();
})();
