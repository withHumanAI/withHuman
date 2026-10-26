import { PageHeader, PageBackLink, PageBackButton } from "@/ds/components/page-header";
import { Page } from "@/ds/components/page";
import { Section, Demo, DemoRow } from "../section";

export function SurfacesSection() {
  return (
    <Section
      id="surfaces"
      title="Surfaces and motion"
      lede="Hairline 0.5px edges, layered surfaces, and a small motion vocabulary: fast fills, decisive settles, nothing bouncy."
    >
      <Demo
        label="Page"
        hint="every screen lays out in one of three measures, chosen by what it holds"
        bare
      >
        <div className="flex flex-col gap-2">
          {[
            { width: "narrow" as const, px: 760, note: "something you read and act on: a request, an editor" },
            { width: "default" as const, px: 980, note: "lists and forms: the queue, agents, every settings page" },
            { width: "wide" as const, px: 1240, note: "dense tables: the audit log, pipelines" },
          ].map((measure) => (
            <div key={measure.width} className="overflow-hidden rounded-lg border-hairline border-edge bg-canvas">
              <Page width={measure.width} className="py-4">
                <div className="flex items-baseline justify-between gap-4 rounded-sm border-hairline border-dashed border-edge-strong px-3 py-2">
                  <span className="font-mono text-micro text-ink">{measure.width}</span>
                  <span className="text-micro text-ink-muted">{measure.note}</span>
                  <span className="font-mono text-micro text-ink-faint">{measure.px}px</span>
                </div>
              </Page>
            </div>
          ))}
        </div>
      </Demo>

      <Demo label="Page back navigation" hint="a 14px arrow and muted label, left aligned on their own row 12px above the title">
        <DemoRow className="items-start gap-8">
          <PageHeader back={<PageBackLink to="/agents">Agents</PageBackLink>}>
            <h1 className="text-title-lg text-ink">Agent details</h1>
          </PageHeader>
          <PageHeader back={<PageBackButton>Stop editing</PageBackButton>}>
            <h1 className="text-title-lg text-ink">Pipeline editor</h1>
          </PageHeader>
        </DemoRow>
      </Demo>

      <Demo label="Elevation" bare>
        <DemoRow className="items-stretch">
          {[
            { name: "surface", cls: "bg-surface shadow-sm", note: "cards, rows" },
            { name: "raised", cls: "bg-surface-raised shadow-md", note: "menus" },
            { name: "overlay", cls: "bg-surface-overlay shadow-overlay", note: "dialogs" },
          ].map((level) => (
            <div
              key={level.name}
              className={`flex h-28 w-44 flex-col justify-between rounded-lg border-hairline border-edge p-4 ${level.cls}`}
            >
              <span className="text-label text-ink">{level.name}</span>
              <span className="text-micro text-ink-muted">{level.note}</span>
            </div>
          ))}
        </DemoRow>
      </Demo>

      <Demo
        label="Motion"
        hint="hover each chip to preview its duration and easing"
      >
        <DemoRow>
          {[
            { name: "fast · 100ms", cls: "duration-100 ease-standard" },
            { name: "base · 160ms", cls: "duration-160 ease-standard" },
            { name: "slow · 240ms", cls: "duration-240 ease-out" },
          ].map((motion) => (
            <div
              key={motion.name}
              className={`cursor-default rounded-sm border-hairline border-edge bg-canvas px-3 py-1.5 font-mono text-micro font-normal text-ink-secondary transition-transform hover:-translate-y-1 ${motion.cls}`}
            >
              {motion.name}
            </div>
          ))}
        </DemoRow>
        <p className="mt-3 text-micro text-ink-faint">
          Transition specific properties only, never all. Reduced motion
          is respected globally.
        </p>
      </Demo>

      <Demo label="Motion policy" bare>
        <div className="flex max-w-[640px] flex-col gap-5">
          <p className="text-body text-ink-secondary">
            Motion explains change, it never decorates. Something animates
            only when it is a direct response to a user action and a
            geometry change the eye needs to track. If nothing moved,
            nothing animates.
          </p>
          <div className="grid gap-6 sm:grid-cols-2">
            <PolicyList
              title="Gets motion"
              items={[
                "Structural reveals and collapses: disclosures, the sidebar, popovers, dialogs, toasts. Show where the space came from. 160ms.",
                "Handoffs inside a row: a separator ceding space to a control, a check drawing in. The container is reshaping; show it.",
                "In-progress work: spinners and loading dots. The only motion without a user gesture, because the movement is the information.",
                "Micro-feedback on interactive surfaces: hover and press fills, copy-tick fades. Color and opacity only, 100ms, never position or size.",
                "Rows arriving in a live tail the reader switched on, like the audit log's Live: each batch opens from the top in one 160ms slide, and a batch too large to follow appears instantly. The switch is the gesture; nothing glows.",
              ]}
            />
            <PolicyList
              title="Never moves"
              items={[
                "Anything at rest. No pulsing, breathing, or shimmering.",
                "State where color is the signal: selection, active row, status. A 100ms color fade is the ceiling.",
                "Keyboard-driven movement: arrowing through lists, focus rings. Animation there reads as lag. Instant.",
                "Content: text, payloads, and values never slide or reflow-animate. Containers move; content appears.",
                "Data arriving anywhere else: queue updates swap instantly. No row shuffles, no new-item glows. This is a review queue, not a feed.",
                "Errors and confirmations: urgency does not wait 160ms.",
              ]}
            />
          </div>
          <p className="text-micro text-ink-faint">
            Three durations (100 color and opacity, 160 structural, 200
            overlays), two easings (ease-out enters, ease-standard
            otherwise). No springs, no overshoot. Named exception: the
            button press translates 0.5px, sub-pixel tactility rather
            than movement.
          </p>
        </div>
      </Demo>
    </Section>
  );
}

function PolicyList({ title, items }: { title: string; items: string[] }) {
  return (
    <div className="flex flex-col gap-2">
      <p className="text-micro tracking-[0.06em] text-ink-faint uppercase">
        {title}
      </p>
      <ul className="flex flex-col gap-2">
        {items.map((item) => (
          <li
            key={item}
            className="border-l-2 border-edge pl-3 text-label font-normal text-ink-secondary"
          >
            {item}
          </li>
        ))}
      </ul>
    </div>
  );
}
