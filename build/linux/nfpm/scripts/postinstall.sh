#!/bin/sh
# Registers what the package just installed with the desktop environment.
#
# Three caches, all of which are stale until told otherwise, and none of which
# is fatal: the app and the `otis` command work regardless. A missing tool is a
# warning, never a failed install.

# The .desktop file: puts Otis in the application menu and records that it
# handles text/x-http.
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q /usr/share/applications || true
else
  echo "otis: update-desktop-database not found; Otis may not appear in the application menu yet." >&2
fi

# The MIME database: makes *.http resolve to text/x-http, which is what makes
# the .desktop file's MimeType line match a real file (see otis-http.xml).
if command -v update-mime-database >/dev/null 2>&1; then
  update-mime-database -n /usr/share/mime || true
else
  echo "otis: update-mime-database not found; double-clicking a .http file may not open Otis yet." >&2
fi

# The icon cache: without this the menu entry and the .http file icon are
# generic until the next login.
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor 2>/dev/null || true
fi

exit 0
