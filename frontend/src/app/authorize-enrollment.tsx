import { useState, type FormEvent } from "react";
import { Link } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import {
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/ds/components/card";
import {
  DescriptionItem,
  DescriptionList,
} from "@/ds/components/description-list";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackLink } from "@/ds/components/page-header";
import { RadioGroup, RadioOption } from "@/ds/components/radio-group";
import { Referenced } from "@/ds/components/reference";
import { TextField } from "@/ds/components/text-field";
import { api, RequestError } from "../api";
import type { Agent, EnrollmentAgents, EnrollmentPreview } from "../types";
import type { EnrollmentSearch } from "../routes/_app.connect_.authorize";
import { useAccess } from "./authorization";
import { CreateAgentForm } from "./connect";
import { useSession } from "./principal";
import { useTitle } from "./use-title";

export interface EnrollmentPage extends EnrollmentAgents {
  preview: EnrollmentPreview | null;
  error: string;
}

export function AuthorizeEnrollmentScreen({
  page,
  search,
  onSearch,
}: {
  page: EnrollmentPage;
  search: EnrollmentSearch;
  onSearch: (search: EnrollmentSearch) => Promise<unknown>;
}) {
  useTitle("Connect an instance");
  const { organization } = useSession();
  const { holdsAnywhere } = useAccess();
  const [code, setCode] = useState(search.user_code ?? "");
  const [query, setQuery] = useState(search.q ?? "");
  const [selected, setSelected] = useState<Agent | null>(null);
  const [creating, setCreating] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [connected, setConnected] = useState<Agent | null>(null);
  const [error, setError] = useState("");
  const preview = page.preview;
  const target = preview?.agent ?? selected;
  const completed =
    connected ?? (preview?.status !== "pending" ? preview?.agent : null);

  async function authorize() {
    if (!target || !search.user_code || submitting) return;
    setSubmitting(true);
    setError("");
    try {
      await api.authorizeAgentEnrollment(search.user_code, target.slug);
      setConnected(target);
    } catch (reason) {
      setError(
        reason instanceof RequestError && reason.status === 404
          ? "The code has expired or the agent is no longer available. Run install again."
          : reason instanceof Error
            ? reason.message
            : "Unable to connect the instance. Try again.",
      );
    } finally {
      setSubmitting(false);
    }
  }

  function submitCode(event: FormEvent) {
    event.preventDefault();
    void onSearch({ user_code: code.trim().toUpperCase() });
  }

  return (
    <Page width="narrow">
      <PageHeader back={<PageBackLink to="/agents">Agents</PageBackLink>}>
        <div>
          <h1 className="text-title-lg text-ink">Connect an instance</h1>
          <p className="mt-1.5 text-body text-ink-muted">
            Choose an agent for this runtime.
          </p>
        </div>
      </PageHeader>
      {completed ? (
        <Card>
          <CardBody>
            <Callout tone="approve" title="Instance authorized">
              Your CLI can now finish installing the approval hooks for{" "}
              <AgentName agent={completed} />.
            </Callout>
          </CardBody>
          <CardFooter>
            <Link
              to="/agents/$agentSlug"
              params={{ agentSlug: completed.slug }}
              className="text-body text-accent hover:underline"
            >
              View agent
            </Link>
          </CardFooter>
        </Card>
      ) : (
        <>
          {page.error && (
            <Callout role="alert" tone="deny">
              {page.error}
            </Callout>
          )}
          {!preview ? (
            <form onSubmit={submitCode}>
              <Card>
                <CardBody>
                  <TextField
                    label="Authorization code"
                    mono
                    value={code}
                    onChange={(event) => setCode(event.target.value)}
                    placeholder="ABCD-EFGH"
                    description="Enter the code shown by withhuman agent install."
                    required
                    autoFocus
                  />
                </CardBody>
                <CardFooter>
                  <Button
                    type="submit"
                    variant="primary"
                    disabled={!code.trim()}
                  >
                    Continue
                  </Button>
                </CardFooter>
              </Card>
            </form>
          ) : (
            <>
              <Card>
                <CardBody>
                  <DescriptionList>
                    <DescriptionItem term="Organization" copy={false}>
                      {organization?.name ?? "Current organization"}
                    </DescriptionItem>
                    <DescriptionItem term="Runtime" mono copy={false}>
                      {preview.runtime}
                    </DescriptionItem>
                    <DescriptionItem term="Instance" mono copy={false}>
                      {preview.instance_name}
                    </DescriptionItem>
                    <DescriptionItem
                      term="Authorization code"
                      mono
                      copy={false}
                    >
                      {search.user_code}
                    </DescriptionItem>
                    {preview.requested_agent_slug && !target && (
                      <DescriptionItem term="Requested slug" mono copy={false}>
                        {preview.requested_agent_slug}
                      </DescriptionItem>
                    )}
                    {target && (
                      <DescriptionItem term="Agent" copy={false}>
                        <AgentName agent={target} />
                      </DescriptionItem>
                    )}
                  </DescriptionList>
                </CardBody>
              </Card>
              {creating ? (
                <>
                  <CreateAgentForm
                    canCreate={holdsAnywhere("agent.write")}
                    forEnrollment
                    onCreated={(agent) => {
                      setSelected(agent);
                      setCreating(false);
                    }}
                  />
                  <Button onClick={() => setCreating(false)}>
                    Choose an existing agent
                  </Button>
                </>
              ) : (
                <Card>
                  <CardHeader>
                    <CardTitle>
                      {preview.requested_agent_slug
                        ? "Confirm connection"
                        : "Choose an agent"}
                    </CardTitle>
                  </CardHeader>
                  <CardBody className="flex flex-col gap-4">
                    {preview.requested_agent_slug ? (
                      <p className="text-body text-ink-muted">
                        Your CLI requested the agent{" "}
                        <span className="font-mono text-[12px] text-ink">
                          {preview.requested_agent_slug}
                        </span>
                        {target && (
                          <>
                            {" "}(<AgentName agent={target} />)
                          </>
                        )}
                        .
                      </p>
                    ) : (
                      <>
                        <form
                          className="flex items-end gap-2"
                          onSubmit={(event) => {
                            event.preventDefault();
                            void onSearch({ ...search, q: query, offset: 0 });
                          }}
                        >
                          <TextField
                            label="Search agents"
                            value={query}
                            onChange={(event) => setQuery(event.target.value)}
                          />
                          <Button type="submit">Search</Button>
                        </form>
                        <RadioGroup
                          aria-label="Agent"
                          value={selected?.slug ?? ""}
                          onValueChange={(value) =>
                            setSelected(
                              page.agents.find((agent) => agent.slug === value) ??
                                null,
                            )
                          }
                        >
                          {selected &&
                            !page.agents.some(
                              (agent) => agent.slug === selected.slug,
                            ) && (
                              <RadioOption
                                value={selected.slug}
                                label={<AgentName agent={selected} />}
                              />
                            )}
                          {page.agents.map((agent) => (
                            <RadioOption
                              key={agent.slug}
                              value={agent.slug}
                              label={<AgentName agent={agent} />}
                            />
                          ))}
                        </RadioGroup>
                        {page.agents.length === 0 && (
                          <p className="text-body text-ink-muted">
                            {search.q
                              ? "No matching agents."
                              : holdsAnywhere("agent.write")
                                ? "No agents are available to connect. Create one to get started."
                                : "No agents are available to connect. Ask an administrator for access."}
                          </p>
                        )}
                        {((search.offset ?? 0) > 0 ||
                          page.next_offset !== undefined) && (
                          <div className="flex items-center justify-between gap-2">
                            <Button
                              disabled={!search.offset}
                              onClick={() =>
                                void onSearch({
                                  ...search,
                                  offset: Math.max(
                                    0,
                                    (search.offset ?? 0) - 25,
                                  ),
                                })
                              }
                            >
                              Previous
                            </Button>
                            <Button
                              disabled={page.next_offset === undefined}
                              onClick={() =>
                                void onSearch({
                                  ...search,
                                  offset: page.next_offset,
                                })
                              }
                            >
                              Next
                            </Button>
                          </div>
                        )}
                        {holdsAnywhere("agent.write") && (
                          <Button onClick={() => setCreating(true)}>
                            Create new agent
                          </Button>
                        )}
                      </>
                    )}
                    {error && (
                      <Callout role="alert" tone="deny">
                        {error}
                      </Callout>
                    )}
                  </CardBody>
                  <CardFooter>
                    <Button
                      variant="primary"
                      disabled={!target}
                      loading={submitting}
                      onClick={() => void authorize()}
                    >
                      Connect instance
                    </Button>
                  </CardFooter>
                </Card>
              )}
            </>
          )}
        </>
      )}
    </Page>
  );
}

/* The agent's name with its slug beside it in mono, so two agents with
 * similar names tell apart at a glance. */
function AgentName({ agent }: { agent: Agent }) {
  return (
    <Referenced
      reference={{ kind: "agent", slug: agent.slug, name: agent.name }}
      inline
    >
      <span className="text-ink">{agent.name}</span>{" "}
      <span className="font-mono text-[12px] text-ink-muted">{agent.slug}</span>
      {agent.kind === "personal" ? (
        <span className="ml-1 text-label text-ink-faint">personal</span>
      ) : null}
    </Referenced>
  );
}
