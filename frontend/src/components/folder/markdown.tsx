import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

/**
 * A folder's README, rendered (screen 3a's left column).
 *
 * `react-markdown` builds a React element tree rather than an HTML string, so
 * there is no `dangerouslySetInnerHTML` anywhere and a README cannot inject
 * markup. That matters more than it looks: a README is repository content, it
 * arrives from whoever wrote the branch, and the window it renders in is the
 * one holding the collection.
 *
 * Every element is mapped to the design's own type scale and tokens
 * (DESIGN-NOTES §3): 13px sans prose at a 20px line height, headings at the
 * sizes the scale already has, and inline code as the `--bg-control` chip
 * screen 3a draws. No element is left to the browser's defaults, because the
 * browser's defaults are the one thing in the window that would not match.
 */
export function MarkdownView({ source }: { source: string }) {
  return (
    <div className="max-w-[70ch] text-result text-fg-secondary">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => (
            <h1 className="mt-6 mb-3 text-heading font-medium text-fg-emphasis first:mt-0">
              {children}
            </h1>
          ),
          h2: ({ children }) => (
            <h2 className="mt-6 mb-2 text-result font-medium text-fg-emphasis first:mt-0">
              {children}
            </h2>
          ),
          h3: ({ children }) => (
            <h3 className="mt-5 mb-2 text-ui font-medium text-fg first:mt-0">{children}</h3>
          ),
          h4: ({ children }) => (
            <h4 className="mt-4 mb-1.5 text-ui font-medium text-fg-muted first:mt-0">
              {children}
            </h4>
          ),
          p: ({ children }) => <p className="my-3 leading-5">{children}</p>,
          a: ({ href, children }) => (
            // Not a live link: the window is served from a custom scheme and
            // navigating it away from the app would be a one-way door. The
            // target is shown instead, which is what a reader needs anyway.
            <span className="text-primary underline decoration-dotted" title={href}>
              {children}
            </span>
          ),
          ul: ({ children }) => <ul className="my-3 list-disc pl-5 leading-5">{children}</ul>,
          ol: ({ children }) => <ol className="my-3 list-decimal pl-5 leading-5">{children}</ol>,
          li: ({ children }) => <li className="my-1">{children}</li>,
          strong: ({ children }) => (
            <strong className="font-medium text-fg-emphasis">{children}</strong>
          ),
          em: ({ children }) => <em className="italic">{children}</em>,
          blockquote: ({ children }) => (
            <blockquote className="my-3 border-l-2 border-border-control pl-3 text-fg-muted">
              {children}
            </blockquote>
          ),
          hr: () => <hr className="my-5 border-border" />,
          // A fenced block. react-markdown gives the <pre> the <code> as its
          // child, so the styling lives here and `code` only has to handle
          // the inline case.
          pre: ({ children }) => (
            <pre className="my-3 overflow-x-auto rounded-sm border border-border-control bg-inset p-3 font-mono text-code leading-5 text-fg-secondary">
              {children}
            </pre>
          ),
          code: ({ className, children }) => {
            // A fenced block's <code> carries a language class; an inline one
            // does not. Only the inline case gets the chip.
            if (className?.includes("language-")) {
              return <code className={className}>{children}</code>;
            }
            return (
              <code className="rounded-sm bg-control px-1 font-mono text-ui text-fg-secondary">
                {children}
              </code>
            );
          },
          table: ({ children }) => (
            <div className="my-3 overflow-x-auto">
              <table className="w-full border-collapse text-ui">{children}</table>
            </div>
          ),
          th: ({ children }) => (
            <th className="border-b border-border px-2 py-1 text-left font-medium text-fg-muted">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="border-b border-border-hairline px-2 py-1 align-top">{children}</td>
          ),
          img: ({ alt }) => (
            // Images are not loaded: the page is served from a custom scheme
            // and a remote one would be a request the collection made without
            // being asked. The alt text says what is missing.
            <span className="my-2 inline-block rounded-sm border border-dashed border-border-control px-2 py-1 text-meta text-fg-faint">
              image: {alt || "untitled"}
            </span>
          ),
        }}
      >
        {source}
      </Markdown>
    </div>
  );
}
