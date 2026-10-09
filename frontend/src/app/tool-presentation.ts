import type {
  PresentationField,
  PresentationKind,
  ToolPresentationBinding,
  ToolPresentationSpec,
} from "../types";
import { humanizeKey } from "./format";

/*
 * Turning a request's arguments into something a reviewer reads. The
 * arguments are the action: a presentation only says
 * which of them is an email body, a shell command, or a pair of strings
 * that is really a diff, and what to call the request. Two rules hold
 * whatever the presentation says: nothing is hidden (an argument no
 * field names still shows, rendered by its shape) and the raw JSON is
 * one click away. The same plan feeds the request page and the
 * presentation editor's preview, so what an administrator sees while
 * editing is what reviewers get.
 */

/* One argument, ready to draw. */
export interface RenderedField {
  /* Stable within one plan: the pointer, or the pair's pointers. */
  key: string;
  label: string;
  kind: PresentationKind;
  language?: string;
  /* The value for every kind but a diff pair and a list. */
  value?: unknown;
  /* A diff pair. */
  before?: string;
  after?: string;
  /* A list's elements, each rendered with the item field. */
  items?: RenderedField[];
  /* Unconsumed fields within a list element. */
  additional?: RenderedField[];
  /* Where the field came from: a named field of the presentation, or
   * an argument the presentation did not name. */
  named: boolean;
}

export interface ArgumentPlan {
  /* The main block: named primary fields, then unnamed arguments. */
  primary: RenderedField[];
  /* Folded away under the main block. */
  detail: RenderedField[];
}

/* RFC 6901: "/a/b~1c" walks a, then "b/c". */
export function resolvePointer(root: unknown, pointer: string): { found: boolean; value?: unknown } {
  if (pointer === "") return { found: true, value: root };
  if (!pointer.startsWith("/")) return { found: false };
  let current: unknown = root;
  for (const raw of pointer.slice(1).split("/")) {
    const token = raw.replace(/~1/g, "/").replace(/~0/g, "~");
    if (Array.isArray(current)) {
      if (!/^(0|[1-9][0-9]*)$/.test(token)) return { found: false };
      const index = Number(token);
      if (index >= current.length) return { found: false };
      current = current[index];
    } else if (current && typeof current === "object") {
      if (!Object.prototype.hasOwnProperty.call(current, token)) return { found: false };
      current = (current as Record<string, unknown>)[token];
    } else {
      return { found: false };
    }
  }
  return { found: true, value: current };
}

/* A pointer's last token as a label: "/file_path" reads "File path". */
function labelFor(pointer: string): string {
  const last = pointer.split("/").pop() ?? pointer;
  return humanizeKey(last.replace(/~1/g, "/").replace(/~0/g, "~") || pointer);
}

function absolute(base: string, relative: string | undefined): string {
  if (!relative) return base;
  return `${base}/${relative}`;
}

const HTML_TAG = /<\/?(?:div|p|br|a|span|table|tr|td|ul|ol|li|html|body|h[1-6]|b|i|strong|em|img|blockquote)\b[^>]*>/i;
const UNIFIED_DIFF = /^(?:--- |\+\+\+ |@@ |\*\*\* Begin Patch|diff --git |Index: )/m;
const URL_VALUE = /^https?:\/\/\S+$/i;
const EMAIL_VALUE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const DATETIME_VALUE = /^\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?$/;
const PATH_VALUE = /^(?:~?\/|\.{1,2}\/)[^\s]*$/;
const IDENTIFIER_VALUE = /^[A-Za-z0-9_-]{8,}$/;
const SHELL_KEYS = /^(command|cmd|shell|script)$/i;
const PATH_KEYS = /(^|_)(path|file|filename|dir|directory|cwd)$/i;
const CODE_KEYS = /^(content|contents|source|code|new_source|patch|input)$/i;
const ENUM_KEYS = /(^|_)(type|mode|status|state|kind|priority|method|action|level|visibility|severity)$/i;
const REFERENCE_KEYS = /(^|_)(id|ids|sha|hash|token|ts)$/i;
const EMAIL_KEYS = /(^|_)(email|from|to|cc|bcc|sender|recipient)s?$/i;
const URL_KEYS = /(^|_)(url|uri|link|href)$/i;
const DATETIME_KEYS = /(^|_)(at|time|date|start|end|until|since|deadline)$/i;
const LONG_TEXT = 80;

/* What an argument looks like, from its key and its value, when the
 * presentation says nothing about it. The value decides where it can
 * (an HTML body, a diff, a URL); the key decides the rest (a
 * "command", an "_id"). A wrong guess only changes the framing: the
 * value is still shown whole. */
export function inferKind(key: string, value: unknown): PresentationKind {
  if (value === null || value === undefined) return "text";
  if (Array.isArray(value)) {
    return value.every((item) => item === null || typeof item !== "object") ? "list" : "json";
  }
  if (typeof value === "object") return "json";
  if (typeof value !== "string") return "text";
  const text = value;
  const multiline = text.includes("\n");
  if (HTML_TAG.test(text)) return "html";
  if (UNIFIED_DIFF.test(text) && multiline) return "diff";
  if (SHELL_KEYS.test(key)) return "shell";
  if (!multiline && URL_VALUE.test(text)) return "url";
  if (!multiline && EMAIL_VALUE.test(text)) return "email";
  if (!multiline && DATETIME_KEYS.test(key) && DATETIME_VALUE.test(text)) return "datetime";
  if (!multiline && (PATH_KEYS.test(key) || PATH_VALUE.test(text)) && !text.includes(" ")) return "path";
  if (CODE_KEYS.test(key) && (multiline || text.length > LONG_TEXT)) return "code";
  if (!multiline && REFERENCE_KEYS.test(key) && IDENTIFIER_VALUE.test(text)) return "reference";
  if (!multiline && ENUM_KEYS.test(key) && text.length <= 32 && !text.includes(" ")) return "enum";
  if (multiline || text.length > LONG_TEXT) return "prose";
  return "text";
}

function byShape(key: string, label: string, value: unknown): RenderedField {
  const kind = inferKind("", value);
  return kind === "list" && Array.isArray(value)
    ? { ...renderList(key, label, value, undefined), named: false }
    : { key, label, kind, value, named: false };
}

function renderList(key: string, label: string, elements: unknown[], item: PresentationField | undefined): RenderedField {
  const items = elements.map((element, index) => {
    const elementKey = `${key}/${index}`;
    const fallback = () => byShape(elementKey, String(index + 1), element);
    if (!item) return fallback();
    const rendered = renderField(element, item, elementKey, String(index + 1), true);
    if (!rendered) return fallback();
    const pointers = [item.path, item.before, item.after].filter((path): path is string => !!path);
    if (pointers.length && element && typeof element === "object") {
      const additional = Array.isArray(element)
        ? [byShape(`${elementKey}/remaining`, "Complete item", element)]
        : Object.entries(element).flatMap(([key, value]) => {
            const escaped = key.replace(/~/g, "~0").replace(/\//g, "~1");
            return pointers.includes(escaped) ? [] : [byShape(`${elementKey}/${escaped}`, humanizeKey(key), value)];
          });
      if (additional.length) rendered.additional = additional;
    }
    return rendered;
  });
  return { key, label, kind: "list", items, value: elements, named: true };
}

/* Renders one field of a presentation against a value: the whole
 * arguments for a top-level field, one element for a list's item.
 * Returns null when the field's path is absent, so a presentation
 * written for one shape of a tool's payload never invents anything for
 * another. */
function renderField(root: unknown, field: PresentationField, base: string, fallbackLabel: string, item: boolean): RenderedField | null {
  if (field.kind === "diff" && field.before && field.after) {
    const before = resolvePointer(root, item ? `/${field.before}` : field.before);
    const after = resolvePointer(root, item ? `/${field.after}` : field.after);
    if (typeof before.value !== "string" || typeof after.value !== "string") return null;
    return {
      key: item ? base : `${field.before}+${field.after}`,
      label: field.label || "Change",
      kind: "diff",
      before: before.value,
      after: after.value,
      named: true,
    };
  }
  const pointer = item ? (field.path ? `/${field.path}` : "") : (field.path ?? "");
  const resolved = resolvePointer(root, pointer);
  if (!resolved.found) return null;
  const key = item ? absolute(base, field.path) : pointer;
  const label = field.label || (field.path ? labelFor(field.path) : fallbackLabel);
  if (field.kind === "list") {
    return Array.isArray(resolved.value)
      ? renderList(key, label, resolved.value, field.item)
      : byShape(key, label, resolved.value);
  }
  if (field.kind !== "json" && typeof resolved.value !== "string") return byShape(key, label, resolved.value);
  return {
    key,
    label,
    kind: field.kind,
    language: field.language,
    value: resolved.value,
    named: true,
  };
}

/* The rendering plan for one request: the presentation's fields in
 * order, then every top-level argument the presentation did not name,
 * rendered by shape. An argument counts as named when any field read
 * it, at the top level or through a pointer underneath it, so a
 * presentation that renders /edits/0/old_string still leaves /edits
 * out of the unnamed list only if it named /edits itself: a partial
 * reading of a nested value keeps the whole value on the page. */
export function planArguments(args: unknown, spec: ToolPresentationSpec | null | undefined): ArgumentPlan {
  const primary: RenderedField[] = [];
  const detail: RenderedField[] = [];
  const named = new Set<string>();
  for (const field of spec?.fields ?? []) {
    const rendered = renderField(args, field, "", "", false);
    if (!rendered) continue;
    (field.role === "detail" ? detail : primary).push(rendered);
    for (const pointer of [field.path, field.before, field.after]) {
      if (pointer) named.add(pointer);
    }
  }
  if (args && typeof args === "object" && !Array.isArray(args)) {
    for (const [key, value] of Object.entries(args as Record<string, unknown>)) {
      const pointer = `/${key.replace(/~/g, "~0").replace(/\//g, "~1")}`;
      if (named.has(pointer)) continue;
      const kind = inferKind(key, value);
      primary.push(
        kind === "list"
          ? renderList(pointer, humanizeKey(key), value as unknown[], undefined)
          : { key: pointer, label: humanizeKey(key), kind, value, named: false },
      );
    }
  } else if (args !== undefined) {
    /* Not an object: the payload is one value. */
    primary.push(byShape("", "Arguments", args));
  }
  return { primary, detail };
}

/* One piece of a rendered title: static words from the presentation, or
 * an argument value interpolated into it, which the page sets apart so
 * a reader can tell the catalog's words from the agent's. */
export type TitleSegment = { kind: "text"; text: string } | { kind: "value"; text: string };

const TITLE_VALUE_MAX = 60;
const PLACEHOLDER = /\{\{([^{}]*)\}\}/g;

/* The request's title from the presentation's template, or null when
 * there is no template or every placeholder came up empty, in which
 * case the humanised tool name stands. A placeholder whose argument is
 * missing or not a scalar is dropped, together with the whitespace
 * around it, so "Edit {{/file_path}}" without a file path reads "Edit". */
export function renderTitle(template: string | undefined, args: unknown): TitleSegment[] | null {
  if (!template) return null;
  const segments: TitleSegment[] = [];
  let last = 0;
  let placeholders = 0;
  let resolved = 0;
  for (const match of template.matchAll(PLACEHOLDER)) {
    placeholders++;
    const text = template.slice(last, match.index);
    const found = resolvePointer(args, match[1].trim());
    const scalar = found.found && found.value !== null && found.value !== undefined && typeof found.value !== "object";
    if (scalar) {
      resolved++;
      if (text) segments.push({ kind: "text", text });
      let value = String(found.value);
      if (value.length > TITLE_VALUE_MAX) value = `${value.slice(0, TITLE_VALUE_MAX - 1)}…`;
      segments.push({ kind: "value", text: value });
    } else if (text.trim()) {
      segments.push({ kind: "text", text: text.replace(/\s+$/, " ") });
    }
    last = match.index + match[0].length;
  }
  const tail = template.slice(last);
  if (tail.trim() || (segments.length > 0 && tail)) segments.push({ kind: "text", text: tail });
  if (placeholders > 0 && resolved === 0) return null;
  const collapsed = collapseSegments(segments);
  return collapsed.length > 0 ? collapsed : null;
}

/* Trims the ends and joins adjacent static runs, so a dropped
 * placeholder never leaves two spaces or a dangling one. */
function collapseSegments(segments: TitleSegment[]): TitleSegment[] {
  const out: TitleSegment[] = [];
  for (const segment of segments) {
    const previous = out[out.length - 1];
    if (segment.kind === "text" && previous?.kind === "text") {
      previous.text += segment.text;
    } else {
      out.push({ ...segment });
    }
  }
  for (const segment of out) {
    if (segment.kind === "text") segment.text = segment.text.replace(/\s{2,}/g, " ");
  }
  if (out[0]?.kind === "text") out[0].text = out[0].text.replace(/^\s+/, "");
  const end = out[out.length - 1];
  if (end?.kind === "text") end.text = end.text.replace(/\s+$/, "");
  return out.filter((segment) => segment.text !== "");
}

/* The title as plain text, for a list row or the document title. */
export function titleText(segments: TitleSegment[]): string {
  return segments.map((segment) => segment.text).join("");
}

/* The server's own rule for which entry presents a call, mirrored so
 * the settings pages can say which shipped entry an organization entry
 * overrides: an exact tool, an empty runtime or server matching any,
 * the most specific match winning with a server binding worth more
 * than a runtime one, and the first of equals. */
export function matchesBinding(binding: ToolPresentationBinding, selector: ToolPresentationBinding): boolean {
  if (binding.tool !== selector.tool) return false;
  if (binding.runtime && binding.runtime !== (selector.runtime ?? "")) return false;
  if (binding.server && binding.server !== (selector.server ?? "")) return false;
  return true;
}

export function resolveBinding<Entry extends ToolPresentationBinding>(entries: Entry[], selector: ToolPresentationBinding): Entry | null {
  let best: Entry | null = null;
  let bestScore = -1;
  for (const entry of entries) {
    if (!matchesBinding(entry, selector)) continue;
    const score = (entry.server ? 2 : 0) + (entry.runtime ? 1 : 0);
    if (score > bestScore) {
      best = entry;
      bestScore = score;
    }
  }
  return best;
}

/* "Bash on claude-code", "create_issue from github", "update_deal": how
 * a binding reads in a list. */
export function describeBinding(binding: ToolPresentationBinding): string {
  const parts = [binding.tool];
  if (binding.server) parts.push(`from ${binding.server}`);
  if (binding.runtime) parts.push(`on ${binding.runtime}`);
  return parts.join(" ");
}
