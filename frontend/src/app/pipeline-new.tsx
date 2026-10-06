import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card, CardBody, CardFooter } from "@/ds/components/card";
import { Page } from "@/ds/components/page";
import { PageBackLink, PageHeader } from "@/ds/components/page-header";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { api, RequestError } from "../api";
import { ENTRY_PIPELINE_KEY } from "../types";
import { PIPELINE_KEY_PATTERN, suggestPipelineKey, validPipelineKey } from "./pipeline-blocks";
import { pipelineLink } from "./pipeline-link";
import { useTitle } from "./use-title";

/*
 * A new pipeline: a name and a key. Creating it writes an empty first
 * revision, active at once, so requests a delegate hands to it go to a
 * human until its owners add blocks; the editor opens right after. The
 * key is chosen once: it is what delegate blocks name and what a
 * pipeline-scoped grant is pinned to, and it is never reused.
 */
export function NewPipelineScreen() {
  const navigate = useNavigate();
  const toast = useToast();
  useTitle("New pipeline");
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  /* The key follows the name until it is typed by hand. */
  const [keyEdited, setKeyEdited] = useState(false);
  const [creating, setCreating] = useState(false);
  const [taken, setTaken] = useState("");

  const keyError =
    key === ""
      ? ""
      : key === ENTRY_PIPELINE_KEY
        ? "This key is reserved."
        : !PIPELINE_KEY_PATTERN.test(key)
          ? "Lowercase letters, digits, dots, dashes, and underscores; up to 63 characters."
          : !validPipelineKey(key)
            ? "This key is reserved."
            : taken === key
              ? "This key is already taken."
              : "";
  const canCreate = name.trim() !== "" && key !== ENTRY_PIPELINE_KEY && validPipelineKey(key) && taken !== key;

  async function create() {
    setCreating(true);
    try {
      const revision = await api.createRequestApprovalPipeline({ pipeline_key: key, name: name.trim(), blocks: [] });
      toast.add({
        type: "approve",
        title: `${revision.name} created`,
        description: "Add blocks to decide what happens to its requests.",
      });
      await navigate({ ...pipelineLink(revision.pipeline_key, { edit: true }), replace: true });
    } catch (reason) {
      if (reason instanceof RequestError && reason.code === "request_approval_pipeline_exists") {
        setTaken(key);
        return;
      }
      toast.add({
        type: "deny",
        title: "The pipeline could not be created",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setCreating(false);
    }
  }

  return (
    <Page width="narrow">
      <PageHeader back={<PageBackLink to="/pipelines">Approval pipelines</PageBackLink>}>
        <h1 className="text-title-lg text-ink">New pipeline</h1>
        <p className="mt-1.5 max-w-[620px] text-body text-ink-muted">
          Create a pipeline that other pipelines can delegate requests to.
        </p>
      </PageHeader>
      <Card>
        <CardBody className="flex flex-col gap-4">
          <TextField
            label="Name"
            value={name}
            onChange={(event) => {
              setName(event.target.value);
              if (!keyEdited) setKey(suggestPipelineKey(event.target.value));
            }}
            placeholder="Payments"
            className="w-72"
            autoFocus
          />
          <TextField
            label="Key"
            mono
            value={key}
            onChange={(event) => {
              setKeyEdited(true);
              setKey(event.target.value.trim().toLowerCase());
            }}
            placeholder="payments"
            className="w-72"
            error={keyError}
            info="The pipeline's unique identifier. It can't be changed later."
            docsLinks={[{ href: "https://withhuman.ai/docs/web-app/approval-pipelines#pipelines", label: "Creating pipelines" }]}
          />
          <Callout tone="neutral">
            Requests sent to this pipeline go to a human until you add blocks.
          </Callout>
        </CardBody>
        <CardFooter className="justify-end gap-2">
          <Button variant="ghost" onClick={() => void navigate({ to: "/pipelines" })} disabled={creating}>
            Cancel
          </Button>
          <Button variant="primary" onClick={() => void create()} disabled={!canCreate} loading={creating}>
            Create pipeline
          </Button>
        </CardFooter>
      </Card>
    </Page>
  );
}
