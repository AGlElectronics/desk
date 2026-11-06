import { defineConfig } from '@rsbuild/core';
import { pluginReact } from '@rsbuild/plugin-react';

// Rsbuild (like Vite) does not auto-forward process.env into import.meta.env.
// Explicitly map the VITE_* vars Desk reads so Docker/CI must set them
// before `rsbuild build` runs — a typo surfaces at build time, not runtime.
const viteEnv = {
  VITE_AUTH_MODE: process.env.VITE_AUTH_MODE ?? 'dev',
  VITE_OIDC_AUTHORITY: process.env.VITE_OIDC_AUTHORITY ?? '',
  VITE_OIDC_CLIENT_ID: process.env.VITE_OIDC_CLIENT_ID ?? '',
  VITE_OIDC_SCOPE: process.env.VITE_OIDC_SCOPE ?? 'openid profile email',
  VITE_API_BASE_URL: process.env.VITE_API_BASE_URL ?? '',
  VITE_PRODUCT_NAME: process.env.VITE_PRODUCT_NAME ?? 'Desk',
} as const;

export default defineConfig({
  plugins: [pluginReact()],
  source: {
    entry: { index: './src/main.tsx' },
    define: {
      'process.env.DESK_VERSION': JSON.stringify(process.env.DESK_VERSION ?? 'dev'),
      'import.meta.env.VITE_AUTH_MODE': JSON.stringify(viteEnv.VITE_AUTH_MODE),
      'import.meta.env.VITE_OIDC_AUTHORITY': JSON.stringify(viteEnv.VITE_OIDC_AUTHORITY),
      'import.meta.env.VITE_OIDC_CLIENT_ID': JSON.stringify(viteEnv.VITE_OIDC_CLIENT_ID),
      'import.meta.env.VITE_OIDC_SCOPE': JSON.stringify(viteEnv.VITE_OIDC_SCOPE),
      'import.meta.env.VITE_API_BASE_URL': JSON.stringify(viteEnv.VITE_API_BASE_URL),
      'import.meta.env.VITE_PRODUCT_NAME': JSON.stringify(viteEnv.VITE_PRODUCT_NAME),
    },
  },
  resolve: {
    alias: {
      '@': './src',
    },
  },
  html: {
    template: './public/index.html',
    title: 'Desk — Support',
    meta: {
      description: 'Desk shared support inboxes',
      'theme-color': '#18181b',
    },
  },
  server: {
    // Desk frontend runs on 3008 (reserved; Stash 3000, Den 3001,
    // Events 3002, HR 3003).
    port: 3008,
    proxy: {
      '/api': {
        target: process.env.API_PROXY_TARGET ?? 'http://localhost:8150',
        changeOrigin: true,
      },
    },
  },
  output: {
    distPath: { root: 'dist' },
    cleanDistPath: true,
    copy: [
      { from: 'public/brand.json', to: 'brand.json' },
      { from: 'public/manifest.webmanifest', to: 'manifest.webmanifest' },
    ],
  },
});
