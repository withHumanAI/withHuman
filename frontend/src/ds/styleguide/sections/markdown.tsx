import { Markdown } from "@/ds/components/markdown";
import { Section, Demo } from "../section";

const SAMPLE = `### Using the \`posthog\` tool

PostHog makes your product self-driving: it reads your data and ships changes **with you, never without you**. Pass CLI-style commands in the \`command\` parameter.

**Requirements**

1. Find unknown tools with \`search\` or \`tools\`.
2. Run \`info <tool_name>\` once if its schema is not in context.
   - Reuse it unless the tool changes.
   - Never guess a schema.

\`\`\`text
posthog:exec({ "command": "search <regex>" })
posthog:exec({ "command": "tools" })
\`\`\`

> Oversized sub-schemas are summarized with a note to drill further.

See the [docs](https://withhuman.ai/docs) for the full grammar.`;

export function MarkdownSection() {
  return (
    <Section
      id="markdown"
      title="Markdown"
      lede="Machine-authored prose (a tool's description, an agent's reasoning) rendered in the product's own type. Headings become bold lines at the surrounding size, code is mono, links open in a new tab, raw HTML is dropped. Sizes are relative, so the block takes the scale of where it sits. Long prose can collapse to a few lines behind Show more."
    >
      <Demo label="Body size, in a card">
        <div className="max-w-[560px] text-body text-ink-secondary">
          <Markdown>{SAMPLE}</Markdown>
        </div>
      </Demo>

      <Demo label="Collapsed to five lines">
        <div className="max-w-[560px] text-body text-ink-secondary">
          <Markdown lines={5}>{SAMPLE}</Markdown>
        </div>
      </Demo>

      <Demo label="Label size, in a list row">
        <div className="max-w-[560px] text-label font-normal text-ink-muted">
          <Markdown>{SAMPLE}</Markdown>
        </div>
      </Demo>
    </Section>
  );
}
