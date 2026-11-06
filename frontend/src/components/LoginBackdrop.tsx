// Sign-in screen frame with Den's background picture. Identical in HR, Stash
// and Rally; keep the copies in sync (Den has its own with Den's colours).
import type { ReactNode } from 'react';

export type LoginLook = { image?: string; layout: 'panel' | 'centered'; caption?: string };

/** The sign-in look from Den: picture URL plus copy.login_layout / copy.login_caption. */
export function loginLook(image: string | undefined, layout: unknown, caption: unknown): LoginLook {
  const text = typeof caption === 'string' ? caption.trim() : '';
  return { image: image || undefined, layout: layout === 'centered' ? 'centered' : 'panel', caption: text || undefined };
}

const CAPTION = 'text-center text-white [text-shadow:0_1px_14px_rgb(0_0_0/0.65)]';
const FLAT_CARD = 'lg:[&_.login-card]:border-0 lg:[&_.login-card]:bg-transparent lg:[&_.login-card]:shadow-none';

/**
 * With a background picture the form is docked in a panel on the right
 * ("panel", from 1024px wide) or stays a card centred over the dimmed picture
 * ("centered", and always on narrow screens). Without a picture the children
 * render inside `plain`, the page's usual wrapper. Mark the card with
 * `login-card` so the panel can drop its border.
 */
export function LoginBackdrop({ look, plain, children }: { look: LoginLook; plain: string; children: ReactNode }) {
  if (!look.image) return <div className={plain}>{children}</div>;
  const style = { backgroundImage: `url(${JSON.stringify(look.image)})` };

  if (look.layout === 'centered') {
    return (
      <div className="min-h-screen bg-cover bg-center" style={style}>
        <div className="flex min-h-screen flex-col items-center justify-center gap-6 bg-black/40 px-4 py-10">
          {children}
          {look.caption && <p className={`max-w-xl text-lg ${CAPTION}`}>{look.caption}</p>}
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-cover bg-center" style={style}>
      <div className="flex min-h-screen bg-black/40 lg:grid lg:grid-cols-[minmax(0,1fr)_30rem] lg:bg-transparent">
        <div className="hidden items-center justify-center p-16 lg:flex">
          {look.caption && <p className={`max-w-3xl text-3xl font-light ${CAPTION}`}>{look.caption}</p>}
        </div>
        <div
          className={`flex w-full flex-col items-center justify-center gap-6 px-4 py-10 lg:bg-card lg:px-10 lg:text-card-foreground lg:shadow-[-16px_0_48px_rgb(0_0_0/0.35)] ${FLAT_CARD}`}
        >
          {children}
          {look.caption && <p className={`max-w-xl text-lg lg:hidden ${CAPTION}`}>{look.caption}</p>}
        </div>
      </div>
    </div>
  );
}
