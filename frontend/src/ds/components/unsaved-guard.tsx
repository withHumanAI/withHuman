import { useBlocker } from "@tanstack/react-router";
import { Button } from "./button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "./dialog";

export interface UnsavedGuardProps {
  /* Block router navigation while true; tab close and refresh get the
   * browser's native prompt. */
  dirty: boolean;
  saving?: boolean;
  /* Commit the pending edits; navigation continues once it resolves. */
  onSave: () => void | Promise<void>;
  onDiscard: () => void;
}

/* SaveBar's companion: intercepts leaving the page while edits are
 * pending and makes the choice explicit. Closing the dialog keeps
 * the user editing; nothing is discarded by reflex. */
export function UnsavedGuard({
  dirty,
  saving = false,
  onSave,
  onDiscard,
}: UnsavedGuardProps) {
  const { proceed, reset, status } = useBlocker({
    shouldBlockFn: () => dirty,
    withResolver: true,
    enableBeforeUnload: () => dirty,
  });

  return (
    <Dialog
      open={status === "blocked"}
      onOpenChange={(open) => {
        if (!open) reset?.();
      }}
    >
      <DialogContent>
        <DialogTitle>Unsaved changes</DialogTitle>
        <DialogDescription>
          Save or discard your changes before leaving.
        </DialogDescription>
        <DialogFooter>
          <Button variant="ghost" disabled={saving} onClick={() => reset?.()}>
            Keep editing
          </Button>
          <Button
            variant="neutral"
            disabled={saving}
            onClick={() => {
              onDiscard();
              proceed?.();
            }}
          >
            Discard and leave
          </Button>
          <Button
            variant="primary"
            loading={saving}
            onClick={() => {
              void Promise.resolve(onSave()).then(() => proceed?.());
            }}
          >
            Save and leave
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
