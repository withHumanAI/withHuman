import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { DocsLink } from "@/ds/components/docs-link";
import {
  CommandPalette,
  useCommandK,
  type Command,
} from "@/ds/components/command";
import { IconButton } from "@/ds/components/icon-button";
import { Kbd } from "@/ds/components/kbd";
import { Logo } from "@/ds/components/logo";
import { SidebarContext } from "@/ds/components/sidebar-reopen";
import { Tooltip } from "@/ds/components/tooltip";
import { cn } from "@/ds/lib/cn";
import { focusRing } from "@/ds/lib/focus";
import { useTheme } from "@/ds/theme/theme-provider";
import {
  AgentIcon,
  ArrowLeftIcon,
  ArrowUpRightIcon,
  InboxIcon,
  AlertIcon,
  MonitorIcon,
  MoonIcon,
  PanelLeftIcon,
  PeopleIcon,
  PlusIcon,
  ProgressIcon,
  ShieldIcon,
  SettingsIcon,
  SunIcon,
} from "@/ds/icons";
import { api } from "../api";
import { AccountMenu } from "./account-menu";
import { docsForPath } from "./docs";
import {
  ExtensionBanners,
  ExtensionNav,
  extensionLink,
  useExtensionCommands,
  useExtensionNav,
  useTodos,
} from "./extensions";
import { NavGroupLabel, NavItem } from "./nav-item";
import { useAccess } from "./authorization";
import { useNotifiedPendingCount } from "./queue-store";
import { SettingsNav } from "./settings-nav";
import { TodoBell } from "./todo-bell";
import { useRuntimeConfig } from "./use-runtime-config";

const SIDEBAR_KEY = "wh-sidebar";
const MIN_WIDTH = 184;
const MAX_WIDTH = 320;
const DEFAULT_WIDTH = 224;
/* Dragging the handle past this collapses the sidebar. */
const COLLAPSE_AT = 140;

interface SidebarState {
  width: number;
  collapsed: boolean;
}

function readSidebarState(): SidebarState {
  try {
    const raw = localStorage.getItem(SIDEBAR_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<SidebarState>;
      return {
        width: Math.min(
          MAX_WIDTH,
          Math.max(MIN_WIDTH, parsed.width ?? DEFAULT_WIDTH),
        ),
        collapsed: parsed.collapsed === true,
      };
    }
  } catch {
    /* storage unavailable */
  }
  return { width: DEFAULT_WIDTH, collapsed: false };
}

export function Shell({
  onSignOut,
  children,
}: {
  onSignOut: () => void;
  children: ReactNode;
}) {
  const navigate = useNavigate();
  /* Navigation gates on "holds anywhere": a page opens when the person
   * can read it at some scope, and the page narrows its own actions. */
  const { holdsAnywhere } = useAccess();
  const canReadOrganization = holdsAnywhere("organization.read");
  const canReadMembers = holdsAnywhere("membership.read");
  const canReadAgents = holdsAnywhere("agent.read");
  const canConnectAgents = holdsAnywhere("agent.write");
  const canReadConnections = holdsAnywhere("connection.read");
  const canReadPipelines = holdsAnywhere("pipeline.read");
  const canReadPaths = holdsAnywhere("escalation_path.read");
  const canReadTeams = holdsAnywhere("team.read");
  const canSeeWebhooks = holdsAnywhere("webhook.read") || canReadPipelines;
  /* Runtime config says what this deployment has: webhook endpoints
   * need the secrets key, and without it the page would only refuse. */
  const { config: runtimeConfig } = useRuntimeConfig();
  const webhookEnabled = runtimeConfig?.webhook_enabled ?? false;
  /* How many requests sent to this person still wait on them, live
   * from the queue's notified layer, so the sidebar says it on every
   * page. */
  const notifiedPending = useNotifiedPendingCount();
  /* What the edition adds to the sidebar and the palette. */
  const extensionNav = {
    records: useExtensionNav("records"),
    agents: useExtensionNav("agents"),
    policy: useExtensionNav("policy"),
  };
  const extensionCommands = useExtensionCommands();
  /* What the person should do, under the bell in the sidebar header;
   * null when no edition can ever list anything, and then no bell. */
  const todos = useTodos();
  const attention = todos !== null && todos.length > 0;
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [sidebar, setSidebar] = useState<SidebarState>(readSidebarState);
  const [dragging, setDragging] = useState(false);
  const { resolvedTheme, setTheme } = useTheme();

  /* Under /settings the sidebar shows the settings navigation instead
   * of the workspace one, with a way back to wherever the person was.
   * The last workspace location is remembered so "Back to app" returns
   * to the open request or filtered queue rather than a default. */
  const location = useLocation();
  const docs = docsForPath(location.pathname, location.search);
  const inSettings = location.pathname.startsWith("/settings");
  const backHref = useRef("/queue");
  if (!inSettings) {
    backHref.current = location.href;
  }

  useEffect(() => {
    try {
      localStorage.setItem(SIDEBAR_KEY, JSON.stringify(sidebar));
    } catch {
      /* storage unavailable */
    }
  }, [sidebar]);

  /* A screen that wants the pane to itself (the guided setup) holds the
   * sidebar closed for as long as it is mounted. The hold sits on top of
   * the stored preference and is never persisted, so a reload or the
   * next screen finds the sidebar as the person left it. Showing the
   * sidebar while held releases the hold: the person has spoken. */
  const [held, setHeld] = useState(false);
  const collapsed = sidebar.collapsed || held;
  const toggleSidebar = useCallback(() => {
    if (held) {
      setHeld(false);
      setSidebar((current) => ({ ...current, collapsed: false }));
      return;
    }
    setSidebar((current) => ({ ...current, collapsed: !current.collapsed }));
  }, [held]);
  /* Show it, whatever state it is in: the reopen control, and a screen
   * handing the pane back (the guided setup on its way out). */
  const showSidebar = useCallback(() => {
    setHeld(false);
    setSidebar((current) => (current.collapsed ? { ...current, collapsed: false } : current));
  }, []);
  /* True while a layout is placing the reopen control on its own title
   * line; otherwise the shell floats it at the pane's corner. */
  const [controlClaimed, setControlClaimed] = useState(false);
  const sidebarHandle = useMemo(
    () => ({ collapsed, reopen: showSidebar, hold: setHeld, claim: setControlClaimed, attention }),
    [collapsed, showSidebar, attention],
  );

  useCommandK(useCallback(() => setPaletteOpen((open) => !open), []));

  /* The [ key toggles the sidebar, as long as nothing editable has
   * focus. */
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== "[" || event.metaKey || event.ctrlKey || event.altKey) {
        return;
      }
      const target = event.target as HTMLElement | null;
      if (
        target &&
        (target.isContentEditable ||
          ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName))
      ) {
        return;
      }
      event.preventDefault();
      toggleSidebar();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [toggleSidebar]);

  const dragRef = useRef<{ pointerId: number } | null>(null);

  function onHandlePointerDown(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = { pointerId: event.pointerId };
    setDragging(true);
  }

  function onHandlePointerMove(event: ReactPointerEvent<HTMLDivElement>) {
    if (!dragRef.current) return;
    const x = event.clientX;
    if (x < COLLAPSE_AT) {
      setSidebar((current) => ({ ...current, collapsed: true }));
      return;
    }
    setSidebar({
      collapsed: false,
      width: Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, x)),
    });
  }

  function onHandlePointerUp() {
    dragRef.current = null;
    setDragging(false);
  }

  const commands = useMemo<Command[]>(
    () => [
      {
        id: "go-queue",
        label: "Go to review queue",
        group: "Navigate",
        icon: <InboxIcon width={15} height={15} />,
        action: () => void navigate({ to: "/queue" }),
      },
      ...(canReadAgents
        ? [
            {
              id: "go-agents",
              label: "Go to agents",
              group: "Navigate",
              icon: <AgentIcon width={15} height={15} />,
              action: () => void navigate({ to: "/agents" }),
            } satisfies Command,
          ]
        : []),
      ...(canConnectAgents
        ? [
            {
              id: "go-connect",
              label: "Connect an agent",
              group: "Navigate",
              icon: <PlusIcon width={15} height={15} />,
              action: () => void navigate({ to: "/connect" }),
            } satisfies Command,
            {
              id: "go-welcome",
              label: "Set up an agent on this machine",
              group: "Navigate",
              icon: <AgentIcon width={15} height={15} />,
              action: () => void navigate({ to: "/welcome" }),
            } satisfies Command,
          ]
        : []),
      ...(canReadOrganization
        ? [
            {
              id: "go-organization",
              label: "Go to organization settings",
              group: "Navigate",
              icon: <SettingsIcon width={15} height={15} />,
              action: () => void navigate({ to: "/settings/organization" }),
            } satisfies Command,
          ]
        : []),
      ...(canReadMembers
        ? [
            {
              id: "go-members",
              label: "Go to members",
              group: "Navigate",
              icon: <PeopleIcon width={15} height={15} />,
              action: () => void navigate({ to: "/settings/members" }),
            } satisfies Command,
          ]
        : []),
      ...(canReadTeams
        ? [
            {
              id: "go-teams",
              label: "Go to teams",
              group: "Navigate",
              icon: <PeopleIcon width={15} height={15} />,
              action: () => void navigate({ to: "/settings/teams" }),
            } satisfies Command,
          ]
        : []),
      ...(canReadConnections
        ? [
            {
              id: "go-integrations",
              label: "Go to integrations",
              group: "Navigate",
              icon: <ShieldIcon width={15} height={15} />,
              action: () => void navigate({ to: "/settings/integrations" }),
            } satisfies Command,
          ]
        : []),
      ...(canReadPipelines
        ? [
            {
              id: "go-pipelines",
              label: "Manage approval pipelines",
              group: "Navigate",
              icon: <ProgressIcon width={15} height={15} />,
              action: () => void navigate({ to: "/pipelines" }),
            } satisfies Command,
          ]
        : []),
      ...(canReadPaths
        ? [
            {
              id: "go-escalation-paths",
              label: "Manage escalation paths",
              group: "Navigate",
              icon: <ArrowUpRightIcon width={15} height={15} />,
              action: () => void navigate({ to: "/escalation-paths" }),
            } satisfies Command,
          ]
        : []),
      ...(canSeeWebhooks && webhookEnabled
        ? [
            {
              id: "go-webhooks",
              label: "Manage webhook endpoints",
              group: "Navigate",
              icon: <MonitorIcon width={15} height={15} />,
              action: () => void navigate({ to: "/webhooks" }),
            } satisfies Command,
          ]
        : []),
      ...extensionCommands.map(
        (entry) =>
          ({
            id: `go-${entry.id}`,
            label: entry.command ?? entry.label,
            group: "Navigate",
            icon: "icon" in entry ? entry.icon : <SettingsIcon width={15} height={15} />,
            action: () => void navigate(extensionLink(entry)),
          }) satisfies Command,
      ),
      {
        id: "go-notifications",
        label: "Go to notifications",
        group: "Navigate",
        icon: <AlertIcon width={15} height={15} />,
        action: () => void navigate({ to: "/settings/notifications" }),
      },
      {
        id: "go-settings",
        label: "Go to settings",
        group: "Navigate",
        icon: <SettingsIcon width={15} height={15} />,
        action: () => void navigate({ to: "/settings" }),
      },
      {
        id: "toggle-sidebar",
        label: collapsed ? "Show sidebar" : "Hide sidebar",
        group: "Workspace",
        keys: "[",
        icon: <PanelLeftIcon width={15} height={15} />,
        action: toggleSidebar,
      },
      {
        id: "toggle-theme",
        label:
          resolvedTheme === "dark"
            ? "Switch to light theme"
            : "Switch to dark theme",
        group: "Workspace",
        icon:
          resolvedTheme === "dark" ? (
            <SunIcon width={15} height={15} />
          ) : (
            <MoonIcon width={15} height={15} />
          ),
        action: () => setTheme(resolvedTheme === "dark" ? "light" : "dark"),
      },
      {
        id: "sign-out",
        label: "Sign out",
        group: "Workspace",
        action: onSignOut,
      },
    ],
    [
      canReadOrganization,
      canReadAgents,
      canReadConnections,
      canConnectAgents,
      canReadMembers,
      canReadPipelines,
      canReadPaths,
      canReadTeams,
      extensionCommands,
      canSeeWebhooks,
      webhookEnabled,
      navigate,
      onSignOut,
      resolvedTheme,
      setTheme,
      collapsed,
      toggleSidebar,
    ],
  );

  return (
    <SidebarContext.Provider value={sidebarHandle}>
      <div className={cn("ds-root flex h-screen", dragging && "select-none")}>
        <aside
          style={{ width: collapsed ? 0 : sidebar.width }}
          className={cn(
            "relative flex shrink-0 flex-col overflow-hidden bg-canvas",
            !collapsed && "border-r-hairline border-edge",
            !dragging && "transition-[width] duration-160 ease-standard",
          )}
        >
          <div
            className="flex h-12 items-center justify-between pr-3 pl-4"
            style={{ minWidth: MIN_WIDTH }}
          >
            {inSettings ? (
              <button
                type="button"
                onClick={() => void navigate({ href: backHref.current })}
                className={cn(
                  "-ml-2 flex h-7 cursor-pointer items-center gap-1.5 rounded-sm px-2",
                  "text-label text-ink-secondary select-none",
                  "transition-colors duration-100 hover:bg-fill-hover hover:text-ink",
                  focusRing,
                )}
              >
                <ArrowLeftIcon width={14} height={14} />
                Back to app
              </button>
            ) : (
              <Link to="/queue" className={cn("flex rounded-sm", focusRing)}>
                <Logo className="text-[13px] text-ink" />
              </Link>
            )}
            <div className="flex items-center gap-0.5">
              {todos !== null ? <TodoBell items={todos} /> : null}
              <Tooltip content="Hide sidebar" keys="[">
                <IconButton aria-label="Hide sidebar" size="sm" onClick={toggleSidebar}>
                  <PanelLeftIcon width={15} height={15} />
                </IconButton>
              </Tooltip>
            </div>
          </div>
          <nav
            className="flex flex-col gap-0.5 px-2 pt-2"
            style={{ minWidth: MIN_WIDTH }}
          >
            {inSettings ? (
              <>
                <h2 className="px-2 pb-1 text-title-sm text-ink">Settings</h2>
                <SettingsNav />
              </>
            ) : (
              <>
                {/* The workspace navigation follows the life of a request:
                 * the records (live queue, permanent audit log), then the
                 * agents that open requests, then the policy that decides
                 * them in execution order. A group heading renders only
                 * when the role can see an entry under it. */}
                <NavItem
                  to="/queue"
                  icon={<InboxIcon width={15} height={15} />}
                  count={notifiedPending}
                  countLabel="pending requests sent to you"
                >
                  Review queue
                </NavItem>
                <ExtensionNav entries={extensionNav.records} />
                {canReadAgents || extensionNav.agents.length > 0 ? (
                  <NavGroupLabel>Agents</NavGroupLabel>
                ) : null}
                {canReadAgents ? (
                  <NavItem to="/agents" icon={<AgentIcon width={15} height={15} />}>
                    Agents
                  </NavItem>
                ) : null}
                <ExtensionNav entries={extensionNav.agents} />
                {canReadPipelines || canReadPaths || (canSeeWebhooks && webhookEnabled) || extensionNav.policy.length > 0 ? (
                  <NavGroupLabel>Policy</NavGroupLabel>
                ) : null}
                {canReadPipelines ? (
                  <NavItem to="/pipelines" icon={<ProgressIcon width={15} height={15} />}>
                    Approval pipelines
                  </NavItem>
                ) : null}
                {canReadPaths ? (
                  <NavItem to="/escalation-paths" icon={<ArrowUpRightIcon width={15} height={15} />}>
                    Escalation paths
                  </NavItem>
                ) : null}
                {canSeeWebhooks && webhookEnabled ? (
                  <NavItem to="/webhooks" icon={<MonitorIcon width={15} height={15} />}>
                    Webhook endpoints
                  </NavItem>
                ) : null}
                <ExtensionNav entries={extensionNav.policy} />
              </>
            )}
          </nav>
          {/* Settings is a mode switch for the sidebar rather than a
           * page in the request flow, so it is reached from the account
           * menu, together with the theme and sign out. */}
          {!collapsed ? (
            <div className="mt-auto px-2 pb-2" style={{ minWidth: MIN_WIDTH }}>
              <DocsLink key={docs.href} {...docs} />
            </div>
          ) : null}
          <div
            className={cn("border-t-hairline border-edge p-2", collapsed && "mt-auto")}
            style={{ minWidth: MIN_WIDTH }}
          >
            <AccountMenu onSignOut={onSignOut} />
          </div>
        </aside>

        {/* Grab handle: drag to resize; drag far left to close. Stays
         * mounted mid-drag so crossing the collapse threshold keeps the
         * pointer capture and dragging back out reopens. */}
        {!collapsed || dragging ? (
          <div
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize sidebar"
            onPointerDown={onHandlePointerDown}
            onPointerMove={onHandlePointerMove}
            onPointerUp={onHandlePointerUp}
            className={cn(
              "-ml-[3px] w-[5px] shrink-0 cursor-col-resize",
              "transition-colors duration-100",
              dragging ? "bg-accent/60" : "hover:bg-edge-strong",
            )}
          />
        ) : null}

        {/* The content pane. While the sidebar is collapsed the reopen
         * control floats at the pane's top-left, centred on the same 48px
         * band as the sidebar header so it lands where the "Hide sidebar"
         * button was, outside the scrolling element so content scrolls
         * underneath it. A layout whose title would sit against that
         * corner claims it through SidebarContext and places it on the
         * title line instead. */}
        <div className="@container/pane relative flex min-w-0 flex-1 flex-col">
          {collapsed && !controlClaimed ? (
            <span className="absolute top-[11px] left-3 z-20">
              <Tooltip content="Show sidebar" keys="[">
                <IconButton
                  aria-label={attention ? "Show sidebar, you have things to do" : "Show sidebar"}
                  variant="neutral"
                  size="sm"
                  attention={attention}
                  onClick={toggleSidebar}
                >
                  <PanelLeftIcon width={15} height={15} />
                </IconButton>
              </Tooltip>
            </span>
          ) : null}
          <main
            data-sidebar={collapsed ? "collapsed" : "open"}
            className="relative min-h-0 min-w-0 flex-1 overflow-y-auto bg-canvas"
          >
            <ExtensionBanners />
            {children}
          </main>
          {collapsed ? (
            <div className="flex shrink-0 justify-end border-t-hairline border-edge bg-canvas px-3 py-1">
              <DocsLink key={docs.href} {...docs} />
            </div>
          ) : null}
        </div>

        <CommandPalette
          open={paletteOpen}
          onOpenChange={setPaletteOpen}
          commands={commands}
        />
      </div>
    </SidebarContext.Provider>
  );
}
