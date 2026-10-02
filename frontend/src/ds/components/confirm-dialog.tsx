import { useState, type ReactNode } from "react";
import { Button } from "./button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "./dialog";
import { TextField } from "./text-field";

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /* State the consequence, not the question: what happens on confirm. */
  description: ReactNode;
  /* Block content under the description, such as a command to run,
   * which the description's paragraph cannot hold. */
  children?: ReactNode;
  /* Confirm button label, e.g. "Revoke credential". */
  confirmLabel: string;
  /* The worst actions require retyping this exact value (an org slug,
   * an agent name) before confirm enables. */
  typeToConfirm?: string;
  /* Confirm action; the dialog closes when it resolves. */
  onConfirm: () => void | Promise<void>;
  /* Destructive by default; approve covers consequential-but-positive
   * confirmations. */
  tone?: "danger" | "approve";
}

/* The ceremony for irreversible actions. Cancel is the safe default;
 * confirm carries the action's own words, never "OK". */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  confirmLabel,
  typeToConfirm,
  onConfirm,
  tone = "danger",
}: ConfirmDialogProps) {
  const [typed, setTyped] = useState("");
  const [working, setWorking] = useState(false);
  const armed = !typeToConfirm || typed === typeToConfirm;

  function close(next: boolean) {
    onOpenChange(next);
    if (!next) setTyped("");
  }

  function confirm() {
    setWorking(true);
    void Promise.resolve(onConfirm()).finally(() => {
      setWorking(false);
      close(false);
    });
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
        {children ? <div className="mt-4">{children}</div> : null}
        {typeToConfirm ? (
          <div className="mt-4">
            <TextField
              label={`Type ${typeToConfirm} to confirm`}
              mono
              autoComplete="off"
              spellCheck={false}
              value={typed}
              onChange={(event) => setTyped(event.target.value)}
            />
          </div>
        ) : null}
        <DialogFooter>
          <Button
            variant="neutral"
            disabled={working}
            onClick={() => close(false)}
          >
            Cancel
          </Button>
          <Button
            variant={tone === "danger" ? "danger" : "approve"}
            disabled={!armed}
            loading={working}
            onClick={confirm}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
