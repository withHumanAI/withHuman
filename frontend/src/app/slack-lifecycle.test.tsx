// @vitest-environment jsdom
import type { ComponentType, ReactNode } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ContactMethod, SlackConnection } from "../types";
import { IntegrationsScreen } from "./integrations";
import { ladderLines, ladderOptions } from "./notification-rules";

vi.mock("@tanstack/react-router", () => ({
  createLink: (component: ComponentType) => component,
  useRouter: () => ({ invalidate: vi.fn() }),
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
}));
vi.mock("@/ds/components/toast", () => ({ useToast: () => ({ add: vi.fn() }) }));
beforeEach(() => { vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} }); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it("shows a disconnected workspace with reconnect and hides member sync", () => {
  const connection: SlackConnection = {
    status: "disconnected", team_id: "T1", team_name: "Example", bot_user_id: "BOT", scopes: [],
    installed_by_membership_id: "member", installed_at: "2026-09-20T10:00:00Z", linked_members: 0,
    disconnected_at: "2026-09-21T10:00:00Z", disconnect_reason: "app_uninstalled",
  };
  render(<IntegrationsScreen canRead canWrite page={{ slack: { connection, unavailable: false }, discord: { connection: null, unavailable: true } }} />);
  expect(screen.getByText("Disconnected")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Reconnect" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Sync members" })).toBeNull();
  expect(screen.getByText(/Slack notifications are paused/)).toBeTruthy();
});

it("preserves unavailable Slack selections and their saved rule references", () => {
  const methods = [{ id: "slack-id", kind: "slack", value: "U1", label: "Owner", verified: true, available: false } as ContactMethod];
  expect(ladderOptions(methods)).toContainEqual({ id: "slack-id", label: "Slack, Owner (unavailable)" });
  expect(ladderLines([{ delay_seconds: 60, contact_method_id: "slack-id" }], methods)).toEqual(["After 1 minute via Slack, Owner (unavailable)"]);
  expect(ladderOptions([{ ...methods[0], available: true }])).toContainEqual({ id: "slack-id", label: "Slack, Owner" });
});
