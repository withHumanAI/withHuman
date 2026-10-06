import { useRef, useState } from "react";
import { Button } from "./button";
import { Callout } from "./callout";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle } from "./dialog";

export interface RevisionActivationDialogProps {
  revision: number;
  activeRevision: number | null;
  canActivate: boolean;
  activationDescription: string;
  onActivate: () => Promise<void>;
  onKeepDraft: () => void;
}

/* Mounted only after a revision has been saved. Dismissal keeps that
 * revision as a draft; failed activation never asks the caller to save again. */
export function RevisionActivationDialog({
  revision,
  activeRevision,
  canActivate,
  activationDescription,
  onActivate,
  onKeepDraft,
}: RevisionActivationDialogProps) {
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");
  const inFlight = useRef(false);

  function keepDraft() {
    if (!inFlight.current) onKeepDraft();
  }

  async function activate() {
    if (inFlight.current || !canActivate) return;
    inFlight.current = true;
    setWorking(true);
    setError("");
    try {
      await onActivate();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Activation could not be completed. Try again.");
    } finally {
      inFlight.current = false;
      setWorking(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) keepDraft(); }}>
      <DialogContent>
        <DialogTitle>Make revision {revision} active?</DialogTitle>
        <DialogDescription>
          Your changes are saved as a draft.{" "}
          {activeRevision === null
            ? "No revision is active yet."
            : `Revision ${activeRevision} is still active.`}{" "}
          {activationDescription}
        </DialogDescription>
        {!canActivate ? (
          <Callout className="mt-4" tone="neutral">
            You do not have permission to activate revisions.
          </Callout>
        ) : null}
        {error ? (
          <div role="alert" className="mt-4">
            <Callout tone="deny" title={`Revision ${revision} saved, activation failed`}>
              {error}
            </Callout>
          </div>
        ) : null}
        <DialogFooter className="flex-wrap">
          <Button variant="neutral" disabled={working} onClick={keepDraft}>
            Keep as draft
          </Button>
          <Button variant="approve" disabled={!canActivate} loading={working} onClick={() => void activate()}>
            Make active
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
