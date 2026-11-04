// isViewKey says a key press is a page's to handle as a shortcut: not one a
// modifier makes the app's, not typing, not in a terminal, and not behind a dialog
// or a menu, which have the keyboard to themselves.
export function isViewKey(e: KeyboardEvent): boolean {
  if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return false;
  const t = e.target as HTMLElement | null;
  return !(t?.closest("input, textarea, select, [contenteditable=true], .xterm, [data-terminal]") || document.querySelector("[data-slot=dialog-popup], [data-slot=alert-dialog-popup], [data-slot=sheet-popup], [data-slot=menu-popup], [role=menu]"));
}
