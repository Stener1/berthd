// GitHub's callouts: a quote whose first line is [!NOTE], [!TIP],
// [!IMPORTANT], [!WARNING] or [!CAUTION] (in any case, alone on its line)
// draws as a box tinted in its kind's colour, headed by its icon and name.
// Any other quote stays a quote.
//
// It works on micromark's HTML (markdown.tsx), which has escaped whatever
// HTML the reply had: it only adds a class to the quote and its own
// heading, from the five kinds below, so a reply can't add markup through
// it. A draft halfway through its marker is still a plain quote.
// No imports: pnpm test runs this.

// lucide's info, lightbulb, message-square-warning, triangle-alert and
// octagon-alert, as GitHub draws its five.
const ICON = (d: string) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;

export const CALLOUTS = {
  note: { label: "Note", icon: ICON('<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>') },
  tip: { label: "Tip", icon: ICON('<path d="M15 14c.2-1 .7-1.7 1.5-2.5 1-.9 1.5-2.2 1.5-3.5A6 6 0 0 0 6 8c0 1 .2 2.2 1.5 3.5.7.7 1.3 1.5 1.5 2.5"/><path d="M9 18h6"/><path d="M10 22h4"/>') },
  important: { label: "Important", icon: ICON('<path d="M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z"/><path d="M12 15h.01"/><path d="M12 7v4"/>') },
  warning: { label: "Warning", icon: ICON('<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>') },
  caution: { label: "Caution", icon: ICON('<path d="M12 16h.01"/><path d="M12 8v4"/><path d="M15.312 2a2 2 0 0 1 1.414.586l4.688 4.688A2 2 0 0 1 22 8.688v6.624a2 2 0 0 1-.586 1.414l-4.688 4.688a2 2 0 0 1-1.414.586H8.688a2 2 0 0 1-1.414-.586l-4.688-4.688A2 2 0 0 1 2 15.312V8.688a2 2 0 0 1 .586-1.414l4.688-4.688A2 2 0 0 1 8.688 2z"/>') },
} as const;

export type CalloutKind = keyof typeof CALLOUTS;

// The marker as micromark draws a quote's first line: alone in its
// paragraph, or followed by the rest of it on the next line (after a hard
// break, when the agent ended the marker with two spaces or a backslash).
export const CALLOUT = /<blockquote>\n<p>\[!(note|tip|important|warning|caution)\](?:<br \/>)?(\n|<\/p>\n?)/gi;

// callout is CALLOUT's replacement: the quote marked with its kind, and its
// marker swapped for the kind's heading. Text after the marker in the same
// paragraph opens a paragraph of its own under the heading.
export function callout(_: string, marker: string, after: string): string {
  const kind = marker.toLowerCase() as CalloutKind;
  const { label, icon } = CALLOUTS[kind];
  return `<blockquote class="cv-callout" data-callout="${kind}">\n<p class="cv-callout-title">${icon}${label}</p>\n${after === "\n" ? "<p>" : ""}`;
}
