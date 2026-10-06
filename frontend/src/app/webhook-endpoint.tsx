import { useMemo, useState } from "react";
import { useRouter } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card, CardBody, CardDescription, CardHeader, CardTitle } from "@/ds/components/card";
import { CodeBlock } from "@/ds/components/code-block";
import { DescriptionItem, DescriptionList } from "@/ds/components/description-list";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { useCopyToClipboard } from "@/ds/components/copy-button";
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/ds/components/dialog";
import { IconButton } from "@/ds/components/icon-button";
import { Menu, MenuContent, MenuTrigger } from "@/ds/components/menu";
import { SaveBar } from "@/ds/components/save-bar";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackLink } from "@/ds/components/page-header";
import { RevisionHistory } from "@/ds/components/revision-history";
import { AlertIcon, CheckIcon, CopyIcon, EllipsisIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import type { PipelineUse, RevisionPage, WebhookEndpointRevision, WebhookEndpointTest } from "../types";
import { PipelineUseList } from "./pipeline-uses";
import { emptyRevisionPage, useRevisionPages } from "./revision-pages";
import { useTitle } from "./use-title";
import { useRevisionActivation } from "./use-revision-activation";
import { hostOf } from "./webhooks";

/*
 * Webhook endpoint editor. The endpoint is a key with immutable revisions
 * of a name and URL, exactly like an escalation path: saving creates a
 * new revision and offers activation, history lets an administrator roll back,
 * and archiving deactivates the current revision. The signing secret
 * belongs to the key: it is minted with the first revision, shown once
 * then and at rotation, and survives every revision.
 */

export interface EndpointDraft {
  name: string;
  url: string;
}

/* What the webhook endpoint route loader delivers: the active revision
 * as the editor's draft (the latest revision when the key is archived, a
 * blank draft when the key is new), the first page of the history, the
 * uses that would refuse an archive, and the failure when the endpoint
 * could not be read. */
export interface WebhookEndpointPage {
  activeRevision: number | null;
  /* Set while the key is archived; a key with neither this nor an active
   * revision holds a draft nobody activated. */
  archivedAt: string | null;
  saved: EndpointDraft | null;
  history: RevisionPage<WebhookEndpointRevision>;
  uses: PipelineUse[];
  secretRotatedAt: string | null;
  error: string;
}

export async function loadWebhookEndpointPage(endpointKey: string): Promise<WebhookEndpointPage> {
  const [active, history, uses, summaries] = await Promise.all([
    api.activeWebhookEndpoint(endpointKey).then(
      (result) => ({ revision: result, error: "" }),
      (reason: unknown) => ({
        revision: null,
        error:
          reason instanceof RequestError && reason.status === 404
            ? ""
            : reason instanceof Error
              ? reason.message
              : "Unable to load the webhook endpoint.",
      }),
    ),
    api.webhookEndpointRevisions(endpointKey).then(
      (result) => result,
      () => emptyRevisionPage<WebhookEndpointRevision>(),
    ),
    api.webhookEndpointUses(endpointKey).then((result) => result.uses, () => [] as PipelineUse[]),
    api.webhookEndpoints().then((result) => result.webhook_endpoints, () => []),
  ]);
  const summary = summaries.find((entry) => entry.endpoint_key === endpointKey);
  const secretRotatedAt = summary?.secret_rotated_at ?? null;
  const archivedAt = summary?.archived_at ?? null;
  if (active.error) {
    return { activeRevision: null, archivedAt, saved: null, history, uses, secretRotatedAt, error: active.error };
  }
  const source = active.revision ?? history.revisions[0] ?? null;
  return {
    activeRevision: active.revision?.revision ?? null,
    archivedAt,
    saved: source ? { name: source.name, url: source.url } : { name: "", url: "" },
    history,
    uses,
    secretRotatedAt,
    error: "",
  };
}

export function WebhookEndpointScreen({
  endpointKey,
  page,
}: {
  endpointKey: string;
  page: WebhookEndpointPage;
}) {
  useTitle("Webhook endpoint");
  const toast = useToast();
  const revisionActivation = useRevisionActivation();
  const router = useRouter();
  const { activeRevision, archivedAt, saved, uses, secretRotatedAt, error } = page;
  const history = useRevisionPages(page.history, (cursor) => api.webhookEndpointRevisions(endpointKey, cursor));
  /* The draft belongs to the page it was edited from: a save, rollback,
   * or archive reloads the page and the editor shows the saved revision. */
  const [edit, setEdit] = useState<{ base: WebhookEndpointPage; draft: EndpointDraft } | null>(null);
  const draft = edit?.base === page ? edit.draft : saved;
  const setDraft = (next: EndpointDraft) => setEdit({ base: page, draft: next });
  const [saving, setSaving] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [revealed, setRevealed] = useState<{ secret: string; rotated: boolean } | null>(null);
  const [tested, setTested] = useState<{ revision: WebhookEndpointRevision; result: WebhookEndpointTest } | null>(null);
  const [testing, setTesting] = useState<number | null>(null);
  const [activating, setActivating] = useState<number | null>(null);
  /* The revision opened read-only from the history: the full URL, which
   * the table truncates. */
  const [viewing, setViewing] = useState<WebhookEndpointRevision | null>(null);
  const isNew = page.history.revisions.length === 0;

  const dirtyCount = useMemo(() => {
    if (!draft || !saved) return 0;
    let count = 0;
    if (draft.name !== saved.name) count += 1;
    if (draft.url !== saved.url) count += 1;
    /* A brand-new endpoint has no revision to keep; saving is the only
     * way to make it exist. */
    if (count === 0 && isNew && draft.url.trim() !== "") count = 1;
    return count;
  }, [draft, saved, isNew]);

  function fail(title: string, reason: unknown) {
    toast.add({ type: "deny", title, description: reason instanceof Error ? reason.message : undefined });
  }

  async function save(current: EndpointDraft) {
    setSaving(true);
    try {
      const created = await api.createWebhookEndpointRevision(endpointKey, {
        name: current.name.trim(),
        url: current.url.trim(),
      });
      await revisionActivation.offer({
        revision: created.revision,
        activeRevision,
        canActivate: true,
        activationDescription: "Once active, new webhook deliveries use this revision.",
        activate: () => api.activateWebhookEndpointRevision(endpointKey, created.revision, activeRevision),
      });
      await router.invalidate();
      if (created.signing_secret) {
        setRevealed({ secret: created.signing_secret, rotated: false });
      }
    } catch (reason) {
      fail("Save failed", reason);
    } finally {
      setSaving(false);
    }
  }

  /* The editor takes this revision's destination as its draft; saving
   * creates a new revision from it. */
  function startFrom(revision: WebhookEndpointRevision) {
    setDraft({ name: revision.name, url: revision.url });
    toast.add({
      title: `Editing from revision ${revision.revision}`,
      description: "Save to create a new revision.",
    });
  }

  async function activate(revision: WebhookEndpointRevision) {
    setActivating(revision.revision);
    try {
      await api.activateWebhookEndpointRevision(endpointKey, revision.revision, activeRevision);
      toast.add({
        title: `Revision ${revision.revision} active`,
        description: "New deliveries go to it.",
      });
      await router.invalidate();
    } catch (reason) {
      fail("Activation failed", reason);
    } finally {
      setActivating(null);
    }
  }

  async function archive() {
    if (activeRevision === null) return;
    try {
      await api.archiveWebhookEndpoint(endpointKey, activeRevision);
      toast.add({ title: "Webhook endpoint archived" });
      setArchiving(false);
      await router.invalidate();
    } catch (reason) {
      if (reason instanceof RequestError && reason.code === "webhook_endpoint_in_use") {
        /* Another administrator activated a pipeline revision posting
         * here after the page loaded; reload so the dialog lists it. */
        await router.invalidate();
        toast.add({
          title: "Archive refused",
          description: "An active pipeline posts to this endpoint.",
          type: "deny",
        });
        return;
      }
      fail("Archive failed", reason);
    }
  }

  async function rotate() {
    try {
      const rotated = await api.rotateWebhookEndpointSecret(endpointKey);
      setRotating(false);
      await router.invalidate();
      setRevealed({ secret: rotated.signing_secret, rotated: true });
    } catch (reason) {
      fail("The secret could not be rotated", reason);
    }
  }

  async function runTest(revision: WebhookEndpointRevision) {
    setTesting(revision.revision);
    try {
      const result = await api.testWebhookEndpointRevision(endpointKey, revision.revision);
      setTested({ revision, result: result.test });
    } catch (reason) {
      fail("The test delivery could not be sent", reason);
    } finally {
      setTesting(null);
    }
  }

  if (error || !draft) {
    return (
      <Page width="narrow" className="gap-3">
        <PageHeader back={<PageBackLink to="/webhooks">Webhook endpoints</PageBackLink>} />
        <p className="text-body text-ink-muted">{error || "Unable to load the webhook endpoint."}</p>
      </Page>
    );
  }

  const active = history.items.find((revision) => revision.is_active);
  /* With no revision active the editor shows the latest one, saved as a
   * draft or left behind by an archive; the header offers to activate
   * it. Unsaved edits hide the offer, since saving them opens the same
   * choice for the new revision. */
  const latest = history.items[0] ?? null;
  const activatable = !isNew && activeRevision === null && dirtyCount === 0 ? latest : null;

  return (
    <Page width="narrow">
      {revisionActivation.dialog}
      <PageHeader back={<PageBackLink to="/webhooks">Webhook endpoints</PageBackLink>}>
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-title-lg text-ink">Webhook endpoint</h1>
            <p className="mt-1.5 max-w-[560px] text-body text-ink-muted">
              Configure the webhook settings.
            </p>
          </div>
          <div className="flex items-center gap-2">
            {activatable ? (
              <Button
                variant="primary"
                size="sm"
                loading={activating !== null}
                onClick={() => void activate(activatable)}
              >
                Activate revision {activatable.revision}
              </Button>
            ) : null}
            {!isNew ? (
              <Menu
                actions={[
                  {
                    id: "test",
                    label: testing !== null ? "Sending test delivery" : "Send test delivery",
                    disabled: !active || testing !== null,
                    action: () => active && void runTest(active),
                  },
                  { id: "rotate", label: "Rotate secret", action: () => setRotating(true) },
                  {
                    id: "archive",
                    label: "Archive endpoint",
                    danger: true,
                    disabled: activeRevision === null,
                    action: () => setArchiving(true),
                  },
                ]}
              >
                <MenuTrigger
                  render={
                    <IconButton aria-label="Manage endpoint" size="sm">
                      <EllipsisIcon width={15} height={15} />
                    </IconButton>
                  }
                />
                <MenuContent />
              </Menu>
            ) : null}
          </div>
        </div>
      </PageHeader>

      <div className="flex flex-wrap items-center gap-3">
        <TextField
          aria-label="Endpoint name"
          value={draft.name}
          onChange={(event) => setDraft({ ...draft, name: event.target.value })}
          placeholder="Fraud service"
          className="w-72"
        />
        <span className="font-mono text-[12px] text-ink-faint">{endpointKey}</span>
        <span className="text-micro font-normal text-ink-faint">
          {activeRevision !== null
            ? `active revision ${activeRevision}`
            : archivedAt
              ? `archived ${new Date(archivedAt).toLocaleDateString()}`
              : isNew
                ? "not saved yet"
                : "draft, no active revision"}
        </span>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Destination</CardTitle>
            <CardDescription>
              The URL that receives deliveries.
            </CardDescription>
          </div>
        </CardHeader>
        <CardBody>
          <TextField
            label="URL"
            mono
            value={draft.url}
            onChange={(event) => setDraft({ ...draft, url: event.target.value })}
            placeholder="https://fraud.example.com/hooks/approval"
          />
          {isNew ? (
            <p className="mt-3 text-micro text-ink-muted">
              The signing secret is shown once, after you save.
            </p>
          ) : secretRotatedAt ? (
            <p className="mt-3 text-micro text-ink-muted">
              Signing secret last rotated {new Date(secretRotatedAt).toLocaleDateString()}.
            </p>
          ) : null}
        </CardBody>
      </Card>

      {!isNew ? (
        <Card>
          <RevisionHistory
            aria-label="Webhook endpoint revisions"
            items={history.items}
            totalCount={history.totalCount}
            hasMore={history.hasMore}
            loadingMore={history.loadingMore}
            onLoadMore={history.loadMore}
            summaryLabel="Destination"
            summary={(revision) => (
              <>
                <span className="text-ink" title={revision.name}>
                  {revision.name}
                </span>
                <span className="ml-2 font-mono text-[11px] text-ink-muted" title={revision.url}>
                  {revision.url}
                </span>
              </>
            )}
            activeRevision={activeRevision}
            onOpen={setViewing}
            onActivate={(revision) => void activate(revision)}
            activating={activating}
            onCreateFrom={startFrom}
            menuActions={(revision) => [
              {
                id: "test",
                label: testing === revision.revision ? "Sending test delivery" : "Send test delivery",
                disabled: testing !== null,
                action: () => void runTest(revision),
              },
            ]}
          />
        </Card>
      ) : null}

      {!isNew ? (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Used by</CardTitle>
              <CardDescription>
                Active pipelines that post to this endpoint.
              </CardDescription>
            </div>
          </CardHeader>
          <CardBody>
            {uses.length === 0 ? (
              <p className="text-body text-ink-muted">No active pipeline posts to this endpoint.</p>
            ) : (
              <PipelineUseList uses={uses} />
            )}
          </CardBody>
        </Card>
      ) : null}

      {archiving && uses.length > 0 ? (
        <Dialog open onOpenChange={(open) => !open && setArchiving(false)}>
          <DialogContent>
            <DialogTitle>This endpoint is in use</DialogTitle>
            <DialogDescription>
              {uses.length === 1
                ? "An active pipeline posts to this endpoint. Remove its webhook block first."
                : `${uses.length} active pipeline blocks post to this endpoint. Remove those webhook blocks first.`}
            </DialogDescription>
            <DialogBody>
              <PipelineUseList uses={uses} />
            </DialogBody>
            <DialogFooter>
              <DialogClose render={<Button variant="ghost">Close</Button>} />
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : archiving ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setArchiving(false)}
          title="Archive this webhook endpoint?"
          description="Pipelines cannot use it until you activate a revision again."
          confirmLabel="Archive endpoint"
          onConfirm={archive}
        />
      ) : null}
      {rotating ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setRotating(false)}
          title="Rotate the signing secret?"
          description="The current secret stops working immediately. Update the receiver with the new secret."
          confirmLabel="Rotate secret"
          tone="danger"
          onConfirm={rotate}
        />
      ) : null}
      {revealed ? (
        <SecretDialog
          name={draft.name || endpointKey}
          secret={revealed.secret}
          rotated={revealed.rotated}
          onClose={() => setRevealed(null)}
        />
      ) : null}
      {tested ? (
        <TestResultDialog revision={tested.revision} result={tested.result} onClose={() => setTested(null)} />
      ) : null}
      {viewing ? (
        <Dialog open onOpenChange={(open) => !open && setViewing(null)}>
          <DialogContent>
            <DialogTitle>
              Revision {viewing.revision} · {viewing.name}
            </DialogTitle>
            <DialogDescription>
              {viewing.is_active
                ? "This revision is active."
                : "This revision is inactive."}
            </DialogDescription>
            <DialogBody>
              <DescriptionList>
                <DescriptionItem term="URL" mono className="break-all">
                  {viewing.url}
                </DescriptionItem>
                <DescriptionItem term="Host" copy={false}>
                  {hostOf(viewing.url)}
                </DescriptionItem>
                <DescriptionItem term="Created" copy={false}>
                  {new Date(viewing.created_at).toLocaleString()}
                </DescriptionItem>
              </DescriptionList>
            </DialogBody>
            <DialogFooter>
              <Button
                variant="ghost"
                loading={testing === viewing.revision}
                disabled={testing !== null}
                onClick={() => void runTest(viewing)}
              >
                Test delivery
              </Button>
              <DialogClose render={<Button variant="ghost">Close</Button>} />
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}

      <SaveBar
        count={dirtyCount}
        saving={saving}
        onDiscard={() => saved && setDraft(saved)}
        onSave={() => void save(draft)}
      />
    </Page>
  );
}

/* The one place the secret is ever visible. Mirrors the agent credential
 * dialog: the value sits in a code well, never a field, and the footer's
 * primary action is the copy itself. */
function SecretDialog({
  name,
  secret,
  rotated,
  onClose,
}: {
  name: string;
  secret: string;
  rotated: boolean;
  onClose: () => void;
}) {
  const { copy, copied } = useCopyToClipboard(secret);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogTitle>
          {rotated ? "New signing secret" : "Signing secret"} for {name}
        </DialogTitle>
        <DialogDescription>
          {rotated
            ? "The previous secret no longer works. Paste this one into the receiver now. It is shown only once."
            : "Paste it into the receiver now. It is shown only once."}
        </DialogDescription>
        <DialogBody className="flex flex-col gap-5">
          <CodeBlock label="signing secret" code={secret} />
          <div className="flex flex-col gap-2">
            <p className="text-label text-ink-secondary">
              Verifying deliveries
            </p>
            <p className="text-micro text-ink-muted">
              Every delivery carries this header:
            </p>
            <CodeBlock
              copyable={false}
              code={[
                "WithHuman-Signature: t=<unix seconds>,v1=<digest>",
                'digest = hex(HMAC-SHA256(secret, "<t>.<raw body>"))',
              ].join("\n")}
            />
            <p className="text-micro text-ink-muted">
              Compute the digest over the raw body, before parsing it, and compare it with v1.
              Reject deliveries whose timestamp is more than five minutes old.
            </p>
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Done
          </Button>
          <Button variant="primary" onClick={copy}>
            {/* The resting label sizes the button so "Copied" never shifts Done. */}
            <span className="grid">
              <span aria-hidden className="invisible col-start-1 row-start-1 inline-flex items-center gap-1.5">
                <CopyIcon width={14} height={14} />
                Copy secret
              </span>
              <span className="col-start-1 row-start-1 inline-flex items-center justify-center gap-1.5">
                {copied ? <CheckIcon width={14} height={14} /> : <CopyIcon width={14} height={14} />}
                {copied ? "Copied" : "Copy secret"}
              </span>
            </span>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function TestResultDialog({
  revision,
  result,
  onClose,
}: {
  revision: WebhookEndpointRevision;
  result: WebhookEndpointTest;
  onClose: () => void;
}) {
  const ok = !result.problem;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogTitle>Test delivery to revision {revision.revision}</DialogTitle>
        <DialogDescription>
          A signed webhook_endpoint.test payload was sent to {hostOf(revision.url)}.
        </DialogDescription>
        <DialogBody className="flex flex-col gap-3">
          <Callout
            tone={ok ? "approve" : "deny"}
            icon={ok ? undefined : <AlertIcon width={15} height={15} />}
            title={ok ? "The endpoint answered with a valid outcome" : result.message ?? "The delivery failed"}
          >
            <span className="font-mono text-[12px]">
              {result.status_code ? `HTTP ${result.status_code}` : "no response"} · {result.duration_ms} ms
              {ok && result.outcome ? ` · outcome ${result.outcome}` : ""}
              {ok && result.reason ? ` · "${result.reason}"` : ""}
            </span>
          </Callout>
        </DialogBody>
        <DialogFooter>
          <Button variant="primary" onClick={onClose}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
