import { useRef, useState, type FormEvent } from "react";
import { Link, useRouter } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import {
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/ds/components/card";
import { CodeBlock } from "@/ds/components/code-block";
import {
  DescriptionItem,
  DescriptionList,
} from "@/ds/components/description-list";
import { Page } from "@/ds/components/page";
import { PageBackLink, PageHeader } from "@/ds/components/page-header";
import { RadioGroup, RadioOption } from "@/ds/components/radio-group";
import { TextField } from "@/ds/components/text-field";
import { InfoLabel } from "@/ds/components/info-label";
import { api, RequestError } from "../api";
import type { AgentDetail } from "../types";
import { agentInstanceSetup } from "./agent-instance-setup";
import { useAccess } from "./authorization";
import { formatAbsolute } from "./format";
import { useRuntimeConfig } from "./use-runtime-config";
import { useTitle } from "./use-title";

export interface NewAgentInstancePage {
  agent: AgentDetail | null;
  error: string;
}

export async function loadNewAgentInstancePage(
  slug: string,
): Promise<NewAgentInstancePage> {
  try {
    return { agent: await api.agent(slug), error: "" };
  } catch (reason) {
    return {
      agent: null,
      error:
        reason instanceof RequestError && reason.status === 404
          ? "The agent could not be found."
          : reason instanceof Error
            ? reason.message
            : "The agent could not be loaded.",
    };
  }
}

export function NewAgentInstanceScreen({
  agentSlug,
  page,
}: {
  agentSlug: string;
  page: NewAgentInstancePage;
}) {
  useTitle("New instance");
  const router = useRouter();
  const { can } = useAccess();
  const { config } = useRuntimeConfig();
  const [method, setMethod] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const pending = useRef(false);
  const [error, setError] = useState("");
  // The one-time credential never enters a loader, URL, or persistent store.
  const [issued, setIssued] = useState<Awaited<
    ReturnType<typeof api.createAgentInstance>
  > | null>(null);
  const resource = { kind: "agent", id: agentSlug } as const;
  const allowed =
    can("agent.read", resource) && can("agent.credential.issue", resource);
  const setup = agentInstanceSetup(config, agentSlug);
  const agent = page.agent;

  async function generate(event: FormEvent) {
    event.preventDefault();
    if (
      pending.current ||
      issued ||
      !allowed ||
      !setup ||
      agent?.status !== "active" ||
      !name.trim()
    )
      return;
    pending.current = true;
    setSubmitting(true);
    setError("");
    try {
      setIssued(await api.createAgentInstance(agentSlug, name.trim()));
      await router.invalidate();
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : "The API key could not be generated. Try again.",
      );
    } finally {
      pending.current = false;
      setSubmitting(false);
    }
  }

  const instancesLink = (label: string) => (
    <Link
      to="/agents/$agentSlug"
      params={{ agentSlug }}
      search={{ tab: "instances" }}
      className="text-body text-accent hover:underline"
    >
      {label}
    </Link>
  );

  return (
    <Page width="narrow">
      <PageHeader
        back={
          <PageBackLink
            to="/agents/$agentSlug"
            params={{ agentSlug }}
            search={{ tab: "instances" }}
          >
            Instances
          </PageBackLink>
        }
      >
        <div>
          <h1 className="text-title-lg text-ink">
            New instance{agent ? ` of ${agent.name}` : ""}
          </h1>
          <p className="mt-1.5 text-body text-ink-muted">
            Connect a runtime to this agent.
          </p>
        </div>
      </PageHeader>
      {!allowed ? (
        <Callout tone="warn" title="Permission required">
          You need permission to view this agent and issue its credentials.
        </Callout>
      ) : issued && setup ? (
        <Card>
          <CardHeader>
            <CardTitle>API key generated</CardTitle>
          </CardHeader>
          <CardBody className="flex flex-col gap-4">
            <DescriptionList>
              <DescriptionItem term="Instance" copy={false}>
                {issued.agent_instance.name}
              </DescriptionItem>
              <DescriptionItem term="Key expires" copy={false}>
                {formatAbsolute(issued.credential.expires_at)}
              </DescriptionItem>
            </DescriptionList>
            <Callout tone="warn" title="Copy your API key now">
              This key is shown only once.
            </Callout>
            <CodeBlock
              label="Agent API key"
              code={issued.credential.token}
              wrap
            />
            <CodeBlock label="AAP provider URL" code={setup.providerURL} wrap />
            <p className="text-body text-ink-muted">
              Set your AAP integration’s provider URL to the address above and
              use this key as its bearer token (
              <code className="font-mono text-[12px]">
                Authorization: Bearer &lt;API key&gt;
              </code>
              ).
            </p>
            <a
              className="text-body text-accent hover:underline"
              href="https://withhuman.ai/docs/reference/agent-approval-protocol"
              target="_blank"
              rel="noreferrer"
            >
              AAP connection reference
            </a>
          </CardBody>
          <CardFooter>{instancesLink("Done")}</CardFooter>
        </Card>
      ) : page.error || !agent ? (
        <Callout tone="warn" title="Agent unavailable">
          <p>{page.error || "The agent could not be loaded."}</p>
          <Button className="mt-3" onClick={() => void router.invalidate()}>
            Try again
          </Button>
        </Callout>
      ) : agent.status !== "active" ? (
        <Callout
          tone="warn"
          title={
            agent.status === "archived" ? "Agent archived" : "Agent disabled"
          }
        >
          {agent.status === "archived" ? "Restore" : "Enable"} this agent before
          connecting an instance.
        </Callout>
      ) : !setup ? (
        <Callout tone="warn" title="Setup unavailable">
          <p>The provider settings could not be loaded.</p>
          <Button className="mt-3" onClick={() => window.location.reload()}>
            Reload page
          </Button>
        </Callout>
      ) : (
        <>
          <Card>
            <CardBody>
              <RadioGroup
                aria-label="Connection method"
                value={method}
                disabled={submitting}
                onValueChange={(value) => {
                  setMethod(value as string);
                  setError("");
                }}
              >
                <RadioOption
                  value="cli"
                  label={
                    <SetupMethodLabel
                      anchor="cli-enrollment"
                      info="Install the withHuman CLI where your agent runs."
                    >
                      Install with the CLI
                    </SetupMethodLabel>
                  }
                  description="Connect a supported runtime on a laptop or server."
                />
                <RadioOption
                  value="api_key"
                  label={
                    <SetupMethodLabel
                      anchor="api-key-setup"
                      info="Create a key for an integration that uses the Agent Approval Protocol directly."
                    >
                      Generate an API key
                    </SetupMethodLabel>
                  }
                  description="Configure an AAP integration directly."
                />
              </RadioGroup>
            </CardBody>
          </Card>
          {method === "cli" ? (
            <Card>
              <CardHeader>
                <CardTitle>Run on the machine where your agent runs</CardTitle>
              </CardHeader>
              <CardBody className="flex flex-col gap-4">
                <p className="text-body text-ink-muted">
                  This command installs the withHuman CLI on macOS or Linux,
                  then connects a runtime to {agent.name}.
                </p>
                <CodeBlock
                  label="Install and connect"
                  code={setup.command}
                  wrap
                />
                <ol className="list-decimal space-y-2 pl-5 text-body text-ink-muted">
                  <li>Choose a runtime if prompted.</li>
                  <li>Approve the connection in your browser.</li>
                  <li>
                    Follow any final setup instructions in the terminal.
                  </li>
                </ol>
              </CardBody>
              <CardFooter>{instancesLink("View instances")}</CardFooter>
            </Card>
          ) : method === "api_key" ? (
            <form onSubmit={generate}>
              <Card>
                <CardHeader>
                  <CardTitle>Set up an AAP integration</CardTitle>
                </CardHeader>
                <CardBody className="flex flex-col gap-4">
                  <p className="text-body text-ink-muted">
                    Give this instance a name.
                  </p>
                  <TextField
                    label="Instance name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="support-server-01"
                    description="Choose a unique name within this agent."
                    required
                    disabled={submitting}
                    autoFocus
                  />
                  {error ? (
                    <Callout
                      role="alert"
                      tone="deny"
                      title="API key was not generated"
                    >
                      {error}
                    </Callout>
                  ) : null}
                </CardBody>
                <CardFooter>
                  <Button
                    type="submit"
                    variant="primary"
                    loading={submitting}
                    disabled={!name.trim()}
                  >
                    Generate API key
                  </Button>
                </CardFooter>
              </Card>
            </form>
          ) : null}
        </>
      )}
    </Page>
  );
}

function SetupMethodLabel({
  anchor,
  info,
  children,
}: {
  anchor: string;
  info: string;
  children: string;
}) {
  return (
    <InfoLabel
      info={info}
      docsLinks={[{ href: `https://withhuman.ai/docs/web-app/agents#${anchor}`, label: `${children} guide` }]}
    >
      {children}
    </InfoLabel>
  );
}
