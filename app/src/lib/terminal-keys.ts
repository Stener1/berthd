// Keys a terminal sends differently from what its renderer would, so they
// do in a terminal here what they do in a Mac terminal (Ghostty's macOS
// defaults, iTerm2's natural text editing).
//
// - ⇧↵ is CSI 13;2u, Shift and Enter. Through berth's tmux (extended-keys,
//   internal/box/sessions.go) a program that asked for modified keys gets
//   Shift+Enter (a newline in Claude Code); a shell gets a plain Enter.
//   Both renderers send a plain Enter for it themselves. Every platform.
// - ⌥← and ⌥→ move a word (ESC b, ESC f), where the renderers send
//   CSI 1;3D and CSI 1;3C, which a shell's line editor doesn't take.
// - ⌘← and ⌘→ go to the line's start and end (Ctrl+A, Ctrl+E), and ⌘⌫
//   deletes back to its start (Ctrl+U). Mac only: on Linux, Ctrl stands in
//   for ⌘ and its keys stay the shell's.

export type TermKey = Pick<KeyboardEvent, "key" | "shiftKey" | "altKey" | "ctrlKey" | "metaKey" | "isComposing">;

const SHIFT_ENTER = "\x1b[13;2u";

const MAC_CMD: Record<string, string> = { ArrowLeft: "\x01", ArrowRight: "\x05", Backspace: "\x15" };
const MAC_OPTION: Record<string, string> = { ArrowLeft: "\x1bb", ArrowRight: "\x1bf" };

// terminalKeyInput is what a keydown sends instead of the renderer's own
// bytes, or undefined to leave it to the renderer.
export function terminalKeyInput(e: TermKey, mac: boolean): string | undefined {
  if (e.isComposing) return undefined;
  const only = (shift: boolean, alt: boolean, ctrl: boolean, meta: boolean) =>
    e.shiftKey === shift && e.altKey === alt && e.ctrlKey === ctrl && e.metaKey === meta;
  if (e.key === "Enter" && only(true, false, false, false)) return SHIFT_ENTER;
  if (!mac) return undefined;
  if (only(false, false, false, true)) return MAC_CMD[e.key];
  if (only(false, true, false, false)) return MAC_OPTION[e.key];
  return undefined;
}
