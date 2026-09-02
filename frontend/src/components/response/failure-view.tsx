import { AlertTriangle, Ban, Clock, Globe, PlugZap, ShieldAlert, Variable } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { SendFailure } from "@bindings/internal/services";

/**
 * A send that produced no response.
 *
 * The requirement from increment 11 is that these read as a failure state, not
 * a stack trace. So each class gets a line saying what happened in the terms
 * the reader is in — a host that does not resolve, a port with nothing
 * listening, a certificate that was rejected — and the Go error is kept
 * underneath, in mono, for when it is the thing you actually need.
 *
 * The classes come from Go (services.FailureKind), because the classification
 * is done where the error is: matching on message text in the window would be
 * guessing at something the sender knew for certain.
 */

const ICON: Record<string, LucideIcon> = {
  dns: Globe,
  refused: PlugZap,
  tls: ShieldAlert,
  timeout: Clock,
  cancelled: Ban,
  redirect: Globe,
  resolve: Variable,
  request: AlertTriangle,
  network: PlugZap,
  collection: AlertTriangle,
};

/** What each class is called, for the line above the message. */
const TITLE: Record<string, string> = {
  dns: "Host not found",
  refused: "Connection refused",
  tls: "TLS failed",
  timeout: "Timed out",
  cancelled: "Cancelled",
  redirect: "Too many redirects",
  resolve: "Not resolved",
  request: "Could not be sent",
  network: "Network error",
  collection: "Not a request",
};

export function FailureView({ failure }: { failure: SendFailure }) {
  const Icon = ICON[failure.kind] ?? AlertTriangle;
  const cancelled = failure.kind === "cancelled";

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto px-4 py-4">
      <div className="flex items-start gap-2.5">
        <Icon
          className={cn("mt-0.5 size-4 shrink-0", cancelled ? "text-fg-dim" : "text-destructive")}
        />
        <div className="min-w-0">
          <p
            className={cn(
              "text-ui font-medium",
              cancelled ? "text-fg-secondary" : "text-fg-emphasis",
            )}
          >
            {TITLE[failure.kind] ?? "Failed"}
          </p>
          <p className="mt-0.5 text-meta text-fg-muted">{failure.message}</p>
        </div>
      </div>

      {failure.detail && failure.detail !== failure.message ? (
        <div className="rounded-md border border-border-control bg-inset p-2.5">
          <p className="mb-1 text-label tracking-[.06em] text-fg-dim uppercase">Detail</p>
          {/* The Go error, verbatim and masked. It is what to paste into a bug
              report, and occasionally the only thing that says which hop
              failed. */}
          <p className="font-mono text-code break-words text-fg-dim">{failure.detail}</p>
        </div>
      ) : null}

      <p className="font-mono text-meta text-fg-faint">
        after {formatDuration(failure.durationMs)}
      </p>
    </div>
  );
}
