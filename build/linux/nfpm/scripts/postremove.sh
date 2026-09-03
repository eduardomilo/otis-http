#!/bin/sh
# Re-runs the same three caches after removal, so the menu entry and the .http
# association go away rather than pointing at a binary that is no longer there.
# Same rule as postinstall: a missing tool is not a failure.

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q /usr/share/applications || true
fi
if command -v update-mime-database >/dev/null 2>&1; then
  update-mime-database -n /usr/share/mime || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor 2>/dev/null || true
fi

exit 0
