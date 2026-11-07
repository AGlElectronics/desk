import { loadRuntimeConfig, renderStartupError } from '@/runtime-config';

const rootEl = document.getElementById('root');
if (!rootEl) {
  throw new Error('#root element missing from index.html');
}

// The app is imported only after /config.json is in, so nothing (OIDC
// client, API client, router) can read a setting before it is known.
loadRuntimeConfig()
  .then(() => import('./app'))
  .then(({ startApp }) => startApp(rootEl))
  .catch((err: unknown) => renderStartupError(rootEl, err));
