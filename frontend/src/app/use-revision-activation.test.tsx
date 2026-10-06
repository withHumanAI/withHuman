// @vitest-environment jsdom
import { useState } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RequestError } from "../api";
import { useRevisionActivation } from "./use-revision-activation";

const { toast } = vi.hoisted(() => ({ toast: vi.fn() }));
vi.mock("@/ds/components/toast", () => ({ useToast: () => ({ add: toast }) }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function setup({
  canActivate = true,
  activeRevision = 2 as number | null,
  activate = vi.fn<() => Promise<unknown>>().mockResolvedValue(undefined),
  save = vi.fn().mockResolvedValue({ revision: 3 }),
} = {}) {
  function Editor() {
    const activation = useRevisionActivation();
    const [saving, setSaving] = useState(false);
    const [finished, setFinished] = useState(false);
    const [error, setError] = useState("");
    return (
      <>
        <button disabled={saving} onClick={async () => {
          setSaving(true);
          try {
            const revision = await save();
            await activation.offer({
              revision: revision.revision,
              activeRevision,
              canActivate,
              activationDescription: "Once active, new requests use this revision.",
              activate,
            });
            setFinished(true);
          } catch {
            setError("Save failed");
          } finally {
            setSaving(false);
          }
        }}>Save</button>
        {activation.dialog}
        {finished ? <p>Save flow finished</p> : null}
        {error ? <p>{error}</p> : null}
      </>
    );
  }
  render(<Editor />);
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  return { activate, save };
}

describe("saved revision activation", () => {
  it("saves before asking, and keeps a draft without activating it", async () => {
    const { activate, save } = setup();
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain("Revision 2 is still active.");
    expect(dialog.textContent).toContain("Your changes are saved as a draft.");
    expect(save).toHaveBeenCalledTimes(1);
    expect(activate).not.toHaveBeenCalled();
    expect(screen.queryByText("Save flow finished")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Keep as draft" }));
    await screen.findByText("Save flow finished");
    expect(activate).not.toHaveBeenCalled();
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ title: "Revision 3 saved as draft" }));
  });

  it("activates only after Make active is chosen", async () => {
    const { activate, save } = setup();
    fireEvent.click(await screen.findByRole("button", { name: "Make active" }));
    await screen.findByText("Save flow finished");
    expect(save).toHaveBeenCalledTimes(1);
    expect(activate).toHaveBeenCalledTimes(1);
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ title: "Revision 3 active" }));
  });

  it.each(["close", "escape"])("keeps the draft when dismissed with %s", async (method) => {
    const { activate } = setup();
    const dialog = await screen.findByRole("dialog");
    if (method === "close") fireEvent.click(screen.getByRole("button", { name: "Close dialog" }));
    else fireEvent.keyDown(dialog, { key: "Escape" });
    await screen.findByText("Save flow finished");
    expect(activate).not.toHaveBeenCalled();
  });

  it("explains missing activation permission and a resource with no active revision", async () => {
    const { activate } = setup({ canActivate: false, activeRevision: null });
    const button = await screen.findByRole("button", { name: "Make active" });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("dialog").textContent).toContain("No revision is active yet.");
    expect(screen.getByRole("dialog").textContent).toContain("You do not have permission to activate");
    fireEvent.click(button);
    expect(activate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Keep as draft" }));
    await screen.findByText("Save flow finished");
  });

  it("keeps activation failures in the dialog and retries the same saved revision", async () => {
    const activate = vi.fn<() => Promise<unknown>>()
      .mockRejectedValueOnce(new Error("The endpoint is inactive."))
      .mockResolvedValue(undefined);
    const { save } = setup({ activate });
    fireEvent.click(await screen.findByRole("button", { name: "Make active" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Revision 3 saved, activation failed");
    expect(screen.queryByText("Save flow finished")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Make active" }));
    await screen.findByText("Save flow finished");
    expect(activate).toHaveBeenCalledTimes(2);
    expect(save).toHaveBeenCalledTimes(1);
  });

  it("explains conflicts and lets the caller refresh after keeping the saved draft", async () => {
    const activate = vi.fn<() => Promise<unknown>>().mockRejectedValue(new RequestError("Conflict", 412, "revision_conflict"));
    setup({ activate });
    fireEvent.click(await screen.findByRole("button", { name: "Make active" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Someone else changed the active revision.");
    fireEvent.click(screen.getByRole("button", { name: "Keep as draft" }));
    await screen.findByText("Save flow finished");
  });

  it("blocks duplicate activation and dismissal while activation is running", async () => {
    let complete!: () => void;
    const activate = vi.fn<() => Promise<unknown>>().mockImplementation(() => new Promise<void>((resolve) => { complete = resolve; }));
    setup({ activate });
    const button = await screen.findByRole("button", { name: "Make active" });
    fireEvent.click(button);
    fireEvent.click(button);
    fireEvent.click(screen.getByRole("button", { name: "Close dialog" }));
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(activate).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(screen.queryByText("Save flow finished")).toBeNull();
    complete();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await screen.findByText("Save flow finished");
  });

  it("does not offer activation when saving fails", async () => {
    const { activate } = setup({ save: vi.fn().mockRejectedValue(new Error("Invalid document")) });
    await screen.findByText("Save failed");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(activate).not.toHaveBeenCalled();
  });
});
