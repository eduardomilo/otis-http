import { tokenize, VARIABLE_CLASS, variableState, variableTitle, type VariableIndex } from "@/lib/variables";
import { cn } from "@/lib/utils";

/**
 * Text with its `{{variable}}` tokens styled (DESIGN-NOTES §2.7, screen 4a's
 * `{{idemKey}}` and `Bearer {{apiKey}}`).
 *
 * This is the read-only counterpart to the CodeMirror decoration: the header
 * table and the auth card show values they do not edit in place, and mounting
 * an editor per table row to get one styled token would be absurd.
 *
 * A token carries a plain `title` naming its origin, for the same reason the
 * tree's git dots do (CLAUDE.md): a Radix tooltip per row is what made the
 * tree slow.
 */
export function VariableText({
  text,
  index,
  className,
}: {
  text: string;
  index: VariableIndex;
  className?: string;
}) {
  const tokens = tokenize(text);
  return (
    <span className={cn("font-mono", className)}>
      {tokens.map((token, i) =>
        token.name === undefined ? (
          <span key={i}>{token.text}</span>
        ) : (
          <span
            key={i}
            title={variableTitle(index, token.name)}
            className={cn("px-0.5", VARIABLE_CLASS[variableState(index, token.name)])}
          >
            {token.text}
          </span>
        ),
      )}
    </span>
  );
}
