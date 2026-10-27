// The mark: a hull seen bow-on, on its keel blocks, with the amber dot (the
// one lamp) lit inside it, and the lowercase wordmark. The same drawing as
// the landing page's header and site/assets/favicon.svg.
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="56 64 400 386" aria-hidden="true" className={className}>
      <path d="M88 96H424Q410 300 256 350Q102 300 88 96Z" fill="none" stroke="currentColor" strokeWidth="60" strokeLinecap="round" strokeLinejoin="round" />
      <rect x="138" y="404" width="58" height="44" rx="9" fill="currentColor" />
      <rect x="227" y="404" width="58" height="44" rx="9" fill="currentColor" />
      <rect x="316" y="404" width="58" height="44" rx="9" fill="currentColor" />
      <circle cx="256" cy="190" r="30" fill="var(--berth-amber)" />
    </svg>
  );
}

export function Logo() {
  return (
    <span className="berth-brand">
      <Mark className="berth-brand-mark" />
      <span className="berth-brand-word">shipyard</span>
      <span className="berth-brand-docs">docs</span>
    </span>
  );
}
