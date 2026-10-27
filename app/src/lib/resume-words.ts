// resumeWords are what a structured chat answers with its picker: /resume
// (or /continue) and what follows it, which the list opens filtered by.
// Anything else is undefined, and is sent to the agent.
export function resumeWords(text: string): string | undefined {
  const m = text.trim().match(/^\/(?:resume|continue)(?:\s+([\s\S]*))?$/);
  return m ? (m[1] ?? "").trim() : undefined;
}
