// @vitest-environment jsdom
import type { ComponentType, ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, RequestError } from "../api";
import type { NotificationRules } from "../types";
import type { ChannelsPage } from "./contact-channels";
import { useLadderDraft } from "./notification-rules";
import { OnboardingRulesStep } from "./onboarding-contacts";

vi.mock("@tanstack/react-router", () => ({
  createLink: (component: ComponentType) => component,
  useNavigate: () => vi.fn(),
  useRouter: () => ({ invalidate: vi.fn() }),
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
}));
vi.mock("./principal", () => ({ useSession: () => ({ organization: { name: "Example" } }) }));
vi.mock("./authorization", () => ({ useAccess: () => ({ can: () => true }) }));
vi.mock("./use-runtime-config", () => ({ useRuntimeConfig: () => ({ config: {} }) }));
vi.mock("@/ds/components/toast", () => ({ useToast: () => ({ add: vi.fn() }) }));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const rules: NotificationRules = {
  standard: [{ delay_seconds: 0, contact_method_id: null }],
  interrupt: [{ delay_seconds: 0, contact_method_id: null }],
  customized: { standard: false, interrupt: false },
};
const page: ChannelsPage = { methods: [], slackWorkspace: null, discordServer: null, rules, error: "" };

function Step({ onContinue }: { onContinue: () => void }) {
  const ladders = useLadderDraft(page.rules, page.methods, async () => undefined);
  return (
    <OnboardingRulesStep
      page={page}
      ladders={ladders}
      onChanged={async () => undefined}
      onContinue={onContinue}
      continuing={false}
      error=""
    />
  );
}

describe("onboarding notification rules", () => {
  it("saves edited ladders when the person continues", async () => {
    const save = vi.spyOn(api, "saveNotificationRules").mockResolvedValue(rules);
    const onContinue = vi.fn();
    render(<Step onContinue={onContinue} />);

    fireEvent.click(screen.getAllByRole("button", { name: "Add rule" })[0]);
    expect(screen.queryByText(/unsaved change/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => expect(onContinue).toHaveBeenCalledOnce());
    expect(save).toHaveBeenCalledOnce();
    expect(save.mock.calls[0][0]).toBe("standard");
    expect(save.mock.calls[0][1]).toHaveLength(2);
  });

  it("continues without a write when nothing changed", async () => {
    const save = vi.spyOn(api, "saveNotificationRules");
    const onContinue = vi.fn();
    render(<Step onContinue={onContinue} />);

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => expect(onContinue).toHaveBeenCalledOnce());
    expect(save).not.toHaveBeenCalled();
  });

  it("stays on the step when the save fails", async () => {
    vi.spyOn(api, "saveNotificationRules").mockRejectedValue(
      new RequestError("A rule names a removed method.", 400, "invalid_rules"),
    );
    const onContinue = vi.fn();
    render(<Step onContinue={onContinue} />);

    fireEvent.click(screen.getAllByRole("button", { name: "Add rule" })[1]);
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText("A rule names a removed method.")).toBeTruthy();
    expect(onContinue).not.toHaveBeenCalled();
  });
});
