import { useEffect, useState } from "react";

import { AppService } from "@bindings/internal/services";
import type { Info } from "@bindings/internal/buildinfo";

/**
 * The running build: version, commit, build date, toolchain and target.
 *
 * Otis ships no auto-updater, so "which version am I on" is a question the
 * user has to be able to answer by looking (DESIGN-NOTES §9.18). Go owns the
 * answer — `internal/buildinfo`, stamped by the linker — and this reads it
 * once per window. It cannot change while the window is open, so there is no
 * event and nothing to re-fetch.
 *
 * Returns null until the first call answers, and stays null if it fails: a
 * version line is the least important thing on any screen it appears on, and
 * a screen that renders an error where a version should be is worse than one
 * that renders nothing.
 */
export function useBuildInfo(): Info | null {
  const [info, setInfo] = useState<Info | null>(null);

  useEffect(() => {
    let live = true;
    void AppService.Build()
      .then((got) => {
        if (live) setInfo(got);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);

  return info;
}

/**
 * The build as one short line: `v0.2.0 · 1a2b3c4`.
 *
 * Shorter than what AppService.CopyVersion puts on the clipboard, which names
 * the toolchain and platform too. What is worth *showing* is the version and
 * the commit — the version because it is the thing people say out loud, the
 * commit because between two tags every build calls itself the same thing.
 * The rest matters only in a bug report, which is what copying is for.
 *
 * An unstamped build (`go build` with no ldflags) has no commit, and says just
 * "dev" rather than inventing one.
 */
export function buildLabel(info: Info | null): string {
  if (!info) return "";
  return info.commit ? `${info.version} · ${info.commit}` : info.version;
}
