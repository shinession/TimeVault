/* The guide of a page: a step-by-step tour that dims the page, lights up one part at a time and says what it does or
   what it shows. The steps are the elements of the page that carry data-tour (the server writes the attributes, with the
   title and the text in the language of the page: Page.Tour in internal/server/render.go), so the script has no texts of
   its own except the words of its buttons, which the Guide button hands over in data-tour-* attributes. Nothing is changed
   and nothing is clicked for the visitor. Plain DOM and CSSOM only: the page allows no inline script or style. */
(function () {
  'use strict';

  var content = document.getElementById('tv-content');
  if (!content) { return; }

  var parts = null;      // the elements of the guide while it is open
  var steps = [];
  var index = 0;
  var opener = null;     // the button that started it, which gets the focus back
  var where = '';        // the address of the page that is being explained
  var labels = {};
  var watcher = null;
  var frame = 0;
  var settling = 0;
  var settleNode = null; // the part whose scroll margin is set while the window scrolls to it
  var token = 0;         // tells the answers of an earlier step from those of the current one

  function make(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) { node.className = cls; }
    if (text) { node.textContent = text; }
    return node;
  }

  function visible(node) {
    if (!node.getClientRects().length) { return false; }
    var r = node.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  }

  // What the page marks, first what is in its content and then the header, the footer and the round buttons that every
  // page has. A part that is not shown has no step, which is how a button that only some visitors get is explained only
  // to those who see it.
  function collect() {
    var own = [];
    var rest = [];
    document.querySelectorAll('[data-tour]').forEach(function (node) {
      var text = node.getAttribute('data-tour-text');
      if (!text || !visible(node)) { return; }
      var step = { key: node.getAttribute('data-tour'), el: node, title: node.getAttribute('data-tour-title') || '', text: text };
      (content.contains(node) ? own : rest).push(step);
    });
    return own.concat(rest);
  }

  /* ---------- the parts ---------- */

  function build() {
    var root = make('div', 'tv-tour');
    var shield = make('div', 'tv-tour-shield');
    var spot = make('div', 'tv-tour-spot');
    var pop = make('div', 'tv-tour-pop');
    var close = make('button', 'tv-iconbtn tv-tour-close', '\u00d7');
    var said = make('div', 'tv-tour-said');
    var title = make('h2', 'tv-tour-title');
    var text = make('p', 'tv-tour-text');
    var foot = make('div', 'tv-tour-foot');
    var count = make('span', 'tv-tour-count');
    var back = make('button', 'tv-btn tv-btn-small tv-btn-ghost', labels.back);
    var next = make('button', 'tv-btn tv-btn-small tv-btn-primary');

    title.id = 'tv-tour-title';
    text.id = 'tv-tour-text';
    // The focus stays on the Next button from step to step, so the new step is announced by the page itself.
    said.setAttribute('aria-live', 'polite');
    said.setAttribute('aria-atomic', 'true');
    pop.setAttribute('role', 'dialog');
    pop.setAttribute('aria-modal', 'true');
    pop.setAttribute('aria-label', labels.dialog);
    pop.setAttribute('aria-labelledby', title.id);
    pop.setAttribute('aria-describedby', text.id);
    [close, back, next].forEach(function (b) { b.type = 'button'; });
    close.setAttribute('aria-label', labels.close);
    close.title = labels.close;

    close.addEventListener('click', stop);
    back.addEventListener('click', function () { go(-1); });
    next.addEventListener('click', function () { go(1); });
    // The page under the guide does not react to clicks; a click outside the bubble is not a way out either, so a
    // stray click does not end the tour.
    shield.addEventListener('click', function (e) { e.preventDefault(); });

    said.append(title, text);
    foot.append(count, back, next);
    pop.append(close, said, foot);
    root.append(shield, spot, pop);
    document.body.appendChild(root);
    return { root: root, spot: spot, pop: pop, title: title, text: text, count: count, back: back, next: next, close: close };
  }

  /* ---------- showing a step ---------- */

  function go(by) {
    var to = index + by;
    if (to < 0) { return; }
    if (to >= steps.length) { stop(); return; }
    show(to);
  }

  function show(i, quiet) {
    var step = steps[i];
    index = i;
    // A part that went away since the guide started is left out.
    if (!document.body.contains(step.el) || !visible(step.el)) {
      steps.splice(i, 1);
      if (!steps.length) { stop(); } else { show(Math.min(i, steps.length - 1), quiet); }
      return;
    }
    parts.title.textContent = step.title;
    parts.text.textContent = step.text;
    parts.count.textContent = labels.step.replace('{n}', String(i + 1)).replace('{total}', String(steps.length));
    parts.back.disabled = i === 0;
    parts.next.textContent = i === steps.length - 1 ? labels.done : labels.next;
    var mine = ++token;
    // On a narrow screen the bubble sits at the bottom of the window, over the end of the page: the page gets room
    // below its end, so that any part can be scrolled up above the bubble.
    document.documentElement.style.paddingBottom = docked() ? (parts.pop.offsetHeight + 24) + 'px' : '';
    // A page that refreshed itself keeps the guide where it is: no scrolling, and the focus stays where the visitor put it.
    if (quiet) { place(step.el); return; }
    reveal(step.el, function () {
      if (mine !== token || !parts) { return; }
      place(step.el);
      parts.next.focus({ preventScroll: true });
    });
  }

  // The content of the page was replaced. When it is another page the guide is over; when it is the same page that
  // refreshed itself (the job page does while its job runs), the steps are found again and the guide goes on.
  function replaced() {
    if (!parts) { return; }
    if (window.location.href !== where) { stop(); return; }
    var key = steps[index] ? steps[index].key : '';
    var fresh = collect();
    if (!fresh.length) { stop(); return; }
    var at = fresh.findIndex(function (s) { return s.key === key; });
    steps = fresh;
    show(at < 0 ? Math.min(index, steps.length - 1) : at, true);
  }

  function headerBottom() {
    var header = document.querySelector('.tv-header');
    if (!header || window.getComputedStyle(header).position !== 'sticky') { return 0; }
    return Math.max(0, header.getBoundingClientRect().bottom);
  }

  function docked() { return window.innerWidth <= 640; }

  // The bubble sits at the bottom of the window on a narrow screen, so there the part has to be above it.
  function room() {
    var vh = window.innerHeight;
    return { top: headerBottom() + 8, bottom: vh - 8 - (docked() ? parts.pop.offsetHeight + 8 : 0) };
  }

  // Clears what a scroll in progress left on the page: the frame loop of reveal and the scroll margin it put on the part.
  function unsettle() {
    cancelAnimationFrame(settling);
    if (settleNode) { settleNode.style.scrollMarginTop = ''; settleNode = null; }
  }

  // Scrolls the part into view when it is not, and calls done when the window has stopped moving.
  function reveal(node, done) {
    unsettle();
    var pinned = node.closest('.tv-fab') || (node.closest('.tv-header') && headerBottom() > 0);
    var r = node.getBoundingClientRect();
    var box = room();
    var tall = r.height > (box.bottom - box.top) * 0.8;
    var inView = r.top >= box.top && r.bottom <= box.bottom;
    var partly = r.top < box.bottom - 40 && r.bottom > box.top + 40;
    if (pinned || inView || (tall && partly)) { done(); return; }
    var calm = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    node.style.scrollMarginTop = (box.top + 4) + 'px';
    settleNode = node;
    node.scrollIntoView({ block: tall || docked() ? 'start' : 'center', behavior: calm ? 'auto' : 'smooth' });
    var last = window.scrollY;
    var still = 0;
    var began = Date.now();
    (function look() {
      var y = window.scrollY;
      still = y === last ? still + 1 : 0;
      last = y;
      if (still >= 4 || Date.now() - began > 1000) {
        unsettle();
        done();
      } else {
        settling = requestAnimationFrame(look);
      }
    })();
  }

  // Puts the light on the part and the bubble next to it: under it when there is room, else over it, else at the bottom
  // of the window on top of a part that is taller than the window.
  function place(node) {
    if (!parts) { return; }
    var r = node.getBoundingClientRect();
    var vw = window.innerWidth;
    var vh = window.innerHeight;
    var pad = 6;
    var spot = parts.spot.style;
    var first = !parts.root.classList.contains('is-shown');
    spot.top = (r.top - pad) + 'px';
    spot.left = (r.left - pad) + 'px';
    spot.width = (r.width + 2 * pad) + 'px';
    spot.height = (r.height + 2 * pad) + 'px';

    var pop = parts.pop;
    var small = docked();
    // A fixed part at the bottom corner (the round buttons) cannot be scrolled out from under a bubble at the bottom, so
    // the bubble goes to the top then.
    pop.classList.toggle('is-docked', small);
    pop.classList.toggle('is-top', small && !!node.closest('.tv-fab'));
    if (small) {
      pop.style.top = '';
      pop.style.left = '';
    } else {
      var gap = 14;
      var pw = pop.offsetWidth;
      var ph = pop.offsetHeight;
      var top0 = Math.max(r.top, 0);
      var bottom0 = Math.min(r.bottom, vh);
      var left0 = Math.max(r.left, 0);
      var right0 = Math.min(r.right, vw);
      var top;
      if (vh - bottom0 - pad >= ph + gap) { top = bottom0 + pad + gap; }
      else if (top0 - pad >= ph + gap) { top = top0 - pad - gap - ph; }
      else { top = Math.max(12, vh - ph - 12); }
      var left = left0 + (right0 - left0) / 2 - pw / 2;
      pop.style.top = top + 'px';
      pop.style.left = Math.min(Math.max(left, 12), Math.max(12, vw - pw - 12)) + 'px';
    }
    // The bubble shows (and can take the focus) as soon as it has its place. The light moves from step to step, but it
    // does not fly in from a corner when the guide opens: it only glides from the next frame on.
    if (first) {
      parts.root.classList.add('is-shown');
      requestAnimationFrame(function () { if (parts) { parts.root.classList.add('is-placed'); } });
    }
  }

  function follow() {
    if (frame || !parts) { return; }
    frame = requestAnimationFrame(function () {
      frame = 0;
      if (parts && steps[index]) { place(steps[index].el); }
    });
  }

  /* ---------- keys and the end ---------- */

  function onKey(e) {
    if (!parts) { return; }
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); stop(); return; }
    if (e.key === 'ArrowRight') { e.preventDefault(); e.stopPropagation(); go(1); return; }
    if (e.key === 'ArrowLeft') { e.preventDefault(); e.stopPropagation(); go(-1); return; }
    if (e.key === 'Tab') {
      var items = [parts.close, parts.back, parts.next].filter(function (b) { return !b.disabled; });
      var first = items[0];
      var last = items[items.length - 1];
      var at = document.activeElement;
      if (!parts.pop.contains(at)) { e.preventDefault(); first.focus(); }
      else if (e.shiftKey && at === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && at === last) { e.preventDefault(); first.focus(); }
      return;
    }
    // Enter and the space bar press the button that has the focus; the shortcuts of the page stay out while the guide is open.
    if (e.key !== 'Enter' && e.key !== ' ' && !e.ctrlKey && !e.metaKey && !e.altKey) { e.stopPropagation(); }
  }

  function start(button) {
    if (parts) { return; }
    steps = collect();
    if (!steps.length) { return; }
    opener = button;
    labels = {
      dialog: button.getAttribute('data-tour-dialog') || '',
      next: button.getAttribute('data-tour-next') || '',
      back: button.getAttribute('data-tour-back') || '',
      done: button.getAttribute('data-tour-done') || '',
      close: button.getAttribute('data-tour-close') || '',
      step: button.getAttribute('data-tour-step') || '{n} / {total}'
    };
    parts = build();
    where = window.location.href;
    document.addEventListener('keydown', onKey, true);
    window.addEventListener('resize', follow);
    window.addEventListener('scroll', follow, true);
    window.addEventListener('popstate', stop);
    // The page that was explained is replaced when the visitor goes on, or when the page refreshes itself.
    watcher = new MutationObserver(replaced);
    watcher.observe(content, { childList: true });
    show(0);
  }

  function stop() {
    if (!parts) { return; }
    cancelAnimationFrame(frame);
    unsettle();
    frame = 0;
    token++;
    document.removeEventListener('keydown', onKey, true);
    window.removeEventListener('resize', follow);
    window.removeEventListener('scroll', follow, true);
    window.removeEventListener('popstate', stop);
    if (watcher) { watcher.disconnect(); watcher = null; }
    parts.root.remove();
    parts = null;
    steps = [];
    document.documentElement.style.paddingBottom = '';
    if (opener && document.body.contains(opener)) { opener.focus({ preventScroll: true }); }
    opener = null;
  }

  document.addEventListener('click', function (e) {
    var t = e.target;
    var button = t && t.closest ? t.closest('[data-tour-start]') : null;
    if (!button) { return; }
    e.preventDefault();
    if (parts) { stop(); } else { start(button); }
  });
})();
