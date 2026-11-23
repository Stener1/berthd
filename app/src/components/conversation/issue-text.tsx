import { createContext, type ReactNode, useContext } from "react";

// Issue ids in a question's words as chips, ported from the Intake plugin's. The
// lookup is optional: under a provider an id opens the issue, and with nothing to
// look it up in, an id is just text.

const ISSUE_ID = /\b[A-Z][A-Z0-9]{0,9}-\d+\b/g;

export const IssueChipsContext = createContext<((id: string) => void) | undefined>(undefined);

function Chip({ id }: { id: string }) {
  const open = useContext(IssueChipsContext);
  if (!open) return id;
  const go = (e: React.SyntheticEvent) => {
    // Inside an option it doesn't pick the option.
    e.preventDefault();
    e.stopPropagation();
    open(id);
  };
  return (
    <span role="link" tabIndex={0} data-testid="issue-chip" className="cursor-pointer rounded-sm font-medium text-primary underline decoration-dotted underline-offset-2 outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={go} onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && go(e)}>
      {id}
    </span>
  );
}

// IssueText is agent text with its ids as chips: still only text, never markup.
export function IssueText({ text }: { text: string }) {
  const out: ReactNode[] = [];
  let at = 0;
  for (const m of text.matchAll(ISSUE_ID)) {
    out.push(text.slice(at, m.index));
    out.push(<Chip key={m.index} id={m[0]} />);
    at = m.index + m[0].length;
  }
  if (!out.length) return text;
  out.push(text.slice(at));
  return <>{out}</>;
}
