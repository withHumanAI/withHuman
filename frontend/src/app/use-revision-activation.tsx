import { useState } from "react";
import { RevisionActivationDialog } from "@/ds/components/revision-activation-dialog";
import { useToast } from "@/ds/components/toast";
import { RequestError } from "../api";

interface SavedRevision {
  revision: number;
  activeRevision: number | null;
  canActivate: boolean;
  activationDescription: string;
  activate: () => Promise<unknown>;
}

/* Every revisioned save pauses here before refreshing or navigating away.
 * Activation retries use the saved revision, never create another one. */
export function useRevisionActivation() {
  const toast = useToast();
  const [pending, setPending] = useState<(SavedRevision & { resolve: () => void }) | null>(null);

  function offer(saved: SavedRevision): Promise<void> {
    return new Promise((resolve) => setPending({ ...saved, resolve }));
  }

  function finish(active: boolean) {
    if (!pending) return;
    toast.add({
      type: active ? "approve" : "neutral",
      title: `Revision ${pending.revision} ${active ? "active" : "saved as draft"}`,
      description: active
        ? pending.activationDescription
        : "Activate it later to make your changes live.",
    });
    setPending(null);
    pending.resolve();
  }

  const dialog = pending ? (
    <RevisionActivationDialog
      revision={pending.revision}
      activeRevision={pending.activeRevision}
      canActivate={pending.canActivate}
      activationDescription={pending.activationDescription}
      onKeepDraft={() => finish(false)}
      onActivate={async () => {
        try {
          await pending.activate();
        } catch (reason) {
          if (reason instanceof RequestError && reason.status === 412) {
            throw new Error("Someone else changed the active revision. Keep this draft and review the page first.");
          }
          throw reason;
        }
        finish(true);
      }}
    />
  ) : null;

  return { offer, dialog };
}
