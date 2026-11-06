import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';

import '@fontsource-variable/inter';
import '@fontsource-variable/montserrat';
import './styles.css';

import { BrandProvider } from '@/brand/BrandProvider';
import { installAuth } from '@/auth/installAuth';
import { queryClient } from '@/lib/queryClient';
import { router } from '@/router';

// Wire the API client's auth header pipeline (dev header OR oidc bearer)
// before the first query fires. Must happen BEFORE RouterProvider mounts,
// because routes with beforeLoad touch the auth state immediately.
installAuth();

const rootEl = document.getElementById('root');
if (!rootEl) {
  throw new Error('#root element missing from index.html');
}

createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrandProvider>
        <RouterProvider router={router} />
      </BrandProvider>
    </QueryClientProvider>
  </StrictMode>
);
