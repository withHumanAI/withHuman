import { useState } from "react";
import { Badge } from "@/ds/components/badge";
import { TextArea } from "@/ds/components/text-area";
import { TextField } from "@/ds/components/text-field";
import { Section, Demo, KeyboardHint } from "../section";

export function TextFieldsSection() {
  const [name, setName] = useState("");

  return (
    <Section
      id="text-fields"
      title="TextField"
      lede="Label, description, and error wiring come from Base UI Field; the focus ring lives inside the border instead of floating outside."
    >
      <Demo label="Anatomy" hint="labelEnd hangs a marker at the far end of the label row">
        <div className="flex max-w-[360px] flex-col gap-5">
          <TextField
            label="Agent name"
            placeholder="billing-agent-01"
            description="Shown in the review queue next to every request."
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
          <TextField
            label="Webhook endpoint"
            mono
            placeholder="https://ops.example.com/hooks/withhuman"
            defaultValue=""
          />
          <TextField
            label="Key"
            mono
            placeholder="payments"
            info="The path's unique identifier. It can't be changed later."
            defaultValue=""
          />
          <TextField
            label="Enrollment token"
            mono
            defaultValue="whk_3f9a1c77b2"
            error="This token expired 3 days ago."
          />
          <TextField
            label="Email"
            type="email"
            defaultValue="ada@example.com"
            labelEnd={
              <Badge fill icon={false} className="h-5 px-1.5 text-micro">
                Last used
              </Badge>
            }
          />
          <TextField label="Disabled" placeholder="Read only" disabled />
        </div>
        <KeyboardHint>
          Tab moves between fields and info icons; labels focus their input on click.
          Info icons show help on hover, focus, or click. Escape dismisses the help.
        </KeyboardHint>
      </Demo>

      <Demo label="TextArea" hint="grows with its content">
        <div className="max-w-[360px]">
          <TextArea
            label="Reason for denial"
            placeholder="Explain the decision for the audit log"
            info="Visible to the requesting agent's owner."
          />
        </div>
      </Demo>
    </Section>
  );
}
