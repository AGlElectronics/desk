import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getBrand, type ApiBrand } from '@/api/brand';
import { brandCss } from '@/lib/moduleBrand';

export type Brand = Required<Pick<ApiBrand, 'productName'>> & ApiBrand;

type Ctx = {
  brand: Brand;
  loading: boolean;
};

// Fallback brand so the login screen renders even before the /api/v1/brand
// response arrives (or if the backend is briefly down). The API response
// takes precedence once it lands.
const fallback: Brand = {
  revision: 0,
  source: 'standalone',
  productName: (import.meta.env.VITE_PRODUCT_NAME as string | undefined) || 'Desk',
  organizationName: '',
  loginSubtitle: 'Support inboxes & tickets',
  loginButtonLabel: 'Continue with your organisation login',
  logoUrl: '',
  themeColor: '#18181b',
  colors: {},
  cssVariables: {},
};

const BrandCtx = createContext<Ctx>({ brand: fallback, loading: true });

/**
 * Loads the brand and refetches it every minute and on tab focus, so a change
 * in Den (when paired) shows up without a rebuild or reload.
 */
export function BrandProvider({ children }: { children: ReactNode }) {
  const q = useQuery({
    queryKey: ['module-brand', '/api/v1/brand'],
    queryFn: ({ signal }) => getBrand(signal),
    staleTime: 30_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
    retry: 1,
  });

  const brand = useMemo<Brand>(
    () => (q.data ? { ...fallback, ...q.data, productName: q.data.productName || fallback.productName } : fallback),
    [q.data],
  );

  useEffect(() => {
    let el = document.getElementById('brand') as HTMLStyleElement | null;
    if (!el) {
      el = document.createElement('style');
      el.id = 'brand';
      document.head.appendChild(el);
    }
    el.textContent = brandCss(brand.theme ?? {});
    const root = document.documentElement;
    for (const [name, value] of Object.entries(brand.cssVariables ?? {})) {
      root.style.setProperty(name, value);
    }
    const org = brand.organizationName?.trim();
    document.title = org ? `${brand.productName} · ${org}` : brand.productName;
  }, [brand]);

  const value = useMemo(() => ({ brand, loading: q.isLoading }), [brand, q.isLoading]);
  return <BrandCtx.Provider value={value}>{children}</BrandCtx.Provider>;
}

export function useBrand(): Ctx {
  return useContext(BrandCtx);
}
