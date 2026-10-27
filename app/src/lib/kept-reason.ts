// keptReason reads a box's refusal to remove a worktree unforced: why it
// was kept, in words for the "Kept" toast that offers Remove anyway, or
// null when the error is something else that forcing wouldn't fix.
export function keptReason(msg: string): string | null {
  if (/submodule commits/i.test(msg)) return "A submodule in it has commits or stashes no remote has, so it was left as it is.";
  if (/could not be checked/i.test(msg)) return "Shipyard couldn't check it for work that removing it would lose, so it was left as it is.";
  // git's own words for work it would lose, which the box's refusals use too.
  if (/modified or untracked files/i.test(msg)) return "It has uncommitted changes, so git left it as it is.";
  return null;
}
