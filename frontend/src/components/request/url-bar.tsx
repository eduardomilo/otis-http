import { useMemo } from "react";
import type { KeyBinding } from "@codemirror/view";

import { CodeEditor } from "@/components/editor/code-editor";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { methodColor } from "@/lib/method";
import { hint } from "@/lib/platform";
import { cn } from "@/lib/utils";
import type { VariableRef } from "@bindings/internal/services";

/**
 * The URL bar: method selector, URL field, Send (screen 1a).
 *
 * Metrics from DESIGN-NOTES §4.4 — all three controls 30px tall with radius
 * 4px, the method and URL fields on `--bg-inset` inside `--border-control`,
 * Send filled with the accent and labelled in `--accent-on` at weight 600,
 * which §1 notes is the only place weight 600 appears.
 *
 * The URL field is a single-line CodeMirror rather than an `<input>`. The
 * design renders `{{baseUrl}}` and `{{$uuid}}` as accent tokens on a wash
 * *inside* the field, which a plain input cannot do; the alternatives are a
 * mirror div behind a transparent input, whose glyph alignment has to be
 * maintained by hand, or this — which reuses the body editor's variable
 * decoration, so a token looks the same everywhere by construction.
 */

/** The methods the selector offers. */
const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;

export function UrlBar({
  method,
  url,
  variables,
  disabled,
  onMethodChange,
  onUrlChange,
  onSend,
  onSave,
}: {
  method: string;
  url: string;
  variables?: readonly VariableRef[];
  /** Send is disabled until increment 11 wires the sender. */
  disabled?: boolean;
  onMethodChange: (method: string) => void;
  onUrlChange: (url: string) => void;
  onSend?: () => void;
  onSave?: () => void;
}) {
  // ⌘↵ and ⌘S have to work with the caret in the URL field, so the shell's
  // shortcuts are handed to the editor as a keymap. See useKeymap's inFields
  // flag for the other half of this.
  const keys = useMemo<KeyBinding[]>(
    () => [
      { key: "Mod-Enter", preventDefault: true, run: () => (onSend?.(), true) },
      { key: "Mod-s", preventDefault: true, run: () => (onSave?.(), true) },
      // A single-line field: Enter must not insert a newline. It sends, the
      // way it does in every address bar.
      { key: "Enter", preventDefault: true, run: () => (onSend?.(), true) },
    ],
    [onSend, onSave],
  );

  return (
    <div className="flex shrink-0 items-center gap-2 px-4 py-2">
      <Select value={method || "GET"} onValueChange={onMethodChange}>
        <SelectTrigger
          aria-label="Method"
          className={cn(
            "h-[30px] w-[92px] shrink-0 rounded-md border-border-control bg-inset px-2.5 font-mono text-ui font-medium",
            methodColor(method),
          )}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent className="min-w-[92px] rounded-md border-border-control bg-raised">
          {METHODS.map((m) => (
            <SelectItem
              key={m}
              value={m}
              className={cn("font-mono text-ui font-medium", methodColor(m))}
            >
              {m}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <div className="flex h-[30px] min-w-0 flex-1 items-center overflow-hidden rounded-md border border-border-control bg-inset px-2.5">
        <CodeEditor
          value={url}
          onChange={(next) => onUrlChange(next.replace(/\n/g, ""))}
          variables={variables}
          keys={keys}
          singleLine
          placeholder="https://example.com/v1/resource"
          className="flex-1"
        />
      </div>

      <button
        type="button"
        disabled={disabled}
        onClick={onSend}
        title={disabled ? "Sending arrives in increment 11" : `Send ${hint("↵")}`}
        className={cn(
          "flex h-[30px] shrink-0 items-center gap-2 rounded-md px-3.5 text-ui font-semibold",
          "bg-primary text-primary-foreground hover:bg-primary-hover",
          "disabled:opacity-40 disabled:hover:bg-primary",
        )}
      >
        Send
        <span className="font-mono text-label font-medium opacity-70">{hint("↵")}</span>
      </button>
    </div>
  );
}
