import { Component, type ErrorInfo, type ReactNode } from "react";

import { Scene } from "@/components/art/scenes";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { cn } from "@/lib/utils";

// A way out of what broke, such as closing the tab it is in.
export type Rescue = { label: string; run(): void };

type Props = {
  children: ReactNode;
  // What the boundary guards, for the message: "Automations", "a dialog".
  scope?: string;
  // Shown as a way out, such as back to the workspace.
  onLeave?: () => void;
  leaveLabel?: string;
  // More ways out, asked for when it breaks (what is showing then decides
  // them). Each clears the error and tries again after it.
  rescues?: () => Rescue[];
  // One line rather than a page, for a bar; className places it.
  compact?: boolean;
  className?: string;
  // The whole window's guard: it keeps a strip to drag the window by.
  window?: boolean;
};
type State = { error?: Error; stack?: string; details: boolean };

// ErrorBoundary keeps a render that throws from taking more than its part of
// the window: around a view, the sidebar and every other view keep working.
// Without it React replaces the tree with nothing, the window goes white,
// and what threw is in a console nobody can open: release builds ship
// without devtools. Showing it on screen makes a report from someone else's
// machine actionable.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { details: false };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.setState({ stack: info.componentStack ?? undefined });
    // Kept for anyone who does have a console attached, such as a dev build.
    console.error(`Shipyard: ${this.props.scope ?? "the app"} hit an error it could not render through:`, error, info.componentStack);
  }

  render() {
    const { error, stack, details } = this.state;
    if (!error) return this.props.children;
    const { scope, onLeave, leaveLabel, compact, className, window: whole } = this.props;
    const detail = [error.stack || `${error.name}: ${error.message}`, stack && `Component stack:${stack}`].filter(Boolean).join("\n\n");
    const retry = () => this.setState({ error: undefined, details: false });
    // The first way out leads; in a bar, none shouts.
    const rescues = (this.props.rescues?.() ?? []).map((r, i) => (
      <Button
        key={r.label}
        size={compact ? "xs" : undefined}
        variant={compact || i > 0 ? "outline" : undefined}
        onClick={() => {
          retry();
          r.run();
        }}
      >
        {r.label}
      </Button>
    ));
    if (compact) {
      return (
        <div role="alert" className={cn("flex min-w-0 items-center gap-2 px-2 text-meta", className)}>
          <span className="shrink-0 font-medium">{scope ? `Something in ${scope} broke` : "Shipyard hit an error"}</span>
          <span className="min-w-0 truncate text-muted-foreground">{error.message || String(error)}</span>
          <span className="ml-auto flex shrink-0 items-center gap-1">
            {rescues}
            <Button size="xs" variant="ghost" onClick={retry}>
              Try again
            </Button>
          </span>
        </div>
      );
    }
    return (
      <div role="alert" className={cn("relative flex h-full min-h-0 items-center justify-center overflow-y-auto p-6", className)}>
        {whole && <div data-tauri-drag-region className="absolute inset-x-0 top-0 h-bar" />}
        <Empty className="max-w-xl">
          <EmptyHeader>
            <EmptyMedia>
              <Scene name="storm" />
            </EmptyMedia>
            <EmptyTitle>{scope ? `Something in ${scope} broke` : "Shipyard hit an error"}</EmptyTitle>
            <EmptyDescription>
              {scope ? "The rest of Shipyard still works. " : ""}
              {error.message || String(error)}
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <div className="flex flex-wrap justify-center gap-2">
              {rescues}
              {onLeave && (
                <Button
                  onClick={() => {
                    this.setState({ error: undefined, details: false });
                    onLeave();
                  }}
                >
                  {leaveLabel ?? "Back to workspace"}
                </Button>
              )}
              <Button variant="outline" onClick={retry}>
                Try again
              </Button>
              <Button variant="ghost" onClick={() => navigator.clipboard?.writeText(detail)}>
                Copy details
              </Button>
            </div>
            <button type="button" className="text-muted-foreground text-meta hover:text-foreground" onClick={() => this.setState({ details: !details })}>
              {details ? "Hide details" : "Show details"}
            </button>
            {details && <pre className="max-h-64 w-full overflow-auto rounded-md bg-muted p-3 text-left font-mono text-mono whitespace-pre-wrap select-text">{detail}</pre>}
          </EmptyContent>
        </Empty>
      </div>
    );
  }
}
