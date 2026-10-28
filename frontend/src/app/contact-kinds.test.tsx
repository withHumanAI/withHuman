// @vitest-environment jsdom
import type { ComponentType, ReactNode } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ContactMethod } from "../types";
import { ChannelRows, isChannelKind } from "./contact-channels";
import { contactKind, defineContactKind, registerExtensions } from "./extensions";
import { ladderOptions } from "./notification-rules";

vi.mock("@tanstack/react-router", () => ({
  createLink: (component: ComponentType) => component,
  useNavigate: () => vi.fn(),
  useRouter: () => ({ invalidate: vi.fn() }),
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
}));
vi.mock("./principal", () => ({ useSession: () => ({ organization: { name: "Example" } }) }));
vi.mock("./authorization", () => ({ useAccess: () => ({ can: () => true }) }));
vi.mock("@/ds/components/toast", () => ({ useToast: () => ({ add: vi.fn() }) }));
afterEach(cleanup);

/* A kind the way an edition would register one: vouched for by the
 * server, so nothing proves it, and the list cannot remove it. */
const PAGER = defineContactKind({
  kind: "pager",
  label: "Pager",
  icon: <span />,
  description: "Get paged.",
  available: (config) => config.webhook_enabled,
  unavailable: "Not set up on this deployment.",
  action: "Add pager",
  add: { title: "Add a pager", description: "Pages arrive here.", component: () => <p>pager flow</p> },
  row: {
    title: (method) => method.value,
    badge: () => null,
    removeLabel: (method) => `Remove ${method.value}`,
    removeFailure: "Could not remove that pager",
    cannotRemove: () => "Pagers are removed on the pager.",
    confirm: null,
  },
  ladderLabel: (method) => `Pager, ${method.value}`,
});

function method(overrides: Partial<ContactMethod>): ContactMethod {
  return { id: "m", kind: "pager", value: "12", available: true, verified: true, primary: false, created_at: "2026-09-24T00:00:00Z", ...overrides };
}

describe("contact kinds", () => {
  it("registers a kind that core's screens then draw its way", () => {
    expect(contactKind("pager")).toBeUndefined();
    expect(isChannelKind("pager")).toBe(false);
    registerExtensions({ contactKinds: [PAGER] });
    expect(contactKind("pager")).toBe(PAGER);
    expect(isChannelKind("pager")).toBe(true);
    expect(ladderOptions([method({ id: "pager-id" })])).toContainEqual({ id: "pager-id", label: "Pager, 12" });
    render(<ChannelRows methods={[method({ id: "pager-id" })]} onChanged={vi.fn()} />);
    expect(screen.getByText("12")).toBeTruthy();
    expect(screen.getByRole("img", { name: "Pager" })).toBeTruthy();
    expect((screen.getByRole("button", { name: "Remove 12" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("lists a method of a kind no bundle registered by its raw value", () => {
    const fax = method({ id: "fax-id", kind: "fax", value: "+1 555 0100", verified: false });
    expect(contactKind("fax")).toBeUndefined();
    expect(isChannelKind("fax")).toBe(false);
    expect(ladderOptions([{ ...fax, verified: true }])).toContainEqual({ id: "fax-id", label: "fax, +1 555 0100" });
    render(<ChannelRows methods={[fax]} onChanged={vi.fn()} />);
    expect(screen.getByText("+1 555 0100")).toBeTruthy();
    expect(screen.getByRole("img", { name: "fax" })).toBeTruthy();
    expect(screen.getByText("Unconfirmed")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Remove +1 555 0100" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("lists methods still being confirmed after the confirmed ones", () => {
    render(
      <ChannelRows
        methods={[
          method({ id: "primary", kind: "email", value: "me@example.com", primary: true }),
          method({ id: "pending", kind: "email", value: "new@example.com", verified: false }),
          method({ id: "work", kind: "email", value: "me@work.example" }),
          method({ id: "slack", kind: "slack", value: "U1", label: "Me (@me)" }),
        ]}
        onChanged={vi.fn()}
      />,
    );
    const order = screen.getAllByRole("button", { name: /^(Remove|Unlink)/ }).map((button) => button.getAttribute("aria-label"));
    expect(order).toEqual(["Remove me@example.com", "Remove me@work.example", "Unlink Slack", "Remove new@example.com"]);
  });

  it("shows no badge for a method in its default state and keeps every remove action", () => {
    render(
      <ChannelRows
        methods={[
          method({ id: "primary", kind: "email", value: "me@example.com", primary: true }),
          method({ id: "work", kind: "email", value: "me@work.example" }),
          method({ id: "slack", kind: "slack", value: "U05CTH1AC3E", label: "Me (@me)" }),
        ]}
        onChanged={vi.fn()}
      />,
    );
    expect(screen.queryByText("Confirmed")).toBeNull();
    expect(screen.queryByText("Linked")).toBeNull();
    expect(screen.queryByText("U05CTH1AC3E")).toBeNull();
    expect(screen.getByText("Me (@me)")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Remove me@example.com" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Remove me@work.example" }) as HTMLButtonElement).disabled).toBe(false);
    expect((screen.getByRole("button", { name: "Unlink Slack" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
