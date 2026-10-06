import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { PlusIcon } from "@/ds/icons";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { ChannelRows, type ChannelsPage } from "./contact-channels";
import { NotificationLadders } from "./notification-rules";
import { useTitle } from "./use-title";

/* Contact methods are self-service, so this page has no permission gate:
 * everything on it acts on the signed-in account. */
export function NotificationsScreen({ page }: { page: ChannelsPage }) {
  useTitle("Notifications");
  const router = useRouter();
  const navigate = useNavigate();
  const { methods, rules, error } = page;

  /* Every change is followed by a reload of the loader's page, so the
   * lists always show what the server holds. */
  const refresh = () => router.invalidate();

  /* The rules are keyed on what the server holds, so a save or a change
   * to the methods starts the ladders from a fresh draft. */
  const rulesKey =
    JSON.stringify(rules) + methods.map((method) => `${method.id}:${method.verified}:${method.available}`).join(",");

  return (
    <SettingsPage
      title="Notifications"
      description="Choose how you want to be notified."
    >
      {error ? (
        <Callout tone="warn" title="Could not load your channels">
          {error}
        </Callout>
      ) : null}

      <SettingsSection
        title="Channels"
        description="Add ways to get in touch with you."
        action={
          <Button
            size="sm"
            variant="neutral"
            onClick={() => void navigate({ to: "/settings/notifications/add" })}
          >
            <PlusIcon />
            Add channel
          </Button>
        }
      >
        <ChannelRows methods={methods} onChanged={refresh} />
      </SettingsSection>

      <NotificationLadders key={rulesKey} rules={rules} methods={methods} onSaved={refresh} />
    </SettingsPage>
  );
}
