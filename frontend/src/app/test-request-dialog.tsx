import { useState } from "react";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/ds/components/dialog";
import { TextArea } from "@/ds/components/text-area";
import { AlertIcon } from "@/ds/icons";

/* The test entry point: a dialog with the sample request. Running it
 * closes the dialog and replays the evaluation on the rail itself. The
 * overview reuses it with its own description, since there the run
 * opens the entry pipeline first. */
export function TestRequestDialog({
  open,
  onOpenChange,
  sampleText,
  onSampleTextChange,
  onRun,
  description = "See what would happen to a sample request.",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sampleText: string;
  onSampleTextChange: (next: string) => void;
  /* Resolves to an error message to show, or null when the run
   * started and the dialog should close. */
  onRun: () => Promise<string | null>;
  description?: string;
}) {
  const [error, setError] = useState("");
  const [running, setRunning] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setError("");
        onOpenChange(next);
      }}
    >
      <DialogContent className="w-[min(560px,calc(100vw-32px))]">
        <DialogTitle>Test a request</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
        <DialogBody className="flex flex-col gap-3">
          {error ? (
            <Callout tone="warn" icon={<AlertIcon width={15} height={15} />}>
              {error}
            </Callout>
          ) : null}
          <TextArea
            aria-label="Test request"
            spellCheck={false}
            className="min-h-[260px] resize-y bg-canvas font-mono text-[11px] leading-[18px] shadow-none"
            value={sampleText}
            onChange={(event) => {
              onSampleTextChange(event.target.value);
              setError("");
            }}
          />
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" disabled={running} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={running}
            onClick={() => {
              try {
                JSON.parse(sampleText);
              } catch {
                setError("The sample is not valid JSON.");
                return;
              }
              setRunning(true);
              void onRun()
                .then((problem) => {
                  if (problem) setError(problem);
                })
                .finally(() => setRunning(false));
            }}
          >
            Run test
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
