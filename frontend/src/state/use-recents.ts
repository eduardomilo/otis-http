import { useCallback, useEffect, useMemo, useState } from "react";

import { AppService, SettingsService } from "@bindings/internal/services";
import type { Recent } from "@bindings/internal/settings";
import { relativeTime } from "@/lib/time";
import { useSettings } from "@/state/settings-context";

/**
 * The recent-collections list, ready to render: paths abbreviated to the
 * "~/code/..." form the design shows, and a remove action for an entry whose
 * directory has gone.
 *
 * `missing` is computed by Go on every read, so a directory that comes back
 * stops being missing without the window doing anything.
 */
export function useRecents() {
  const { settings, reload } = useSettings();
  const [home, setHome] = useState("");

  useEffect(() => {
    void AppService.HomeDir().then(setHome);
  }, []);

  const recents = useMemo(
    () =>
      (settings?.recents ?? []).map((recent) => ({
        ...recent,
        display: abbreviate(recent.path, home),
      })),
    [settings?.recents, home],
  );

  const remove = useCallback(
    async (path: string) => {
      await SettingsService.RemoveRecent(path);
      await reload();
    },
    [reload],
  );

  return { recents, remove, format: relativeTime };
}

export type DisplayRecent = Recent & { display: string };

/** Replaces the home directory prefix with "~". */
function abbreviate(path: string, home: string): string {
  if (!home || !path.startsWith(home)) return path;
  const rest = path.slice(home.length);
  if (rest !== "" && !rest.startsWith("/") && !rest.startsWith("\\")) return path;
  return `~${rest}`;
}

/** Exposed for the title strip, which shows the same abbreviated form. */
export function abbreviatePath(path: string, home: string): string {
  return abbreviate(path, home);
}

/** Reads the home directory once, for components that only need the prefix. */
export function useHomeDir(): string {
  const [home, setHome] = useState("");
  useEffect(() => {
    void AppService.HomeDir().then(setHome);
  }, []);
  return home;
}
