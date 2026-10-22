import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { previewHTML } from "@/ds/lib/preview-html";
import { cn } from "@/ds/lib/cn";
import { useTheme } from "@/ds/theme/theme-provider";
import { CodeBlock } from "./code-block";
import { CopyButton } from "./copy-button";
import { ToggleGroup, ToggleGroupItem } from "./toggle-group";

/*
 * HTML an agent wrote (an email body, a snippet), shown the way its
 * recipient would see it. The document renders in an iframe with every
 * sandbox restriction on and a policy that loads nothing: no script
 * runs, no image or font is fetched (so no tracking pixel fires from
 * the review page), no link navigates. The source is one click away,
 * because the rendered view is a courtesy and the markup is the fact.
 */
export interface HtmlPreviewProps {
  html: string;
  label?: string;
  raised?: boolean;
  className?: string;
}

type View = "preview" | "source";

/* The tallest the preview grows before the frame scrolls. */
const MAX_HEIGHT = 560;

/* The page's own webfonts cannot load inside the frame, which loads
 * nothing, so the frame's stack starts at the first family it can use.
 * Measuring with the same stack keeps the measured lines the frame's. */
function frameFontStack(stack: string): string {
  const webfonts = new Set([...window.document.fonts].map((face) => face.family.replace(/^["']|["']$/g, "")));
  const families = stack.split(",").map((family) => family.trim()).filter(Boolean);
  const usable = families.filter((family) => !webfonts.has(family.replace(/^["']|["']$/g, "")));
  return usable.length > 0 ? usable.join(", ") : "system-ui, sans-serif";
}

export function HtmlPreview({ html, label, raised = false, className }: HtmlPreviewProps) {
  const [view, setView] = useState<View>("preview");
  const { resolvedTheme } = useTheme();
  const { body, css } = useMemo(() => {
    /* The product's own type inside the frame, from the tokens the
     * theme resolves to at this moment; the frame is opaque to the
     * page's stylesheet. The body rule is written for a root selector
     * so the measurement below lays out under the same rules. */
    const style = getComputedStyle(window.document.documentElement);
    const token = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback;
    const css = (root: string) => `
      ${root} {
        display: block;
        margin: 0;
        padding: 14px 16px;
        font: 14px/1.5 ${frameFontStack(token("--font-sans", "system-ui, sans-serif"))};
        color: ${token("--ds-ink", "#111")};
        background: ${token(raised ? "--ds-surface" : "--ds-canvas", "#fff")};
        overflow-wrap: anywhere;
      }
      ${root} > :first-child { margin-top: 0; }
      ${root} > :last-child { margin-bottom: 0; }
      a { color: ${token("--ds-accent", "#2450c8")}; pointer-events: none; }
      pre, code { font-family: ${token("--font-mono", "monospace")}; font-size: 12px; }
      blockquote { margin: 0.6em 0; padding-left: 0.8em; border-left: 2px solid ${token("--ds-edge-strong", "#ccc")}; color: ${token("--ds-ink-muted", "#666")}; }
      table { border-collapse: collapse; }
      td, th { padding: 0.2em 0.5em; }
    `;
    return { body: previewHTML(html), css };
  }, [html, raised, resolvedTheme]);
  const document = `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'"><style>:root { color-scheme: ${resolvedTheme}; } html { margin: 0; padding: 0; } ${css("body")}</style></head><body>${body}</body></html>`;

  /* The frame runs no script, so it cannot report its height. The
   * cleaned fragment carries no attributes, URLs or images, so it is
   * laid out once more in the page, invisibly and under the same rules,
   * and the frame takes that height up to the cap. */
  const measureRef = useRef<HTMLDivElement>(null);
  const [height, setHeight] = useState(0);
  useLayoutEffect(() => {
    const host = measureRef.current;
    if (!host || view !== "preview") return;
    const shadow = host.shadowRoot ?? host.attachShadow({ mode: "open" });
    shadow.innerHTML = `<style>:host { all: initial; } ${css(".root")}</style><div class="root">${body}</div>`;
    const root = shadow.querySelector(".root") as HTMLElement;
    const measure = () => setHeight(Math.min(MAX_HEIGHT, Math.ceil(root.getBoundingClientRect().height)));
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(host);
    return () => observer.disconnect();
  }, [body, css, view]);

  return (
    <div
      className={cn(
        "overflow-hidden border-hairline border-edge",
        raised ? "rounded-lg bg-surface shadow-sm" : "rounded-md bg-canvas",
        className,
      )}
    >
      {/* Tall enough for the toggle, as in the arguments card's header. */}
      <div className="flex h-10 items-center justify-between border-b-hairline border-edge pr-2 pl-3">
        <span className="font-mono text-[11px] text-ink-muted">{label ?? "html"}</span>
        <span className="flex items-center gap-1.5">
          <ToggleGroup aria-label="HTML view" value={view} onValueChange={(next) => setView(next as View)}>
            <ToggleGroupItem value="preview">Preview</ToggleGroupItem>
            <ToggleGroupItem value="source">Source</ToggleGroupItem>
          </ToggleGroup>
          <CopyButton value={html} aria-label="Copy HTML source" />
        </span>
      </div>
      {view === "preview" ? (
        <div className="relative resize-y overflow-hidden" style={{ height: height || undefined }}>
          <div ref={measureRef} aria-hidden className="pointer-events-none invisible absolute inset-x-0 top-0" />
          <iframe
            title={label ? `${label} preview` : "HTML preview"}
            sandbox=""
            referrerPolicy="no-referrer"
            srcDoc={document}
            className="block h-full w-full border-0"
          />
        </div>
      ) : (
        <CodeBlock code={html} copyable={false} wrap className="rounded-none border-0" />
      )}
    </div>
  );
}
