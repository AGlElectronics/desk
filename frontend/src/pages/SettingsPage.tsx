import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { DenConnection } from '@/components/DenConnection';

/**
 * Module settings. The slice only has the Den pairing; organisation-wide
 * settings (name, templates) live in Den once paired.
 */
export function SettingsPage() {
  return (
    <PageContainer>
      <PageHeader
        title="Settings"
        description="Module settings. Only Desk admins can see this page."
      />
      <div className="max-w-2xl">
        <DenConnection base="/api/v1/den" />
      </div>
    </PageContainer>
  );
}
