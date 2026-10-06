import { useMemo, useState } from "react";
import { Avatar } from "@/ds/components/avatar";
import { Button } from "@/ds/components/button";
import { IconButton } from "@/ds/components/icon-button";
import {
  Dropdown,
  DropdownContent,
  DropdownGroup,
  DropdownGroupLabel,
  DropdownItem,
  DropdownRadioGroup,
  DropdownRadioItem,
  DropdownTrigger,
} from "@/ds/components/dropdown";
import { Menu, MenuContent, MenuTrigger, type MenuAction } from "@/ds/components/menu";
import {
  ArrowUpRightIcon,
  BellIcon,
  CheckIcon,
  CopyIcon,
  MonitorIcon,
  MoonIcon,
  PersonIcon,
  SettingsIcon,
  SignOutIcon,
  SunIcon,
  TrashIcon,
  XIcon,
} from "@/ds/icons";
import { Section, Demo, DemoRow, KeyboardHint } from "../section";

export function MenusSection() {
  const [lastAction, setLastAction] = useState<string | null>(null);
  const [theme, setTheme] = useState("system");

  const actions = useMemo<MenuAction[]>(
    () => [
      {
        id: "approve",
        label: "Approve",
        group: "decide",
        keys: "A",
        info: "Lets the agent carry on with the action it asked about.",
        docsLinks: [{ href: "https://withhuman.ai/docs/web-app/review-queue#approvals-and-denials", label: "Approvals and denials" }],
        icon: <CheckIcon />,
        action: () => setLastAction("Approve"),
      },
      {
        id: "deny",
        label: "Deny",
        group: "decide",
        keys: "D",
        icon: <XIcon />,
        action: () => setLastAction("Deny"),
      },
      {
        id: "copy-id",
        label: "Copy request id",
        group: "utility",
        keys: "mod+C",
        icon: <CopyIcon />,
        action: () => setLastAction("Copy id"),
      },
      {
        id: "open-temporal",
        label: "Open in Temporal",
        group: "utility",
        icon: <ArrowUpRightIcon />,
        action: () => setLastAction("Open in Temporal"),
      },
      {
        id: "delete",
        label: "Delete request",
        group: "danger",
        danger: true,
        icon: <TrashIcon />,
        action: () => setLastAction("Delete"),
      },
    ],
    [],
  );

  return (
    <Section
      id="menus"
      title="Menu"
      lede="Raised surface, pointer and arrow keys share one highlight, and destructive items carry their own tone. Menu is a list of actions that filters as you type; Dropdown is a fixed composition for menus that carry more than actions."
    >
      <Demo
        label="Request actions"
        hint={lastAction ? `last action: ${lastAction}` : "click to open, try typing"}
      >
        <DemoRow>
          <Menu actions={actions}>
            <MenuTrigger
              render={<Button variant="neutral">Request actions</Button>}
            />
            <MenuContent />
          </Menu>
        </DemoRow>
        <KeyboardHint>
          Arrows move, Enter runs, Esc closes and returns focus, typing
          filters the actions. Tab reaches an info icon; reading its card does not run the action.
        </KeyboardHint>
      </Demo>

      <Demo label="Dropdown" hint={`theme: ${theme}`}>
        <DemoRow>
          <Dropdown>
            <DropdownTrigger render={<Button variant="neutral">Account</Button>} />
            <DropdownContent className="w-[232px]">
              <DropdownGroup>
                <div className="flex items-center gap-2.5 px-2 pt-1.5 pb-2">
                  <Avatar name="Ada Lovelace" size="lg" tooltip={false} />
                  <div className="flex min-w-0 flex-col">
                    <span className="truncate text-body leading-[18px] font-medium text-ink">
                      Ada Lovelace
                    </span>
                    <span className="truncate text-micro font-normal text-ink-faint">
                      Analytical Engines
                    </span>
                  </div>
                </div>
              </DropdownGroup>
              <DropdownGroup>
                <DropdownItem
                  icon={<SettingsIcon width={15} height={15} />}
                  onClick={() => setLastAction("Settings")}
                >
                  Settings
                </DropdownItem>
                <DropdownItem
                  icon={<PersonIcon width={15} height={15} />}
                  onClick={() => setLastAction("Account")}
                >
                  Account
                </DropdownItem>
              </DropdownGroup>
              <DropdownGroup>
                <DropdownGroupLabel>Theme</DropdownGroupLabel>
                <DropdownRadioGroup
                  aria-label="Color theme"
                  value={theme}
                  onValueChange={(value) => setTheme(value as string)}
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
                <DropdownItem
                  icon={<SignOutIcon width={15} height={15} />}
                  onClick={() => setLastAction("Sign out")}
                >
                  Sign out
                </DropdownItem>
              </DropdownGroup>
            </DropdownContent>
          </Dropdown>
        </DemoRow>
        <KeyboardHint>
          Arrows move through entries and theme segments alike, Enter or
          Space chooses, a chosen segment keeps the menu open, Esc closes
          and returns focus.
        </KeyboardHint>
      </Demo>

      <Demo label="Described entries" hint="the to-do bell's list">
        <DemoRow>
          <Dropdown>
            <DropdownTrigger
              render={
                <IconButton aria-label="To do, 2 things" attention>
                  <BellIcon />
                </IconButton>
              }
            />
            <DropdownContent className="w-[300px]">
              <DropdownGroup>
                <DropdownGroupLabel>To do</DropdownGroupLabel>
                <DropdownItem
                  description="Claude Code uses it with your own account."
                  onClick={() => setLastAction("Connect GitHub")}
                >
                  Connect GitHub · Write
                </DropdownItem>
                <DropdownItem
                  description="Claude Code and Codex use it with your own account."
                  onClick={() => setLastAction("Connect Linear")}
                >
                  Connect Linear · Write
                </DropdownItem>
              </DropdownGroup>
            </DropdownContent>
          </Dropdown>
        </DemoRow>
        <KeyboardHint>
          A description wraps under the label instead of truncating, for an
          entry that has to say why it is there.
        </KeyboardHint>
      </Demo>
    </Section>
  );
}
