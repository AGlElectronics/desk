import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  redirect,
} from '@tanstack/react-router';

import { Layout } from '@/components/Layout';
import type { Folder } from '@/api/desk';
import { ConversationPage } from '@/pages/ConversationPage';
import { InboxPage } from '@/pages/InboxPage';
import { LoginPage } from '@/pages/LoginPage';
import { MailboxesPage } from '@/pages/MailboxesPage';
import { OidcCallbackPage } from '@/pages/OidcCallbackPage';
import { SettingsPage } from '@/pages/SettingsPage';
import { getUserManager, isOidcAuthMode } from '@/auth/oidc';

// In OIDC mode a route behind the Layout checks there is a cached user
// before letting the component mount. If not, we redirect to /login and
// carry the attempted path so the post-login redirect lands there.
async function requireAuth(fullPath: string) {
  if (!isOidcAuthMode()) {
    return; // dev mode: nothing to check
  }
  const mgr = getUserManager();
  if (!mgr) {
    throw redirect({ to: '/login', search: { redirect: fullPath } });
  }
  const user = await mgr.getUser();
  if (!user || user.expired) {
    throw redirect({ to: '/login', search: { redirect: fullPath } });
  }
}

const FOLDERS: Folder[] = ['unassigned', 'mine', 'assigned', 'pending', 'closed', 'spam', 'all'];

export type InboxSearch = { mailbox?: string; folder?: Folder; q?: string };

const rootRoute = createRootRoute({
  component: () => <Outlet />,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
  validateSearch: (search: Record<string, unknown>) => ({
    redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
  }),
});

const authCallbackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/auth/callback',
  component: OidcCallbackPage,
});

const appLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  component: Layout,
});

const indexRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/',
  beforeLoad: async () => {
    await requireAuth('/inbox');
    throw redirect({ to: '/inbox', search: { folder: 'unassigned' } });
  },
});

export const inboxRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/inbox',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  validateSearch: (search: Record<string, unknown>): InboxSearch => ({
    mailbox: typeof search.mailbox === 'string' ? search.mailbox : undefined,
    folder: FOLDERS.includes(search.folder as Folder) ? (search.folder as Folder) : undefined,
    q: typeof search.q === 'string' && search.q ? search.q : undefined,
  }),
  component: InboxPage,
});

export const conversationRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/conversations/$conversationId',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: ConversationPage,
});

const mailboxesRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/mailboxes',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: MailboxesPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/settings',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: SettingsPage,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  authCallbackRoute,
  appLayoutRoute.addChildren([indexRoute, inboxRoute, conversationRoute, mailboxesRoute, settingsRoute]),
]);

export const router = createRouter({ routeTree });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
