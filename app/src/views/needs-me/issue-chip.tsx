import { ExternalLinkIcon } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

import { IssueChipsContext } from "@/components/conversation/issue-text";
import { Markdown } from "@/components/conversation/markdown";
import { Button } from "@/components/ui/button";
import { Sheet, SheetDescription, SheetFooter, SheetHeader, SheetPanel, SheetPopup, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/format";
import { linearApi } from "@/lib/linear";
import { openUrl } from "@/lib/open-url";
import { useStore } from "@/lib/store";
import type { LinearIssueDetail } from "@berth/plugin";

// IssueChips gives what's inside it chips that open the issue, when Linear is
// connected on this computer (Linear through the laptop agent, lib/linear).
export function IssueChips({ children }: { children: ReactNode }) {
  const client = useStore((s) => s.client);
  const [connected, setConnected] = useState(false);
  const [id, setId] = useState<string>();
  const [issue, setIssue] = useState<{ id: string; detail?: LinearIssueDetail; error?: string }>();
  useEffect(() => {
    if (!client) return;
    let alive = true;
    void linearApi
      .status(client)
      .then((s) => alive && setConnected(s.connected))
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [client]);
  useEffect(() => {
    if (!client || !id) return;
    let alive = true;
    setIssue({ id });
    void linearApi
      .issue(client, id)
      .then((detail) => alive && setIssue({ id, detail }))
      .catch((err: unknown) => alive && setIssue({ id, error: errorMessage(err) }));
    return () => {
      alive = false;
    };
  }, [client, id]);
  const detail = issue?.id === id ? issue?.detail : undefined;
  const error = issue?.id === id ? issue?.error : undefined;
  return (
    <IssueChipsContext value={connected ? setId : undefined}>
      {children}
      <Sheet open={!!id} onOpenChange={(o) => !o && setId(undefined)}>
        <SheetPopup side="right" className="max-w-lg" data-testid="issue-sheet">
          <SheetHeader>
            <SheetTitle>
              {id} {detail?.title}
            </SheetTitle>
            {detail && <SheetDescription>{detail.state.name}</SheetDescription>}
          </SheetHeader>
          <SheetPanel>{error ? <p className="text-body text-destructive-foreground">{error}</p> : detail ? <Markdown text={detail.description || "No description."} copy={false} onLink={(url) => void openUrl(url)} /> : <Skeleton className="h-24 w-full" />}</SheetPanel>
          {detail && (
            <SheetFooter>
              <Button size="sm" onClick={() => void openUrl(detail.url)}>
                <ExternalLinkIcon />
                Open in Linear
              </Button>
            </SheetFooter>
          )}
        </SheetPopup>
      </Sheet>
    </IssueChipsContext>
  );
}
