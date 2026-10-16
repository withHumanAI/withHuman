import { Link } from "@tanstack/react-router";
import { Avatar } from "@/ds/components/avatar";
import {
  Dropdown,
  DropdownContent,
  DropdownGroup,
  DropdownGroupLabel,
  DropdownItem,
  DropdownLinkItem,
  DropdownRadioGroup,
  DropdownRadioItem,
  DropdownTrigger,
} from "@/ds/components/dropdown";
import {
  AlertIcon,
  ChevronUpDownIcon,
  MonitorIcon,
  MoonIcon,
  PersonIcon,
  SettingsIcon,
  SignOutIcon,
  SunIcon,
} from "@/ds/icons";
import { DiscordIcon } from "@/ds/icons/brands";
import { cn } from "@/ds/lib/cn";
import { focusRing } from "@/ds/lib/focus";
import type { ThemePreference } from "@/ds/theme/theme";
import { useTheme } from "@/ds/theme/theme-provider";
import { useSession } from "./principal";

const DISCORD_URL = "https://discord.gg/fwSryh9p8";

/*
 * The sidebar's account row and its menu. The row is the only thing
 * the footer shows: who is signed in, and where. Its menu reads top to
 * bottom as the identity, the pages about you, the community link,
 * the theme, and sign out on its own so it cannot be mistaken for a
 * page.
 */
export function AccountMenu({ onSignOut }: { onSignOut: () => void }) {
  const session = useSession();
  const { theme, setTheme } = useTheme();
  const name = session.user?.display_name ?? session.organization?.name ?? "Account";
  const detail = session.user
    ? (session.organization?.name ?? session.user.email)
    : undefined;

  return (
    <Dropdown>
      <DropdownTrigger
        aria-label="Account menu"
        className={cn(
          "flex w-full min-w-0 cursor-pointer items-center gap-2.5 rounded-sm px-2 py-1.5",
          "text-left select-none",
          "transition-colors duration-100 hover:bg-fill-hover data-popup-open:bg-fill-active",
          focusRing,
        )}
      >
        <Avatar name={name} tooltip={false} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-label font-medium text-ink">{name}</span>
          {detail ? (
            <span className="truncate text-micro font-normal text-ink-faint">{detail}</span>
          ) : null}
        </span>
        <ChevronUpDownIcon width={14} height={14} className="shrink-0 text-ink-faint" />
      </DropdownTrigger>
      <DropdownContent side="right" align="end" className="w-[232px]">
        <DropdownGroup>
          <div className="flex items-center gap-2.5 px-2 pt-1.5 pb-2">
            <Avatar name={name} size="lg" tooltip={false} />
            <div className="flex min-w-0 flex-col">
              <span className="truncate text-body leading-[18px] font-medium text-ink">
                {name}
              </span>
              {detail ? (
                <span className="truncate text-micro font-normal text-ink-faint">
                  {detail}
                </span>
              ) : null}
            </div>
          </div>
        </DropdownGroup>
        <DropdownGroup>
          <DropdownLinkItem
            render={<Link to="/settings" />}
            icon={<SettingsIcon width={15} height={15} />}
          >
            Settings
          </DropdownLinkItem>
          <DropdownLinkItem
            render={<Link to="/settings/account" />}
            icon={<PersonIcon width={15} height={15} />}
          >
            Account
          </DropdownLinkItem>
          <DropdownLinkItem
            render={<Link to="/settings/notifications" />}
            icon={<AlertIcon width={15} height={15} />}
          >
            Notifications
          </DropdownLinkItem>
        </DropdownGroup>
        <DropdownGroup>
          <DropdownLinkItem
            render={<a href={DISCORD_URL} target="_blank" rel="noreferrer" />}
            icon={<DiscordIcon width={15} height={15} />}
          >
            Join our Discord
          </DropdownLinkItem>
        </DropdownGroup>
        <DropdownGroup>
          <DropdownGroupLabel>Theme</DropdownGroupLabel>
          <DropdownRadioGroup
            aria-label="Color theme"
            value={theme}
            onValueChange={(value) => setTheme(value as ThemePreference)}
          >
            <DropdownRadioItem value="light">
              <SunIcon width={14} height={14} />
              Light
            </DropdownRadioItem>
            <DropdownRadioItem value="system">
              <MonitorIcon width={14} height={14} />
              System
            </DropdownRadioItem>
            <DropdownRadioItem value="dark">
              <MoonIcon width={14} height={14} />
              Dark
            </DropdownRadioItem>
          </DropdownRadioGroup>
        </DropdownGroup>
        <DropdownGroup>
          <DropdownItem icon={<SignOutIcon width={15} height={15} />} onClick={onSignOut}>
            Sign out
          </DropdownItem>
        </DropdownGroup>
      </DropdownContent>
    </Dropdown>
  );
}
