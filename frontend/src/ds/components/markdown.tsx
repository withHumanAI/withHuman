import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "@/ds/lib/cn";
import { focusRing } from "@/ds/lib/focus";

/*
 * Machine-authored prose: a tool's description, an agent's reasoning.
 * Rendered as Markdown with the product's own type, never the
 * author's: every heading is a bold line at the surrounding size, code
 * is mono, links open in a new tab, and a bare web address is a link
 * too (GitHub-flavoured Markdown, so tables and strikethrough read as
 * the author meant). Raw HTML is never rendered. Sizes
 * are relative so the block takes the type scale of where it sits
 * (text-body in a card, text-label in a list row). Long prose can be
 * collapsed to a few lines, opened with Show more.
 */

const components: Components = {
  p: ({ children }) => <p className="my-[0.6em] first:mt-0 last:mb-0">{children}</p>,
  h1: ({ children }) => <p className="my-[0.6em] font-medium text-ink first:mt-0 last:mb-0">{children}</p>,
  h2: ({ children }) => <p className="my-[0.6em] font-medium text-ink first:mt-0 last:mb-0">{children}</p>,
  h3: ({ children }) => <p className="my-[0.6em] font-medium text-ink first:mt-0 last:mb-0">{children}</p>,
  h4: ({ children }) => <p className="my-[0.6em] font-medium text-ink first:mt-0 last:mb-0">{children}</p>,
  h5: ({ children }) => <p className="my-[0.6em] font-medium text-ink first:mt-0 last:mb-0">{children}</p>,
  h6: ({ children }) => <p className="my-[0.6em] font-medium text-ink first:mt-0 last:mb-0">{children}</p>,
  ul: ({ children }) => (
    <ul className="my-[0.6em] list-disc pl-[1.4em] first:mt-0 last:mb-0 [&_ul]:my-[0.2em] [&_ol]:my-[0.2em]">
      {children}
    </ul>
  ),
  ol: ({ children }) => (
    <ol className="my-[0.6em] list-decimal pl-[1.4em] first:mt-0 last:mb-0 [&_ul]:my-[0.2em] [&_ol]:my-[0.2em]">
      {children}
    </ol>
  ),
  li: ({ children }) => <li className="my-[0.15em] pl-[0.2em]">{children}</li>,
  strong: ({ children }) => <strong className="font-medium text-ink">{children}</strong>,
  em: ({ children }) => <em className="italic">{children}</em>,
  a: ({ href, children }) => (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="text-ink underline decoration-edge-strong underline-offset-2 hover:decoration-ink"
    >
      {children}
    </a>
  ),
  code: ({ children, className }) =>
    /* Fenced blocks arrive as code inside pre with a language class;
     * inline code arrives bare. */
    className ? (
      <code className="font-mono">{children}</code>
    ) : (
      <code className="rounded-xs bg-fill-hover px-[0.3em] py-[0.1em] font-mono text-[0.92em] text-ink">
        {children}
      </code>
    ),
  pre: ({ children }) => (
    <pre className="my-[0.6em] overflow-x-auto rounded-sm border-hairline border-edge bg-surface p-[0.8em] font-mono text-[0.92em] leading-[1.5] text-ink-secondary first:mt-0 last:mb-0">
      {children}
    </pre>
  ),
  blockquote: ({ children }) => (
    <blockquote className="my-[0.6em] border-l-2 border-edge-strong pl-[0.8em] text-ink-muted first:mt-0 last:mb-0">
      {children}
    </blockquote>
  ),
  hr: () => <hr className="my-[0.8em] border-t-hairline border-edge" />,
  table: ({ children }) => (
    <div className="my-[0.6em] overflow-x-auto first:mt-0 last:mb-0">
      <table className="border-collapse text-[0.95em]">{children}</table>
    </div>
  ),
  th: ({ children }) => (
    <th className="border-b-hairline border-edge px-[0.6em] py-[0.3em] text-left font-medium text-ink">{children}</th>
  ),
  td: ({ children }) => <td className="border-b-hairline border-edge px-[0.6em] py-[0.3em] align-top">{children}</td>,
  img: ({ alt }) => <span className="text-ink-muted">[image{alt ? `: ${alt}` : ""}]</span>,
};

export interface MarkdownProps {
  children: string;
  className?: string;
  /* Collapse the text to this many lines, with a Show more toggle when
   * it runs longer. */
  lines?: number;
}

export function Markdown({ children, className, lines }: MarkdownProps) {
  const body = (
    <ReactMarkdown components={components} remarkPlugins={[remarkGfm]} skipHtml>
      {children}
    </ReactMarkdown>
  );
  if (lines === undefined) {
    return <div className={cn("min-w-0 break-words", className)}>{body}</div>;
  }
  return (
    <ClampedMarkdown className={className} lines={lines}>
      {body}
    </ClampedMarkdown>
  );
}

function ClampedMarkdown({ children, className, lines }: { children: ReactNode; className?: string; lines: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);

  /* Only the collapsed text can be measured: expanded, it never
   * overflows. The toggle stays while expanded so it can close. */
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element || expanded) return;
    const measure = () => setOverflows(element.scrollHeight > element.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [expanded, children]);

  return (
    <div className={cn("flex min-w-0 flex-col items-start gap-1", className)}>
      <div
        ref={ref}
        className="min-w-0 self-stretch break-words"
        style={expanded ? undefined : { display: "-webkit-box", WebkitBoxOrient: "vertical", WebkitLineClamp: lines, overflow: "hidden" }}
      >
        {children}
      </div>
      {overflows || expanded ? (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((open) => !open)}
          className={cn(
            "cursor-pointer rounded-xs text-label text-ink-muted transition-colors duration-100 hover:text-ink",
            focusRing,
          )}
        >
          {expanded ? "Show less" : "Show more"}
        </button>
      ) : null}
    </div>
  );
}
