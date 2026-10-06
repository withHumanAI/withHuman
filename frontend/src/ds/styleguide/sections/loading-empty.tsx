import { Button } from "@/ds/components/button";
import { EmptyState } from "@/ds/components/empty-state";
import { LoadingDots } from "@/ds/components/loading-dots";
import { Spinner } from "@/ds/components/spinner";
import { InboxIcon, PlusIcon } from "@/ds/icons";
import { Section, Demo, DemoRow } from "../section";

export function LoadingEmptySection() {
  return (
    <Section
      id="loading-empty"
      title="Loading & EmptyState"
      lede="Waiting and nothing-to-do states. An empty queue is the product working, so it gets a calm, deliberate moment."
    >
      <Demo label="Spinner">
        <DemoRow className="gap-6">
          <Spinner size={14} />
          <Spinner />
          <Spinner size={20} />
          <span className="inline-flex items-center gap-2 text-body text-ink-muted">
            <Spinner size={14} />
            Loading requests
          </span>
        </DemoRow>
      </Demo>

      <Demo label="Loading dots">
        <DemoRow className="gap-6">
          <LoadingDots />
          <span className="inline-flex items-center gap-2 text-body text-ink-muted">
            <LoadingDots aria-label="The assistant is working" />
            The assistant is working
          </span>
        </DemoRow>
      </Demo>

      <Demo label="EmptyState" bare>
        <div className="rounded-lg border-hairline border-edge bg-surface shadow-sm">
          <EmptyState
            icon={<InboxIcon />}
            title="Nothing waiting for you"
            description="New requests from your agents will appear here."
            action={
              <Button variant="neutral">
                <PlusIcon width={14} height={14} />
                Connect an agent
              </Button>
            }
          />
        </div>
      </Demo>
    </Section>
  );
}
