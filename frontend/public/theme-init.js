// Apply the cached light/dark choice before first paint (see src/lib/theme.ts).
// A file, not an inline script: the Content Security Policy allows scripts
// from this origin only (script-src 'self').
/* global window, document, localStorage */
(function () {
  var p = 'system';
  try {
    p = localStorage.getItem('cs-theme') || localStorage.getItem('desk-theme') || 'system';
  } catch {
    // Storage blocked: follow the system setting.
  }
  var dark = p === 'dark' || (p !== 'light' && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.classList.toggle('dark', dark);
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
})();
