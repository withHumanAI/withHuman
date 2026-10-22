import { Fragment, type ReactNode } from "react";
import { Badge } from "@/ds/components/badge";
import { CodeBlock } from "@/ds/components/code-block";
import { CopyButton } from "@/ds/components/copy-button";
import { DiffView } from "@/ds/components/diff-view";
import { Disclosure } from "@/ds/components/disclosure";
import { HtmlPreview } from "@/ds/components/html-preview";
import { Markdown } from "@/ds/components/markdown";
import { cn } from "@/ds/lib/cn";
import { formatAbsolute } from "./format";
import type { ArgumentPlan, RenderedField } from "./tool-presentation";

/*
 * The arguments of a request, drawn from a plan: short values as
 * label-and-value rows, long ones (a body, a command, a diff) as blocks
 * under their label. Every value is copyable as the text the tool will
 * receive. The plan decides what is shown and in what order; this file
 * only decides how each kind looks.
 */

/* Kinds that stand as a block under their label rather than beside it. */
const BLOCK_KINDS = new Set(["prose", "markdown", "html", "code", "shell", "diff", "json", "list"]);

/* A list of one-line values reads as a run beside its label; only a
 * list holding blocks stands under it. */
function isScalarList(field: RenderedField): boolean {
  return field.kind === "list" && (field.items ?? []).every((item) => !BLOCK_KINDS.has(item.kind) && !item.additional?.length);
}

function standsAsBlock(field: RenderedField): boolean {
  return BLOCK_KINDS.has(field.kind) && !isScalarList(field);
}

function textOf(value: unknown): string {
  if (value === null || value === undefined) return "none";
  if (typeof value === "string") return value;
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function copyTextOf(field: RenderedField): string {
  if (field.kind === "diff" && field.before !== undefined) return `--- before\n${field.before}\n+++ after\n${field.after ?? ""}`;
  const value = field.value;
  if (typeof value === "string") return value;
  if (value === null || value === undefined) return "";
  return JSON.stringify(value, null, 2);
}

/* An enum value as a badge, a datetime in the reader's locale, a link
 * shown but not followed: the one-line kinds. */
function InlineValue({ field }: { field: RenderedField }) {
  const value = field.value;
  const text = textOf(value);
  switch (field.kind) {
    case "enum":
      return typeof value === "string" && value !== "" ? (
        <Badge icon={false} fill>
          {value}
        </Badge>
      ) : (
        <span className="font-mono text-[12px] leading-5 text-ink">{text}</span>
      );
    case "datetime": {
      const parsed = typeof value === "string" ? new Date(value) : null;
      if (parsed && !Number.isNaN(parsed.getTime())) {
        return (
          <span className="text-body leading-5 text-ink">
            {formatAbsolute(value as string)}
            <span className="ml-1.5 font-mono text-[11px] text-ink-faint">{value as string}</span>
          </span>
        );
      }
      return <span className="font-mono text-[12px] leading-5 text-ink">{text}</span>;
    }
    case "url":
    case "email":
    case "path":
    case "reference":
      return <span className="font-mono text-[12px] leading-5 break-all text-ink">{text}</span>;
    case "text":
    default:
      return typeof value === "string" ? (
        <span className="text-body leading-5 break-words text-ink">{value === "" ? <span className="text-ink-faint">empty</span> : value}</span>
      ) : (
        <span className="font-mono text-[12px] leading-5 text-ink">{text}</span>
      );
  }
}

/* The block kinds, each in the component that reads best. */
function BlockValue({ field }: { field: RenderedField }) {
  const label = field.language ? `${field.label.toLowerCase()} · ${field.language}` : undefined;
  switch (field.kind) {
    case "diff":
      return field.before === undefined && field.after === undefined && typeof field.value === "string" ? (
        <DiffView patch={field.value} />
      ) : (
        <DiffView before={field.before} after={field.after} />
      );
    case "html":
      return <HtmlPreview html={textOf(field.value)} label="html" />;
    case "markdown":
      return (
        <div className="rounded-md border-hairline border-edge bg-canvas px-3.5 py-3 text-body text-ink">
          <Markdown>{textOf(field.value)}</Markdown>
        </div>
      );
    case "prose":
      return (
        <p className="rounded-md border-hairline border-edge bg-canvas px-3.5 py-3 text-body break-words whitespace-pre-wrap text-ink">
          {textOf(field.value)}
        </p>
      );
    case "shell":
      return <CodeBlock code={textOf(field.value)} wrap />;
    case "code":
      return <CodeBlock code={textOf(field.value)} label={label} />;
    case "json":
      return <CodeBlock code={JSON.stringify(field.value, null, 2)} />;
    case "list":
      return <ListValue field={field} />;
    default:
      return <CodeBlock code={textOf(field.value)} wrap />;
  }
}

/* A list: scalar elements as a compact run, block elements stacked
 * with their index. */
function ListValue({ field }: { field: RenderedField }) {
  const items = field.items ?? [];
  if (items.length === 0) {
    return <span className="text-label text-ink-faint">Empty list</span>;
  }
  if (isScalarList(field)) {
    return (
      <ul className="flex flex-wrap gap-x-3 gap-y-1">
        {items.map((item) => (
          <li key={item.key} className="flex items-center">
            <InlineValue field={item} />
          </li>
        ))}
      </ul>
    );
  }
  return (
    <ol className="flex flex-col gap-2">
      {items.map((item, index) => (
        <li key={item.key} className="flex flex-col gap-1">
          <span className="text-micro text-ink-faint">{index + 1}</span>
          {BLOCK_KINDS.has(item.kind) ? <BlockValue field={item} /> : <InlineValue field={item} />}
          {item.additional?.length ? <Fields fields={item.additional} /> : null}
        </li>
      ))}
    </ol>
  );
}

function FieldRow({ field }: { field: RenderedField }) {
  const block = standsAsBlock(field);
  /* The first line's height: a badge stands 24px tall, text 20px. The
   * label and the copy button center on it. */
  const line = field.kind === "enum" ? "h-6" : "h-5";
  const copyable = (
    <span className={`flex ${line} w-[26px] shrink-0 items-center justify-center opacity-0 transition-opacity duration-100 group-hover/item:opacity-100 has-[:focus-visible]:opacity-100 has-[[data-copied]]:opacity-100`}>
      <CopyButton value={copyTextOf(field)} aria-label={`Copy ${field.label.toLowerCase()}`} />
    </span>
  );
  if (block) {
    return (
      <div className="group/item flex flex-col gap-1.5">
        <div className="flex items-center gap-1">
          <span className="text-label leading-5 font-normal text-ink-muted">{field.label}</span>
          {field.kind !== "html" && field.kind !== "diff" ? copyable : null}
        </div>
        <BlockValue field={field} />
      </div>
    );
  }
  return (
    <div className="group/item grid grid-cols-[minmax(96px,max-content)_minmax(0,1fr)] gap-x-6 gap-y-1.5">
      <span className={`flex ${line} items-center text-label leading-5 font-normal text-ink-muted`}>{field.label}</span>
      <span className="flex min-w-0 items-start gap-1">
        <span className="min-w-0">
          {field.kind === "list" ? <ListValue field={field} /> : <InlineValue field={field} />}
        </span>
        {copyable}
      </span>
    </div>
  );
}

/* Rows of fields: the inline ones pack tightly, a block gets room. */
function Fields({ fields, className }: { fields: RenderedField[]; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-2.5", className)}>
      {fields.map((field) => (
        <Fragment key={field.key}>
          <FieldRow field={field} />
        </Fragment>
      ))}
    </div>
  );
}

export function ArgumentView({ plan, empty }: { plan: ArgumentPlan; empty?: ReactNode }) {
  if (plan.primary.length === 0 && plan.detail.length === 0) {
    return <div className="px-4 py-3 text-label text-ink-faint">{empty ?? "No arguments"}</div>;
  }
  /* Folding saves nothing for a single argument: it shows with the rest. */
  const folded = plan.detail.length > 1;
  const shown = folded ? plan.primary : [...plan.primary, ...plan.detail];
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      {shown.length > 0 ? <Fields fields={shown} /> : null}
      {folded ? (
        <Disclosure label={`${plan.detail.length} more arguments`}>
          <Fields fields={plan.detail} />
        </Disclosure>
      ) : null}
    </div>
  );
}
