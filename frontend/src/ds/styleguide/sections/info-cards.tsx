import { TextField } from "@/ds/components/text-field";
import { InfoCard, InfoCardLink } from "@/ds/components/info-card";
import { Section, Demo, KeyboardHint } from "../section";

const PATHS_DOCS = "https://withhuman.ai/docs/web-app/escalation-paths";
const PIPELINES_DOCS = "https://withhuman.ai/docs/web-app/approval-pipelines";

export function InfoCardsSection() {
  return (
    <Section
      id="info-cards"
      title="Info card"
      lede="A small info icon opens contextual help, with links in the explanation, a documentation footer, or both. Hover to read; click or tap to keep it open."
    >
      <Demo label="Text only" hint="a short explanation beside a label">
        <span className="inline-flex items-center gap-1 text-label text-ink-secondary">
          Path key
          <InfoCard title="Path key">
            <p>
              A permanent identifier that approval pipelines use to reference this escalation path.
              Choose it when you create the path; it cannot be changed later.
            </p>
          </InfoCard>
        </span>
      </Demo>

      <Demo label="Inline documentation" hint="a link where it adds context">
        <span className="inline-flex items-center gap-1 text-label text-ink-secondary">
          Escalate after
          <InfoCard title="Escalate after">
            <p>
              How long to wait before continuing to the next step if nobody has decided.
              Earlier reviewers can still act after the path moves on.
            </p>
            <p>
              See how <InfoCardLink href={`${PATHS_DOCS}#notify-steps`}>Notify steps</InfoCardLink>{" "}
              contact people and teams.
            </p>
          </InfoCard>
        </span>
      </Demo>

      <Demo label="Documentation footer" hint="related guides below the explanation">
        <span className="inline-flex items-center gap-1 text-label text-ink-secondary">
          Approval pipeline
          <InfoCard
            title="Approval pipeline"
            docsLinks={[
              { href: PIPELINES_DOCS, label: "Approval pipelines guide" },
              { href: PATHS_DOCS, label: "Escalation paths guide" },
            ]}
          >
            <p>
              Rules evaluate a request in order. They can approve it, deny it, or ask a human to review it.
            </p>
            <p>
              All requests go through the entry pipeline first.
            </p>
          </InfoCard>
        </span>
      </Demo>

      <Demo label="Inline links and footer" hint="context now, a full guide when needed">
        <span className="inline-flex items-center gap-1 text-label text-ink-secondary">
          Default escalation path
          <InfoCard
            title="Default escalation path"
            docsLinks={[{ href: PATHS_DOCS, label: "Escalation paths guide" }]}
          >
            <p>
              Choose who is contacted when a request needs human review and the deciding block has not chosen a path.
            </p>
            <p>
              The agent default takes precedence over the organization default.
              See <InfoCardLink href={`${PIPELINES_DOCS}#human-review`}>Human review</InfoCardLink>{" "}
              for how these choices apply.
            </p>
          </InfoCard>
        </span>
      </Demo>

      <Demo label="On a field" hint="shared by forms across the product">
        <div className="max-w-xs">
          <TextField
            label="Escalation path key"
            defaultValue="payments-review"
            info="A permanent identifier that pipelines use to reference this escalation path. Choose it when you create the path."
            docsLinks={[{ href: `${PATHS_DOCS}#paths`, label: "Creating escalation paths" }]}
          />
        </div>
      </Demo>

      <KeyboardHint>
        Tab to an info icon to open its card, then Tab through its links. Enter or Space keeps it open;
        press again or use Escape to dismiss. Documentation opens in a new tab.
      </KeyboardHint>
    </Section>
  );
}
