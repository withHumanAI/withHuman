import { createContext, useContext, Fragment, useEffect, useRef, useState, type KeyboardEvent } from "react";
import { fieldHints, schemaWarning, suitableOperators, type FieldHint, type ToolSchema } from "@/ds/lib/tool-schema";
import { cn } from "@/ds/lib/cn";
import { Button } from "./button";
import { IconButton } from "./icon-button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "./select";
import { Referenced, type Reference } from "./reference";
import { TagInput } from "./tag-input";
import { TextField } from "./text-field";
import { InputSuggestions, useInputSuggestions } from "./input-suggestions";
import { ToggleGroup, ToggleGroupItem } from "./toggle-group";
import { PlusIcon, XIcon } from "@/ds/icons";

/*
 * The visual policy condition editor. It edits the same condition tree
 * JSON the pipeline API stores: a node is a leaf (field, operator,
 * value) or an all/any group of nodes. some/every array predicates stay
 * JSON-only for now and render as locked rows.
 */

export interface ConditionNode {
  id: string;
  field?: string;
  operator?: string;
  value?: unknown;
  all?: ConditionNode[];
  any?: ConditionNode[];
  some?: { field: string; where: ConditionNode };
  every?: { field: string; where: ConditionNode };
}

export type ConditionValueKind =
  | "none"
  | "scalar"
  | "text"
  | "regex"
  | "number"
  | "list";

export interface ConditionOperator {
  value: string;
  label: string;
  kind: ConditionValueKind;
}

/* Ordered for the picker: the common comparisons first, presence checks
 * last. Labels read as the middle of a sentence: "amount_cents at least
 * 1000". */
export const CONDITION_OPERATORS: ConditionOperator[] = [
  { value: "equals", label: "equals", kind: "scalar" },
  { value: "not_equals", label: "does not equal", kind: "scalar" },
  { value: "in", label: "is one of", kind: "list" },
  { value: "not_in", label: "is not one of", kind: "list" },
  { value: "less_than", label: "less than", kind: "number" },
  { value: "less_than_or_equal", label: "at most", kind: "number" },
  { value: "greater_than", label: "greater than", kind: "number" },
  { value: "greater_than_or_equal", label: "at least", kind: "number" },
  { value: "contains", label: "contains", kind: "text" },
  { value: "not_contains", label: "does not contain", kind: "text" },
  { value: "contains_any", label: "contains any of", kind: "list" },
  { value: "contains_all", label: "contains all of", kind: "list" },
  { value: "starts_with", label: "starts with", kind: "list" },
  { value: "ends_with", label: "ends with", kind: "text" },
  { value: "regex", label: "matches regex", kind: "regex" },
  { value: "is_true", label: "is true", kind: "none" },
  { value: "is_false", label: "is false", kind: "none" },
  { value: "is_empty", label: "is empty", kind: "none" },
  { value: "is_not_empty", label: "is not empty", kind: "none" },
  { value: "exists", label: "exists", kind: "none" },
  { value: "not_exists", label: "does not exist", kind: "none" },
  { value: "is_null", label: "is null", kind: "none" },
  { value: "is_not_null", label: "is not null", kind: "none" },
];

const OPERATOR_BY_VALUE = new Map(
  CONDITION_OPERATORS.map((operator) => [operator.value, operator]),
);

export function newConditionLeaf(): ConditionNode {
  return {
    id: crypto.randomUUID(),
    field: "",
    operator: "equals",
    value: "",
  };
}

export function newConditionGroup(kind: "all" | "any" = "any"): ConditionNode {
  return { id: crypto.randomUUID(), [kind]: [newConditionLeaf()] };
}

/*
 * A condition stores a JSON Pointer into the request document, but people
 * read and pick fields by name. The fixed fields the engine allows have
 * plain names; everything under the request's arguments is shown as the
 * dotted argument key, which is what the agent's payload calls it.
 */
export interface FieldDescription {
  /* The JSON Pointer the condition stores. */
  path: string;
  /* What a reviewer calls it: "Tool name", or the dotted argument key. */
  label: string;
  /* An argument key is machine content and renders mono; a fixed name does not. */
  argument: boolean;
}

export const FIXED_FIELDS: readonly { path: string; label: string }[] = [
  { path: "/request/tool", label: "Tool name" },
  { path: "/request/server", label: "MCP server" },
  { path: "/agent/slug", label: "Agent slug" },
  { path: "/agent/name", label: "Agent name" },
];

const FIXED_FIELD_LABELS = new Map(FIXED_FIELDS.map((field) => [field.path, field.label]));
const ARGUMENTS_PATH = "/request/arguments";

function unescapePointerToken(token: string): string {
  return token.replace(/~1/g, "/").replace(/~0/g, "~");
}

export function describeField(path: string): FieldDescription {
  const fixed = FIXED_FIELD_LABELS.get(path);
  if (fixed) return { path, label: fixed, argument: false };
  if (path === ARGUMENTS_PATH) return { path, label: "Arguments", argument: false };
  if (path.startsWith(`${ARGUMENTS_PATH}/`)) {
    const key = path.slice(ARGUMENTS_PATH.length + 1).split("/").map(unescapePointerToken).join(".");
    return { path, label: key || "argument", argument: true };
  }
  /* Anything else is shown exactly as typed. */
  return { path, label: path || "field", argument: true };
}

/* True when a person's search matches the field by name or by pointer. */
function fieldMatches(path: string, query: string): boolean {
  if (!query) return true;
  const field = describeField(path);
  return path.toLowerCase().includes(query) || field.label.toLowerCase().includes(query);
}

function valueSummary(operator: ConditionOperator | undefined, value: unknown): string {
  if (!operator || operator.kind === "none") return "";
  if (Array.isArray(value)) return value.map(String).join(", ");
  if (value === undefined || value === "") return "…";
  return String(value);
}

/* One line of plain language for a collapsed block header. */
export function summarizeCondition(node: ConditionNode, depth = 0): string {
  const children = node.all ?? node.any;
  if (children) {
    const joiner = node.all ? " and " : " or ";
    const inner = children
      .map((child) => summarizeCondition(child, depth + 1))
      .join(joiner);
    if (children.length <= 1 || depth === 0) return inner;
    return `(${inner})`;
  }
  if (node.some) return `some ${describeField(node.some.field).label} matches`;
  if (node.every) return `every ${describeField(node.every.field).label} matches`;
  const operator = node.operator ? OPERATOR_BY_VALUE.get(node.operator) : undefined;
  const parts = [
    describeField(node.field ?? "").label,
    operator?.label ?? node.operator ?? "",
    valueSummary(operator, node.value),
  ];
  return parts.filter(Boolean).join(" ");
}

/* Exact resource values identify references; partial matches and patterns do not. */
function conditionReference(field: string | undefined, value: string): Reference | null {
  switch (field) {
    case "/request/tool": return { kind: "tool", name: value };
    case "/agent/slug": return { kind: "agent", slug: value, name: value };
    case "/agent/name": return { kind: "agent", name: value };
    default: return null;
  }
}

export function ConditionSummary({ node, depth = 0 }: { node: ConditionNode; depth?: number }) {
  const children = node.all ?? node.any;
  if (children) {
    const grouped = children.length > 1 && depth > 0;
    return (
      <>
        {grouped ? "(" : ""}
        {children.map((child, index) => (
          <Fragment key={child.id}>
            {index > 0 ? (node.all ? " and " : " or ") : ""}
            <ConditionSummary node={child} depth={depth + 1} />
          </Fragment>
        ))}
        {grouped ? ")" : ""}
      </>
    );
  }
  if (node.some || node.every || !conditionReference(node.field, "") ||
      !["equals", "not_equals", "in", "not_in"].includes(node.operator ?? "")) {
    return <>{summarizeCondition(node, depth)}</>;
  }
  const values = Array.isArray(node.value) ? node.value : [node.value];
  const operator = OPERATOR_BY_VALUE.get(node.operator ?? "");
  return (
    <>
      {describeField(node.field ?? "").label} {operator?.label}{" "}
      {values.map((value, index) => {
        const reference = typeof value === "string" && value.trim()
          ? conditionReference(node.field, value)
          : null;
        return (
          <Fragment key={index}>
            {index > 0 ? ", " : ""}
            {reference ? (
              <span onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
                <Referenced reference={reference} inline>{String(value)}</Referenced>
              </span>
            ) : valueSummary(operator, value)}
          </Fragment>
        );
      })}
    </>
  );
}

/* ---------------------------------------------------------------- */
/* Read-only tree: the builder's layout with no controls             */
/* ---------------------------------------------------------------- */

/* One leaf as a sentence: the field by name, the operator, and the
 * value or values, with resource values opening their reference cards. */
function ConditionLeafReadout({ node }: { node: ConditionNode }) {
  const field = describeField(node.field ?? "");
  const operator = OPERATOR_BY_VALUE.get(node.operator ?? "");
  const values = operator?.kind === "none"
    ? []
    : Array.isArray(node.value) ? node.value : [node.value];
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 py-0.5">
      <span className={cn(field.argument ? "font-mono text-[12px] text-ink" : "text-body text-ink")}>
        {field.label}
      </span>
      <span className="text-label text-ink-muted">{operator?.label ?? node.operator ?? ""}</span>
      {values.map((value, index) => {
        const text = valueSummary(operator, value);
        const reference = typeof value === "string" && value.trim()
          ? conditionReference(node.field, value)
          : null;
        const chip = (
          <span className="rounded-sm border-hairline border-edge bg-surface px-1.5 py-0.5 font-mono text-[12px] text-ink">
            {text}
          </span>
        );
        return (
          <Fragment key={index}>
            {reference ? <Referenced reference={reference} inline>{chip}</Referenced> : chip}
          </Fragment>
        );
      })}
    </div>
  );
}

/*
 * The condition a block stores, laid out exactly like the builder (the
 * same group frames, the same and/or dividers) but with plain text in
 * place of every control. The view-mode face of a conditions block.
 */
export function ConditionTree({
  node,
  depth = 0,
  className,
}: {
  node: ConditionNode;
  depth?: number;
  className?: string;
}) {
  const kind: "all" | "any" | null = node.all ? "all" : node.any ? "any" : null;
  if (!kind) {
    /* A leaf root: one sentence, framed like a group so it sits where
     * the builder would. */
    return (
      <div className={cn("rounded-md border-hairline border-edge bg-canvas px-3 py-2", className)}>
        {node.some || node.every ? <LockedPredicateRow node={node} readOnly /> : <ConditionLeafReadout node={node} />}
      </div>
    );
  }
  const children = node[kind] ?? [];
  return (
    <div
      className={cn(
        "flex flex-col rounded-md border-hairline border-edge",
        depth % 2 === 0 ? "bg-canvas" : "bg-surface",
        className,
      )}
    >
      <div className="flex items-center gap-2 border-b-hairline border-edge px-3 py-1.5">
        <span className="font-mono text-[10px] font-medium tracking-wide text-ink uppercase">{kind}</span>
        <span className="text-label text-ink-muted">
          {kind === "all" ? "of the following must match" : "of the following may match"}
        </span>
      </div>
      <div className="flex flex-col px-3 py-2">
        {children.length === 0 ? (
          <p className="py-0.5 text-label text-ink-muted">No conditions. Every request matches.</p>
        ) : null}
        {children.map((child, index) => {
          const isGroup = child.all !== undefined || child.any !== undefined;
          const isLocked = child.some !== undefined || child.every !== undefined;
          return (
            <div key={child.id}>
              {index > 0 ? (
                <div className="flex items-center gap-2 py-1">
                  <span className="w-7 text-center font-mono text-[10px] font-medium tracking-wide text-ink-faint uppercase">
                    {kind === "all" ? "and" : "or"}
                  </span>
                  <span className="h-px flex-1 bg-edge" />
                </div>
              ) : null}
              {isLocked ? (
                <LockedPredicateRow node={child} readOnly />
              ) : isGroup ? (
                <ConditionTree node={child} depth={depth + 1} />
              ) : (
                <ConditionLeafReadout node={child} />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

/* ---------------------------------------------------------------- */
/* Field path input: free text with learned suggestions.            */
/* ---------------------------------------------------------------- */

export interface FieldPathInputProps {
  hints?: Record<string, FieldHint>;
  value: string;
  onChange: (next: string) => void;
  /* Candidate JSON Pointers, usually learned from a sample request. */
  suggestions?: string[];
  "aria-label"?: string;
  className?: string;
}

export function FieldPathInput({
  value,
  onChange,
  suggestions = [],
  hints = {},
  "aria-label": ariaLabel = "Field path",
  className,
}: FieldPathInputProps) {
  const [open, setOpen] = useState(false);
  const [focused, setFocused] = useState(false);
  /* Focusing lists every field; the list narrows once the person types. */
  const [filtering, setFiltering] = useState(false);
  const [highlight, setHighlight] = useState(0);
  const rootRef = useRef<HTMLDivElement | null>(null);

  /* At rest the field shows its name. Focusing reveals the stored pointer,
   * which stays the escape hatch for a path the suggestions do not know. */
  const field = describeField(value);
  const atRest = !focused && value !== "";
  const shown = atRest ? field.label : value;
  const mono = value !== "" && (!atRest || field.argument);

  const query = filtering ? value.trim().toLowerCase() : "";
  const matches = suggestions.filter((path) => fieldMatches(path, query));
  const listRef = useRef<HTMLUListElement | null>(null);

  useEffect(() => {
    setHighlight(0);
  }, [value, open]);

  useEffect(() => {
    if (open) listRef.current?.children[highlight]?.scrollIntoView({ block: "nearest" });
  }, [open, highlight]);

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (!open || matches.length === 0) return;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setHighlight((index) => Math.min(index + 1, matches.length - 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setHighlight((index) => Math.max(index - 1, 0));
    } else if (event.key === "Enter") {
      event.preventDefault();
      onChange(matches[highlight]);
      setOpen(false);
    } else if (event.key === "Escape") {
      setOpen(false);
    }
  }

  return (
    <div ref={rootRef} className={cn("relative min-w-0", className)}>
      <TextField
        aria-label={ariaLabel}
        mono={mono}
        value={shown}
        placeholder="Choose a field"
        spellCheck={false}
        data-1p-ignore=""
        data-lpignore="true"
        data-bwignore=""
        onChange={(event) => {
          onChange(event.target.value);
          setFiltering(true);
          setOpen(true);
        }}
        onFocus={() => { setFocused(true); setFiltering(false); setOpen(true); }}
        onBlur={() => { setFocused(false); setOpen(false); }}
        onKeyDown={onKeyDown}
      />
      {open && matches.length > 0 ? (
        <ul
          ref={listRef}
          className={cn(
            "absolute top-[34px] left-0 z-20 w-max min-w-full max-w-[min(480px,calc(100vw-32px))]",
            "max-h-72 overflow-y-auto rounded-md bg-surface-raised p-1 shadow-overlay ring-hairline",
          )}
        >
          {matches.map((path, index) => {
            const option = describeField(path);
            return (
              <li key={path}>
                <button
                  type="button"
                  tabIndex={-1}
                  /* mousedown beats the input blur that closes the list */
                  onMouseDown={(event) => {
                    event.preventDefault();
                    onChange(path);
                    setOpen(false);
                  }}
                  onMouseEnter={() => setHighlight(index)}
                  className={cn(
                    "flex min-h-7 w-full cursor-pointer flex-wrap items-center gap-x-3 rounded-sm px-2 py-1",
                    "text-left text-ink",
                    index === highlight && "bg-fill-hover",
                  )}
                >
                  <span className={cn("truncate", option.argument ? "font-mono text-[12px]" : "text-body")}>
                    {option.label}
                  </span>
                  <span className="ml-auto shrink-0 font-mono text-micro text-ink-faint">{hints[path]?.types.join(" | ") || path}</span>
                  {hints[path] ? <span className="w-full text-micro text-ink-muted">{hints[path].description}{hints[path].partial ? ` · Applies to ${hints[path].sources.join(", ")}` : ""}</span> : null}
                </button>
              </li>
            );
          })}
        </ul>
      ) : null}
    </div>
  );
}

/* ---------------------------------------------------------------- */
/* Value editors                                                    */
/* ---------------------------------------------------------------- */

export type ConditionValueSuggestions = Record<string, (string | number)[]>;
/* A line under a suggested value, by field path then value: what the
 * editor knows about a server beyond its alias. Tool names carry their
 * own line from the schema catalogue. */
export type ConditionValueDetails = Record<string, Record<string, string>>;

function SuggestedValueInput({ value, onChange, suggestions, details, isNumber = false, placeholder = "value" }: {
  value: unknown;
  onChange: (next: unknown) => void;
  suggestions: (string | number)[];
  details?: Record<string, string>;
  isNumber?: boolean;
  placeholder?: string;
}) {
  const rendered = value === undefined || value === null ? "" : String(value);
  const [draft, setDraft] = useState(rendered);
  useEffect(() => setDraft(rendered), [rendered]);
  const text = isNumber ? draft : rendered;
  function change(raw: string) {
    setDraft(raw);
    const parsed = Number(raw);
    if (isNumber && raw !== "" && !Number.isFinite(parsed)) return;
    onChange(isNumber && raw !== "" && Number.isFinite(parsed) ? parsed : raw);
  }
  const autocomplete = useInputSuggestions(text, suggestions.filter((item) => typeof item === (isNumber ? "number" : "string")).map(String), change);
  return (
    <div className="relative min-w-0 flex-1">
      <TextField {...autocomplete.input} aria-label="Comparison value" mono value={text}
        spellCheck={false} inputMode={isNumber ? "decimal" : undefined} placeholder={placeholder}
        onChange={(event) => { change(event.target.value); autocomplete.changed(); }} />
      <InputSuggestions {...autocomplete.list} details={details} />
    </div>
  );
}

function ScalarValueEditor({
  value,
  onChange,
  suggestions,
  details,
  typeHint,
}: {
  value: unknown;
  onChange: (next: unknown) => void;
  suggestions: (string | number)[];
  details?: Record<string, string>;
  typeHint?: string;
}) {
  const isNumber = typeof value === "number" || (value === "" && (typeHint === "number" || typeHint === "integer"));
  const isBoolean = typeof value === "boolean" || (value === "" && typeHint === "boolean");
  return (
    <div className="flex min-w-0 flex-1 items-center gap-1.5">
      {isBoolean ? <ToggleGroup aria-label="Boolean value" value={value === true ? "true" : value === false ? "false" : ""} onValueChange={(next) => { if (next) onChange(next === "true"); }}><ToggleGroupItem value="true">True</ToggleGroupItem><ToggleGroupItem value="false">False</ToggleGroupItem></ToggleGroup> : <SuggestedValueInput details={details} value={value} onChange={onChange} suggestions={suggestions} isNumber={isNumber} placeholder={isNumber ? "0" : "value"} />}
      {/* v1 comparisons never coerce, so the value's JSON type is part
       * of the condition and stays visible. */}
      <ToggleGroup
        aria-label="Value type"
        value={isBoolean ? "boolean" : isNumber ? "number" : "text"}
        onValueChange={(next) => {
          if (next === "boolean") { onChange(value === true); } else if (next === "number" && !isNumber) {
            const parsed = Number(value);
            onChange(Number.isFinite(parsed) && String(value).trim() !== "" ? parsed : 0);
          } else if (next === "text" && (isNumber || isBoolean)) {
            onChange(String(value));
          }
        }}
      >
        <ToggleGroupItem value="text" aria-label="Compare as text" className="font-mono text-[11px]">
          Aa
        </ToggleGroupItem>
        <ToggleGroupItem value="number" aria-label="Compare as number" className="font-mono text-[11px]">
          123
        </ToggleGroupItem>
        <ToggleGroupItem value="boolean" aria-label="Compare as boolean" className="font-mono text-[11px]">T/F</ToggleGroupItem>
      </ToggleGroup>
    </div>
  );
}

function ValueEditor({
  kind,
  value,
  onChange,
  suggestions,
  details,
  typeHint,
}: {
  kind: ConditionValueKind;
  value: unknown;
  onChange: (next: unknown) => void;
  suggestions: (string | number)[];
  details?: Record<string, string>;
  typeHint?: string;
}) {
  if (kind === "none") return null;
  if (kind === "scalar") return <ScalarValueEditor details={details} typeHint={typeHint} value={value} onChange={onChange} suggestions={suggestions} />;
  if (kind === "list") {
    const list = Array.isArray(value) ? value.map(String) : [];
    return (
      <TagInput
        aria-label="Comparison values"
        mono
        value={list}
        suggestions={suggestions.filter((item): item is string => typeof item === "string")}
        onChange={onChange}
        placeholder="add a value, press Enter"
        className="min-w-0 flex-1"
      />
    );
  }
  const isNumber = kind === "number";
  return (
    <SuggestedValueInput
      value={value}
      onChange={onChange}
      suggestions={kind === "regex" ? [] : suggestions}
      isNumber={isNumber}
      placeholder={kind === "regex" ? "^[^@]+@example\\.com$" : isNumber ? "0" : "value"}
    />
  );
}

/* ---------------------------------------------------------------- */
/* Rows and groups                                                  */
/* ---------------------------------------------------------------- */

function ConditionLeafRow({
  node,
  onChange,
  onRemove,
  suggestions,
  valueSuggestions,
  valueDetails,
}: {
  node: ConditionNode;
  onChange: (next: ConditionNode) => void;
  onRemove?: () => void;
  suggestions: string[];
  valueSuggestions: ConditionValueSuggestions;
  valueDetails: ConditionValueDetails;
}) {
  const catalog = useContext(ToolSchemaContext);
  const hints = catalog ? fieldHints(catalog.root, node.id, catalog.tools) : { fields: {}, tools: [], narrowed: false };
  const field = hints.fields[node.field ?? ""];
  const warning = schemaWarning(node, hints);
  const [allOperators, setAllOperators] = useState(false);
  const suitable = suitableOperators(field);
  const operators = allOperators || !suitable ? CONDITION_OPERATORS : CONDITION_OPERATORS.filter((item) => suitable.includes(item.value) || item.value === node.operator);
  const paths = [...new Set([...suggestions.filter((path) => !hints.narrowed || !path.startsWith("/request/arguments/")), ...Object.keys(hints.fields), ...(node.field ? [node.field] : [])])];
  const values = field?.values.filter((value): value is string | number => typeof value === "string" || typeof value === "number") ?? [];
  const toolDetails = Object.fromEntries((catalog?.tools ?? []).map((tool) => {
    const matching = catalog!.tools.filter((other) => other.name === tool.name);
    const variants = new Set(matching.map((other) => JSON.stringify(other.inputSchema ?? null))).size;
    return [tool.name, [...new Set(matching.map((other) => other.source))].join(", ") + (variants > 1 ? ` · ${variants} schema variants` : "") + (tool.description ? `: ${tool.description}` : "")];
  }));
  const operator = OPERATOR_BY_VALUE.get(node.operator ?? "") ?? CONDITION_OPERATORS[0];
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-2">
      <FieldPathInput
        hints={hints.fields}
        value={node.field ?? ""}
        onChange={(field) => onChange({ ...node, field })}
        suggestions={paths}
        className="w-[230px] shrink-0 max-sm:w-full"
      />
      <Select
        items={operators}
        itemToStringLabel={(item) => (item as ConditionOperator).label}
        value={operator}
        onValueChange={(next) => {
          if (!next) return;
          const chosen = next as ConditionOperator;
          const kept =
            chosen.kind === operator.kind
              ? node.value
              : chosen.kind === "list"
                ? []
                : chosen.kind === "none"
                  ? undefined
                  : "";
          onChange({ ...node, operator: chosen.value, value: kept });
        }}
      >
        <SelectTrigger aria-label="Operator" className="w-[150px] min-w-[150px] shrink-0" />
        <SelectContent<ConditionOperator>>
          {(item) => (
            <SelectItem key={item.value} value={item}>
              {item.label}
            </SelectItem>
          )}
        </SelectContent>
      </Select>
      <ValueEditor
        key={`${node.field}:${operator.kind}`}
        kind={operator.kind}
        value={node.value}
        onChange={(value) => onChange({ ...node, value })}
        suggestions={[...new Set([...values, ...(!hints.narrowed || !node.field?.startsWith("/request/arguments/") ? valueSuggestions[node.field ?? ""] ?? [] : [])])]}
        details={node.field === "/request/tool" ? toolDetails : valueDetails[node.field ?? ""]}
        typeHint={field && !field.unknown && field.types.length === 1 ? field.types[0] : undefined}
      />
      {suitable ? <Button size="sm" variant="ghost" onClick={() => setAllOperators(!allOperators)}>{allOperators ? "Suggested operators" : "All operators"}</Button> : null}
      {node.field === "/request/tool" && (catalog?.tools.length ?? 0) > 0 ? <p className="w-full text-micro text-ink-muted">A tool name matches every source that sends that name.</p> : null}
      {field ? <p className="w-full text-micro text-ink-muted">{field.description} {field.types.join(" | ")}{field.required ? " · Required" : ""}{field.partial ? " · Present in some definitions" : ""}. {field.sources.join(", ")}</p> : null}
      {warning ? <p role="status" className="w-full text-micro text-warn">{warning}</p> : null}
      {onRemove ? (
        <IconButton aria-label="Remove condition" size="sm" onClick={onRemove}>
          <XIcon width={12} height={12} />
        </IconButton>
      ) : null}
    </div>
  );
}

function LockedPredicateRow({ node, readOnly = false }: { node: ConditionNode; readOnly?: boolean }) {
  const quantifier = node.some ? "some" : "every";
  const field = node.some?.field ?? node.every?.field ?? "";
  return (
    <div className="flex items-center gap-2 rounded-sm border-hairline border-edge bg-canvas px-2.5 py-1.5">
      <span className="text-label text-ink-muted">
        {quantifier === "some" ? "Some element of" : "Every element of"}
      </span>
      <span className="font-mono text-[12px] text-ink">{field}</span>
      {readOnly ? null : <span className="ml-auto text-micro text-ink-faint">edit in JSON</span>}
    </div>
  );
}

const ToolSchemaContext = createContext<{ root: ConditionNode; tools: ToolSchema[] } | null>(null);

export interface ConditionBuilderProps {
  tools?: ToolSchema[];
  value: ConditionNode;
  onChange: (next: ConditionNode) => void;
  /* Candidate field paths offered by the path input. */
  suggestions?: string[];
  valueSuggestions?: ConditionValueSuggestions;
  valueDetails?: ConditionValueDetails;
  /* Conditions every request reaching this one already met: the
   * enclosing pipeline branches' conditions. They are never edited here;
   * they only narrow the field hints, as if they were joined to this
   * condition with "all". */
  scope?: ConditionNode[];
  className?: string;
}

/*
 * Edits one group node. The root is always a group; a leaf root is
 * wrapped in an implicit "all" by the caller before editing.
 */
export function ConditionBuilder({
  tools = [],
  value,
  onChange,
  suggestions = [],
  valueSuggestions = {},
  valueDetails = {},
  scope = [],
  className,
}: ConditionBuilderProps) {
  const root = scope.length ? { id: "__scope__", all: [...scope, value] } : value;
  return (
    <ToolSchemaContext.Provider value={{ root, tools }}>
    <ConditionGroupEditor
      node={value}
      onChange={onChange}
      suggestions={suggestions}
      valueSuggestions={valueSuggestions}
      valueDetails={valueDetails}
      depth={0}
      className={className}
    />
    </ToolSchemaContext.Provider>
  );
}

function ConditionGroupEditor({
  node,
  onChange,
  onRemove,
  suggestions,
  valueSuggestions,
  valueDetails,
  depth,
  className,
}: {
  node: ConditionNode;
  onChange: (next: ConditionNode) => void;
  onRemove?: () => void;
  suggestions: string[];
  valueSuggestions: ConditionValueSuggestions;
  valueDetails: ConditionValueDetails;
  depth: number;
  className?: string;
}) {
  const kind: "all" | "any" = node.all ? "all" : "any";
  const children = node[kind] ?? [];

  function setChildren(next: ConditionNode[]) {
    onChange({ id: node.id, [kind]: next });
  }

  function setChild(index: number, next: ConditionNode) {
    setChildren(children.map((child, childIndex) => (childIndex === index ? next : child)));
  }

  function removeChild(index: number) {
    setChildren(children.filter((_, childIndex) => childIndex !== index));
  }

  return (
    <div
      className={cn(
        "flex flex-col rounded-md border-hairline border-edge",
        depth % 2 === 0 ? "bg-canvas" : "bg-surface",
        className,
      )}
    >
      <div className="flex items-center gap-2.5 border-b-hairline border-edge px-3 py-2">
        <ToggleGroup
          aria-label="Group match mode"
          value={kind}
          onValueChange={(next) => {
            if (next === kind) return;
            onChange({ id: node.id, [next]: children });
          }}
        >
          <ToggleGroupItem value="all">All</ToggleGroupItem>
          <ToggleGroupItem value="any">Any</ToggleGroupItem>
        </ToggleGroup>
        <span className="text-label text-ink-muted">
          {kind === "all" ? "of the following must match" : "of the following may match"}
        </span>
        {onRemove ? (
          <IconButton
            aria-label="Remove group"
            size="sm"
            className="ml-auto"
            onClick={onRemove}
          >
            <XIcon width={12} height={12} />
          </IconButton>
        ) : null}
      </div>

      <div className="flex flex-col px-3 py-2.5">
        {children.map((child, index) => {
          const isGroup = child.all !== undefined || child.any !== undefined;
          const isLocked = child.some !== undefined || child.every !== undefined;
          return (
            <div key={child.id}>
              {index > 0 ? (
                <div className="flex items-center gap-2 py-1.5">
                  <span className="w-7 text-center font-mono text-[10px] font-medium tracking-wide text-ink-faint uppercase">
                    {kind === "all" ? "and" : "or"}
                  </span>
                  <span className="h-px flex-1 bg-edge" />
                </div>
              ) : null}
              {isLocked ? (
                <LockedPredicateRow node={child} />
              ) : isGroup ? (
                <ConditionGroupEditor
                  node={child}
                  onChange={(next) => setChild(index, next)}
                  onRemove={() => removeChild(index)}
                  suggestions={suggestions}
                  valueSuggestions={valueSuggestions}
                  valueDetails={valueDetails}
                  depth={depth + 1}
                />
              ) : (
                <ConditionLeafRow
                  node={child}
                  onChange={(next) => setChild(index, next)}
                  onRemove={children.length > 1 ? () => removeChild(index) : undefined}
                  suggestions={suggestions}
                  valueSuggestions={valueSuggestions}
                  valueDetails={valueDetails}
                />
              )}
            </div>
          );
        })}

        <div className="mt-2.5 flex items-center gap-2">
          <Button size="sm" variant="ghost" onClick={() => setChildren([...children, newConditionLeaf()])}>
            <PlusIcon width={13} height={13} />
            Condition
          </Button>
          {depth < 3 ? (
            <Button
              size="sm"
              variant="ghost"
              onClick={() =>
                setChildren([...children, newConditionGroup(kind === "all" ? "any" : "all")])
              }
            >
              <PlusIcon width={13} height={13} />
              Group
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
