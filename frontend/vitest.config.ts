import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';

// `npm test`: unit and component tests in jsdom, with the same `@/` alias as
// rsbuild.config.ts.
export default defineConfig({
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  esbuild: { jsx: 'automatic' },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
