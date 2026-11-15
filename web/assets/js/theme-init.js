/* Applies the stored light/dark choice before the first paint to avoid a flash. */
(function () {
  try {
    var theme = localStorage.getItem('tv-theme');
    if (theme === 'light' || theme === 'dark') {
      document.documentElement.setAttribute('data-theme', theme);
    }
  } catch (e) { /* storage may be blocked; the automatic theme is fine */ }
})();
