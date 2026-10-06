import { useMemo, useState } from "react";
import { flushSync } from "react-dom";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { IconButton } from "@/ds/components/icon-button";
import { PageBackLink } from "@/ds/components/page-header";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/ds/components/select";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { TextArea } from "@/ds/components/text-area";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { UnsavedGuard } from "@/ds/components/unsaved-guard";
import { ChevronDownIcon, ChevronUpIcon, PlusIcon, TrashIcon } from "@/ds/icons";
import { api } from "../api";
import type {
  PresentationField,
  PresentationKind,
  PresentationRole,
  ToolPresentation,
  ToolPresentationInput,
  ToolPresentationsPage,
} from "../types";
import { ArgumentView } from "./argument-view";
import { humanizeTool } from "./format";
import { planArguments, renderTitle, resolveBinding } from "./tool-presentation";
import type { NewToolPresentationSearch } from "./tool-presentation-search";
import { useTitle } from "./use-title";

/*
 * Settings: one tool presentation. Which calls it applies to, what to
 * call them, and a field per argument worth framing: its path, kind,
 * and label. A sample of the arguments sits beside the form and the
 * preview under it is the very view reviewers get, so what is seen
 * here is what ships. Opened from a request, the sample is that
 * request's arguments and the fields start from whatever presented it.
 */

export interface ToolPresentationEditorData {
  page: ToolPresentationsPage | null;
  error: string;
  /* The entry being edited; null on the new page. */
  existing: ToolPresentation | null;
  /* The binding to start from on the new page. */
  start: NewToolPresentationSearch;
  /* Arguments to preview with, from the request the page was opened
   * from. */
  sample: unknown;
}

export async function loadNewToolPresentationPage(search: NewToolPresentationSearch): Promise<ToolPresentationEditorData> {
  const [page, request] = await Promise.all([
    api.toolPresentations().then(
      (page) => ({ page, error: "" }),
      (reason: unknown) => ({ page: null, error: reason instanceof Error ? reason.message : "Could not load tool presentations" }),
    ),
    search.request ? api.approval(search.request).catch(() => null) : Promise.resolve(null),
  ]);
  return { ...page, existing: null, start: search, sample: request?.arguments ?? null };
}

export async function loadToolPresentationEditPage(id: string, requestId?: string): Promise<ToolPresentationEditorData> {
  const page = await api.toolPresentations().then(
    (page) => ({ page, error: "" }),
    (reason: unknown) => ({ page: null, error: reason instanceof Error ? reason.message : "Could not load tool presentations" }),
  );
  const existing = page.page?.presentations.find((entry) => entry.id === id) ?? null;
  const request = requestId ? await api.approval(requestId).catch(() => null) : null;
  return {
    ...page,
    error: page.error || (existing ? "" : "This presentation no longer exists."),
    existing,
    start: {},
    sample: request?.arguments ?? null,
  };
}

const KIND_LABELS: Record<PresentationKind, string> = {
  text: "Text",
  prose: "Prose",
  markdown: "Markdown",
  html: "HTML",
  code: "Code",
  shell: "Shell command",
  diff: "Diff",
  path: "File path",
  url: "URL",
  email: "Email address",
  reference: "Identifier",
  enum: "Enum",
  datetime: "Date and time",
  list: "List",
  json: "JSON",
};

const KIND_HINTS: Partial<Record<PresentationKind, string>> = {
  html: "Rendered with scripts and external content blocked.",
  diff: "A patch as text at one path, or the pair of strings before and after a change.",
  list: "Paths in the element settings are relative to each element.",
  enum: "A short value shown as a badge.",
  reference: "An opaque identifier, in mono.",
};

const ROLE_LABELS: Record<PresentationRole, string> = { primary: "Main block", detail: "Details, folded" };

/* A diff reads a patch at one path, or lines up two strings. */
type DiffMode = "patch" | "pair";

interface FieldDraft {
  id: string;
  kind: PresentationKind;
  path: string;
  label: string;
  language: string;
  role: PresentationRole;
  diffMode: DiffMode;
  before: string;
  after: string;
  /* For a list: how each element renders. Empty kind means plain text
   * elements. */
  itemKind: PresentationKind | "";
  itemPath: string;
  itemDiffMode: DiffMode;
  itemBefore: string;
  itemAfter: string;
  itemLabel: string;
  itemLanguage: string;
}

interface Draft {
  tool: string;
  runtime: string;
  server: string;
  title: string;
  fields: FieldDraft[];
}

let fieldSequence = 0;
function nextFieldId(): string {
  fieldSequence += 1;
  return `field-${fieldSequence}`;
}

function emptyField(kind: PresentationKind = "text"): FieldDraft {
  return {
    id: nextFieldId(),
    kind,
    path: "",
    label: "",
    language: "",
    role: "primary",
    diffMode: "patch",
    before: "",
    after: "",
    itemKind: "",
    itemPath: "",
    itemDiffMode: "patch",
    itemBefore: "",
    itemAfter: "",
    itemLabel: "",
    itemLanguage: "",
  };
}

function fieldFromWire(field: PresentationField): FieldDraft {
  const item = field.item;
  return {
    ...emptyField(field.kind),
    path: field.path ?? "",
    label: field.label ?? "",
    language: field.language ?? "",
    role: field.role ?? "primary",
    diffMode: field.before ? "pair" : "patch",
    before: field.before ?? "",
    after: field.after ?? "",
    itemKind: item?.kind ?? "",
    itemPath: item?.path ?? "",
    itemDiffMode: item?.before ? "pair" : "patch",
    itemBefore: item?.before ?? "",
    itemAfter: item?.after ?? "",
    itemLabel: item?.label ?? "",
    itemLanguage: item?.language ?? "",
  };
}

function draftFromWire(entry: ToolPresentationInput): Draft {
  return {
    tool: entry.tool,
    runtime: entry.runtime ?? "",
    server: entry.server ?? "",
    title: entry.spec.title ?? "",
    fields: entry.spec.fields.map(fieldFromWire),
  };
}

function fieldToWire(field: FieldDraft): PresentationField {
  const out: PresentationField = { kind: field.kind };
  if (field.label.trim()) out.label = field.label.trim();
  if (field.role === "detail") out.role = "detail";
  if (field.kind === "diff" && field.diffMode === "pair") {
    out.before = field.before.trim();
    out.after = field.after.trim();
  } else {
    out.path = field.path.trim();
  }
  if (field.kind === "code" && field.language.trim()) out.language = field.language.trim();
  if (field.kind === "list" && field.itemKind) {
    const item: PresentationField = { kind: field.itemKind };
    if (field.itemLabel.trim()) item.label = field.itemLabel.trim();
    if (field.itemKind === "code" && field.itemLanguage.trim()) item.language = field.itemLanguage.trim();
    if (field.itemKind === "diff" && field.itemDiffMode === "pair") {
      item.before = field.itemBefore.trim();
      item.after = field.itemAfter.trim();
    } else if (field.itemPath.trim()) {
      item.path = field.itemPath.trim();
    }
    out.item = item;
  }
  return out;
}

function draftToWire(draft: Draft): ToolPresentationInput {
  const out: ToolPresentationInput = {
    tool: draft.tool.trim(),
    spec: { title: draft.title.trim(), fields: draft.fields.map(fieldToWire) },
  };
  if (draft.runtime.trim()) out.runtime = draft.runtime.trim();
  if (draft.server.trim()) out.server = draft.server.trim();
  return out;
}

/* What the page starts from: the entry being edited, else the shipped
 * entry the binding would resolve to, else an empty form with the
 * binding filled in. */
function startingDraft(data: ToolPresentationEditorData): Draft {
  if (data.existing) return draftFromWire(data.existing);
  const { tool = "", runtime = "", server = "" } = data.start;
  const builtIn = tool && data.page ? resolveBinding(data.page.built_in, { tool, runtime, server }) : null;
  if (builtIn) {
    const draft = draftFromWire(builtIn);
    /* A customization opened from a request starts with that call's
     * binding. The administrator can explicitly broaden it. */
    return { ...draft, runtime, server };
  }
  return { tool, runtime, server, title: "", fields: [] };
}

/* A sample worth previewing with: the request's arguments, or a
 * placeholder built from the draft's own paths so the preview is never
 * blank. */
function startingSample(data: ToolPresentationEditorData, draft: Draft): string {
  if (data.sample !== null && data.sample !== undefined) return JSON.stringify(data.sample, null, 2);
  const sample: Record<string, unknown> = {};
  for (const field of draft.fields) {
    for (const pointer of [field.path, field.before, field.after]) {
      const match = /^\/([^/]+)$/.exec(pointer);
      if (match && !(match[1] in sample)) sample[match[1]] = field.kind === "list" ? [] : "";
    }
  }
  return JSON.stringify(sample, null, 2);
}

/* Top-level pointers in the sample, offered as path completions. */
function pointersOf(sample: unknown): string[] {
  if (!sample || typeof sample !== "object" || Array.isArray(sample)) return [];
  return Object.keys(sample as Record<string, unknown>).map((key) => `/${key.replace(/~/g, "~0").replace(/\//g, "~1")}`);
}

/* The client-side checks worth doing before the round trip; the server
 * is the authority and its message is shown as it comes. */
function validate(draft: Draft): string {
  if (!draft.tool.trim()) return "Name the tool the presentation applies to.";
  if (/\s/.test(draft.tool.trim())) return "A tool name is one word.";
  for (const [index, field] of draft.fields.entries()) {
    const position = `Field ${index + 1}`;
    if (field.kind === "diff" && field.diffMode === "pair") {
      if (!field.before.trim().startsWith("/") || !field.after.trim().startsWith("/")) {
        return `${position}: before and after are JSON pointers such as /old_string and /new_string.`;
      }
    } else if (!field.path.trim().startsWith("/")) {
      return `${position}: the path is a JSON pointer such as /body.`;
    }
    if (field.kind === "list" && field.itemKind) {
      const relative = [field.itemPath, field.itemBefore, field.itemAfter].filter((value) => value.trim());
      if (relative.some((value) => value.trim().startsWith("/"))) {
        return `${position}: item paths are relative to the element, without a leading slash.`;
      }
      if (field.itemKind === "diff" && field.itemDiffMode === "pair" && (!field.itemBefore.trim() || !field.itemAfter.trim())) {
        return `${position}: an item diff pair needs both before and after.`;
      }
    }
  }
  return "";
}

export function ToolPresentationEditorScreen({ data, canEdit }: { data: ToolPresentationEditorData; canEdit: boolean }) {
  const editing = data.existing !== null;
  useTitle(editing ? "Edit tool presentation" : "New tool presentation");
  const router = useRouter();
  const navigate = useNavigate();
  const toast = useToast();
  const [saved, setSaved] = useState<Draft>(() => startingDraft(data));
  const [draft, setDraft] = useState<Draft>(saved);
  const [sampleText, setSampleText] = useState(() => startingSample(data, saved));
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState("");
  const kinds = data.page?.kinds ?? (Object.keys(KIND_LABELS) as PresentationKind[]);
  const dirty = JSON.stringify(draftToWire(draft)) !== JSON.stringify(draftToWire(saved));

  const sample = useMemo<{ value: unknown; error: string }>(() => {
    if (!sampleText.trim()) return { value: {}, error: "" };
    try {
      return { value: JSON.parse(sampleText), error: "" };
    } catch (reason) {
      return { value: null, error: reason instanceof Error ? reason.message : "Not valid JSON" };
    }
  }, [sampleText]);
  const wire = useMemo(() => draftToWire(draft), [draft]);
  const plan = useMemo(() => planArguments(sample.value, wire.spec), [sample.value, wire]);
  const title = useMemo(() => renderTitle(wire.spec.title, sample.value), [wire, sample.value]);
  const pointers = useMemo(() => pointersOf(sample.value), [sample.value]);
  const problem = validate(draft);
  const back = <PageBackLink to="/settings/tool-presentations">Tool presentations</PageBackLink>;

  function update(patch: Partial<Draft>) {
    setDraft((current) => ({ ...current, ...patch }));
  }

  function updateField(id: string, patch: Partial<FieldDraft>) {
    setDraft((current) => ({
      ...current,
      fields: current.fields.map((field) => (field.id === id ? { ...field, ...patch } : field)),
    }));
  }

  function moveField(id: string, direction: -1 | 1) {
    setDraft((current) => {
      const index = current.fields.findIndex((field) => field.id === id);
      const target = index + direction;
      if (index < 0 || target < 0 || target >= current.fields.length) return current;
      const fields = [...current.fields];
      [fields[index], fields[target]] = [fields[target], fields[index]];
      return { ...current, fields };
    });
  }

  function removeField(id: string) {
    setDraft((current) => ({ ...current, fields: current.fields.filter((field) => field.id !== id) }));
  }

  async function save() {
    if (problem) {
      setFailure(problem);
      return;
    }
    setSaving(true);
    setFailure("");
    try {
      if (data.existing) {
        const updated = await api.updateToolPresentation(data.existing.id, wire);
        const next = draftFromWire(updated);
        setSaved(next);
        setDraft(next);
        toast.add({ type: "approve", title: `Saved the presentation for ${updated.tool}` });
        await router.invalidate();
      } else {
        const created = await api.createToolPresentation(wire);
        /* The guard reads dirty from the last render, so the saved
         * state must land before the navigation it would otherwise
         * block. */
        flushSync(() => {
          setSaved(draft);
          setSaving(false);
        });
        toast.add({ type: "approve", title: `Added a presentation for ${created.tool}` });
        await navigate({ to: "/settings/tool-presentations/$presentationId", params: { presentationId: created.id }, replace: true });
      }
    } catch (reason) {
      setFailure(reason instanceof Error ? reason.message : "The presentation could not be saved");
    } finally {
      setSaving(false);
    }
  }

  if (!data.page || (editing && !data.existing)) {
    return (
      <SettingsPage title={editing ? "Edit tool presentation" : "New tool presentation"} back={back}>
        <Callout tone="warn" title="Unable to load the presentation">
          {data.error}
        </Callout>
      </SettingsPage>
    );
  }

  const saveButton = (
    <Button variant="primary" onClick={() => void save()} disabled={!canEdit || saving || (editing && !dirty)} loading={saving}>
      {editing ? "Save changes" : "Create presentation"}
    </Button>
  );

  return (
    <SettingsPage
      title={editing ? `Presentation for ${draft.tool || "a tool"}` : "New tool presentation"}
      description="Choose how reviewers see this tool's requests."
      back={back}
      action={saveButton}
    >
      <UnsavedGuard dirty={dirty && canEdit} saving={saving} onSave={() => void save()} onDiscard={() => setDraft(saved)} />
      {!canEdit ? (
        <Callout tone="neutral" title="Read only">
          Changing tool presentations needs the organization.settings.write permission.
        </Callout>
      ) : null}
      {failure ? (
        <Callout tone="deny" title="The presentation was not saved">
          {failure}
        </Callout>
      ) : null}

      <SettingsSection
        title="Applies to"
        description="Leave the server or runtime empty to match any."
      >
        <div className="grid grid-cols-1 gap-4 py-3 @md:grid-cols-3">
          <TextField
            label="Tool"
            mono
            value={draft.tool}
            onChange={(event) => update({ tool: event.target.value })}
            placeholder="create_or_update_draft"
            disabled={!canEdit}
            description="The exact tool name."
          />
          <TextField
            label="MCP server"
            mono
            value={draft.server}
            onChange={(event) => update({ server: event.target.value })}
            placeholder="Any"
            disabled={!canEdit}
            description="The server alias the adapter recorded."
          />
          <TextField
            label="Runtime"
            mono
            value={draft.runtime}
            onChange={(event) => update({ runtime: event.target.value })}
            placeholder="Any"
            disabled={!canEdit}
            description="The runtime, such as claude-code."
          />
        </div>
      </SettingsSection>

      <SettingsSection
        title="Title"
        description="Shown instead of the tool name. {{/path}} inserts an argument."
      >
        <div className="py-3">
          <TextField
            label="Title"
            value={draft.title}
            onChange={(event) => update({ title: event.target.value })}
            placeholder={draft.tool ? humanizeTool(draft.tool) : "Email draft"}
            disabled={!canEdit}
          />
        </div>
      </SettingsSection>

      <SettingsSection
        title="Fields"
        description="Add a field for each argument you want to label, in reading order."
        action={
          canEdit ? (
            <Button size="sm" onClick={() => update({ fields: [...draft.fields, emptyField()] })}>
              <PlusIcon width={13} height={13} />
              Add field
            </Button>
          ) : undefined
        }
        inset="none"
      >
        {draft.fields.length === 0 ? (
          <p className="px-5 py-6 text-center text-label text-ink-muted">
            No fields yet.
          </p>
        ) : (
          <ol className="flex flex-col">
            {draft.fields.map((field, index) => (
              <li key={field.id} className="border-b-hairline border-edge px-5 py-4 last:border-b-0">
                <FieldEditor
                  field={field}
                  index={index}
                  count={draft.fields.length}
                  kinds={kinds}
                  pointers={pointers}
                  disabled={!canEdit}
                  onChange={(patch) => updateField(field.id, patch)}
                  onMove={(direction) => moveField(field.id, direction)}
                  onRemove={() => removeField(field.id)}
                />
              </li>
            ))}
          </ol>
        )}
      </SettingsSection>

      <SettingsSection
        title="Preview"
        description="Paste sample arguments to see what reviewers will see."
      >
        <div className="flex flex-col gap-4 py-3">
          <TextArea
            label="Sample arguments (JSON)"
            value={sampleText}
            onChange={(event) => setSampleText(event.target.value)}
            rows={6}
            error={sample.error}
            className="font-mono text-[12px]"
            spellCheck={false}
          />
          <div className="flex flex-col overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
            <div className="flex min-h-10 items-center gap-2 border-b-hairline border-edge px-4 py-2">
              <span className="text-body font-medium text-ink">
                {title
                  ? title.map((segment, index) =>
                      segment.kind === "value" ? (
                        <span key={index} className="font-mono text-[0.85em] text-ink-secondary">
                          {segment.text}
                        </span>
                      ) : (
                        <span key={index}>{segment.text}</span>
                      ),
                    )
                  : humanizeTool(draft.tool || "tool")}
              </span>
            </div>
            {sample.error ? (
              <p className="px-4 py-3 text-label text-ink-muted">Fix the sample to see the preview.</p>
            ) : (
              <ArgumentView plan={plan} empty="The sample has no arguments." />
            )}
          </div>
        </div>
      </SettingsSection>
    </SettingsPage>
  );
}

function KindSelect({
  label,
  value,
  kinds,
  onChange,
  disabled,
  allowNone,
}: {
  label: string;
  value: PresentationKind | "";
  kinds: PresentationKind[];
  onChange: (kind: PresentationKind | "") => void;
  disabled: boolean;
  allowNone?: boolean;
}) {
  const items = allowNone ? ["", ...kinds] : kinds;
  const labelOf = (item: unknown) => (item === "" ? "Plain text" : (KIND_LABELS[item as PresentationKind] ?? String(item)));
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-label text-ink-secondary">{label}</span>
      <Select
        items={items}
        itemToStringLabel={labelOf}
        value={value}
        onValueChange={(next) => onChange((next as PresentationKind | "") ?? "")}
        disabled={disabled}
      >
        <SelectTrigger aria-label={label} />
        <SelectContent<string>>
          {(item) => (
            <SelectItem key={item || "none"} value={item}>
              {labelOf(item)}
            </SelectItem>
          )}
        </SelectContent>
      </Select>
    </div>
  );
}

function DiffModeSelect({ value, onChange, disabled }: { value: DiffMode; onChange: (mode: DiffMode) => void; disabled: boolean }) {
  const labels: Record<DiffMode, string> = { patch: "A patch at one path", pair: "Before and after" };
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-label text-ink-secondary">Diff source</span>
      <Select items={["patch", "pair"]} itemToStringLabel={(item) => labels[item as DiffMode]} value={value} onValueChange={(next) => onChange((next as DiffMode) ?? "patch")} disabled={disabled}>
        <SelectTrigger aria-label="Diff source" />
        <SelectContent<string>>{(item) => <SelectItem key={item} value={item}>{labels[item as DiffMode]}</SelectItem>}</SelectContent>
      </Select>
    </div>
  );
}

function FieldEditor({
  field,
  index,
  count,
  kinds,
  pointers,
  disabled,
  onChange,
  onMove,
  onRemove,
}: {
  field: FieldDraft;
  index: number;
  count: number;
  kinds: PresentationKind[];
  pointers: string[];
  disabled: boolean;
  onChange: (patch: Partial<FieldDraft>) => void;
  onMove: (direction: -1 | 1) => void;
  onRemove: () => void;
}) {
  const listId = `pointers-${field.id}`;
  const pair = field.kind === "diff" && field.diffMode === "pair";
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <span className="text-label text-ink-muted">Field {index + 1}</span>
        <span className="flex items-center gap-1">
          <IconButton size="sm" aria-label="Move up" disabled={disabled || index === 0} onClick={() => onMove(-1)}>
            <ChevronUpIcon width={14} height={14} />
          </IconButton>
          <IconButton size="sm" aria-label="Move down" disabled={disabled || index === count - 1} onClick={() => onMove(1)}>
            <ChevronDownIcon width={14} height={14} />
          </IconButton>
          <IconButton size="sm" aria-label="Remove field" disabled={disabled} onClick={onRemove}>
            <TrashIcon width={14} height={14} />
          </IconButton>
        </span>
      </div>
      <div className="grid grid-cols-1 gap-3 @md:grid-cols-3">
        <KindSelect label="Kind" value={field.kind} kinds={kinds} disabled={disabled} onChange={(kind) => onChange({ kind: kind || "text" })} />
        <TextField label="Label" value={field.label} onChange={(event) => onChange({ label: event.target.value })} placeholder="From the path" disabled={disabled} />
        <div className="flex flex-col gap-1.5">
          <span className="text-label text-ink-secondary">Place</span>
          <Select
            items={["primary", "detail"]}
            itemToStringLabel={(item) => ROLE_LABELS[item as PresentationRole]}
            value={field.role}
            onValueChange={(next) => onChange({ role: (next as PresentationRole) ?? "primary" })}
            disabled={disabled}
          >
            <SelectTrigger aria-label="Place" />
            <SelectContent<string>>{(item) => <SelectItem key={item} value={item}>{ROLE_LABELS[item as PresentationRole]}</SelectItem>}</SelectContent>
          </Select>
        </div>
      </div>
      <div className="grid grid-cols-1 gap-3 @md:grid-cols-3">
        {field.kind === "diff" ? <DiffModeSelect value={field.diffMode} disabled={disabled} onChange={(diffMode) => onChange({ diffMode })} /> : null}
        {pair ? (
          <>
            <TextField label="Before" mono value={field.before} onChange={(event) => onChange({ before: event.target.value })} placeholder="/old_string" list={listId} disabled={disabled} />
            <TextField label="After" mono value={field.after} onChange={(event) => onChange({ after: event.target.value })} placeholder="/new_string" list={listId} disabled={disabled} />
          </>
        ) : (
          <TextField label="Path" mono value={field.path} onChange={(event) => onChange({ path: event.target.value })} placeholder="/body" list={listId} disabled={disabled} description="A JSON pointer into the arguments." />
        )}
        {field.kind === "code" ? (
          <TextField label="Language" mono value={field.language} onChange={(event) => onChange({ language: event.target.value })} placeholder="python" disabled={disabled} description="A label for the reader." />
        ) : null}
      </div>
      {field.kind === "list" ? (
        <div className="grid grid-cols-1 gap-3 rounded-md border-hairline border-edge bg-canvas p-3 @md:grid-cols-3">
          <KindSelect label="Each element" value={field.itemKind} kinds={kinds.filter((kind) => kind !== "list")} allowNone disabled={disabled} onChange={(itemKind) => onChange({ itemKind })} />
          {field.itemKind ? <TextField label="Element label" value={field.itemLabel} onChange={(event) => onChange({ itemLabel: event.target.value })} disabled={disabled} /> : null}
          {field.itemKind === "code" ? <TextField label="Element language" value={field.itemLanguage} onChange={(event) => onChange({ itemLanguage: event.target.value })} disabled={disabled} /> : null}
          {field.itemKind === "diff" ? <DiffModeSelect value={field.itemDiffMode} disabled={disabled} onChange={(itemDiffMode) => onChange({ itemDiffMode })} /> : null}
          {field.itemKind === "diff" && field.itemDiffMode === "pair" ? (
            <>
              <TextField label="Before" mono value={field.itemBefore} onChange={(event) => onChange({ itemBefore: event.target.value })} placeholder="old_string" disabled={disabled} />
              <TextField label="After" mono value={field.itemAfter} onChange={(event) => onChange({ itemAfter: event.target.value })} placeholder="new_string" disabled={disabled} />
            </>
          ) : field.itemKind ? (
            <TextField label="Path in element" mono value={field.itemPath} onChange={(event) => onChange({ itemPath: event.target.value })} placeholder="Empty for the element itself" disabled={disabled} />
          ) : null}
        </div>
      ) : null}
      {KIND_HINTS[field.kind] ? <p className="text-micro text-ink-faint">{KIND_HINTS[field.kind]}</p> : null}
      <datalist id={listId}>
        {pointers.map((pointer) => (
          <option key={pointer} value={pointer} />
        ))}
      </datalist>
    </div>
  );
}
