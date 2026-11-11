// Hidden-iframe target for oidc-client-ts silent sign-in / session checks.
// Mirrors oidc-client-ts's _notifyParent message so the app need not load here.
// A file, not an inline script: the Content Security Policy allows scripts
// from this origin only (script-src 'self').
/* global window, parent */
parent.postMessage({ source: 'oidc-client', url: window.location.href, keepOpen: false }, window.location.origin);
