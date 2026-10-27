import { AlertDialog, AlertDialogClose, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogPopup, AlertDialogTitle, Button, Icon, Input, Tip } from "@berth/plugin/ui";
import { useId, useState } from "react";

import { type Connection, type Database, faultOf } from "./api";
import { addressProblem, cannotLook, connected, EXAMPLE } from "./dotenv.ts";
import { HELPER } from "./ui";

// A database connected by hand: the person pastes its address, and the box
// hands it to the helper, which keeps it as it keeps the ones a service or
// the worktree's .env gave. The address is a secret. It is in this dialog's
// field until it is sent and nowhere after: not in the plugin's storage, not
// in what the panel shows, which is the connection it became.

export interface At {
  box: string;
  location: string;
  // The worktree's name on the box; "" for the main checkout.
  worktree: string;
}

// ConnectDialog asks for the address, says which worktree and box it is
// for, what kind of database is taken and where the address is kept.
export function ConnectDialog({ open, api, at, name, onConnected, onClose }: { open: boolean; api: Database; at: At; name: string; onConnected(id: string): void; onClose(): void }) {
  const [address, setAddress] = useState("");
  const [shown, setShown] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const field = useId();
  const { box } = at;
  const close = () => {
    // Nothing of it stays in the page once the dialog is gone.
    setAddress("");
    setShown(false);
    setProblem(undefined);
    onClose();
  };
  const go = async () => {
    const wrong = addressProblem(address);
    if (wrong) {
      setProblem(wrong);
      return;
    }
    setBusy(true);
    setProblem(undefined);
    try {
      const made = connected(await api.connect(box, at.location, at.worktree, address.trim()));
      if ("id" in made) {
        onConnected(made.id);
        close();
      } else if ("old" in made) {
        setProblem(`${box}'s berthd is from before it could take an address. Update the box in Settings → Boxes, then connect the database again.`);
      } else {
        // The helper's rule, as its own documentation has it: it passes
        // such an address over, and says why only in its log on the box.
        setProblem(`${HELPER} on ${box} didn't take this address. It opens a Postgres on ${box} itself and nothing else: one at localhost, 127.0.0.1 or a Unix socket. Nothing was kept.`);
      }
    } catch (err) {
      const fault = faultOf(err);
      setProblem(cannotLook(fault) ? `${box}'s berthd is from before it could take an address. Update the box in Settings → Boxes, then connect the database again.` : fault.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <AlertDialog
      open={open}
      onOpenChange={(o: boolean) => {
        if (!o) close();
      }}
    >
      <AlertDialogPopup className="sm:max-w-lg" data-testid="database-connect">
        <AlertDialogHeader>
          <AlertDialogTitle>
            Connect a database to {name} on {box}
          </AlertDialogTitle>
          <AlertDialogDescription>
            Paste the address of {name}'s development database. It has to be a Postgres on {box} itself (<code>localhost</code>, <code>127.0.0.1</code> or a Unix socket): <code>{HELPER}</code> opens no other, and a database on another machine is not taken.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <form
          className="mx-6 space-y-2 pb-4"
          autoComplete="off"
          onSubmit={(e) => {
            e.preventDefault();
            void go();
          }}
        >
          <label htmlFor={field} className="block font-medium text-sm">
            Address
          </label>
          <div className="flex items-center gap-1.5">
            <Input
              id={field}
              // Masked like a password: it holds one.
              type={shown ? "text" : "password"}
              value={address}
              onChange={(e: { target: { value: string } }) => {
                setAddress(e.target.value);
                setProblem(undefined);
              }}
              placeholder={EXAMPLE}
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
              spellCheck={false}
              data-1p-ignore
              data-lpignore="true"
              aria-invalid={problem ? true : undefined}
              aria-describedby={`${field}-kept`}
              className="min-w-0 flex-1 font-mono"
              autoFocus
            />
            <Tip label={shown ? "Hide the address" : "Show the address"}>
              <Button type="button" size="icon-sm" variant="ghost" aria-label={shown ? "Hide the address" : "Show the address"} aria-pressed={shown} onClick={() => setShown((s) => !s)}>
                <Icon name={shown ? "EyeOff" : "Eye"} className="size-3.5" />
              </Button>
            </Tip>
          </div>
          {problem && (
            <p role="alert" className="whitespace-pre-wrap rounded-md bg-destructive/8 p-2 text-destructive-foreground text-xs" data-testid="database-connect-problem">
              {problem}
            </p>
          )}
          <p id={`${field}-kept`} className="text-pretty text-muted-foreground text-xs">
            The address is kept on {box}, in a file only you can read there, until {name} is removed, you forget the connection, or Database's data on {box} is removed. It isn't kept on this computer, and isn't shown again: the connection shows as its database, host, port and user. It opens read-only, like any other.
          </p>
        </form>
        <AlertDialogFooter>
          <AlertDialogClose render={<Button variant="ghost" />}>Cancel</AlertDialogClose>
          <Button loading={busy} onClick={() => void go()}>
            Connect
          </Button>
        </AlertDialogFooter>
      </AlertDialogPopup>
    </AlertDialog>
  );
}

// ForgetDialog asks before a database that was connected by hand is
// forgotten.
export function ForgetDialog({ open, api, at, name, conn, onForgotten, onClose }: { open: boolean; api: Database; at: At; name: string; conn: Connection; onForgotten(): void; onClose(): void }) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const { box } = at;
  const go = async () => {
    setBusy(true);
    setProblem(undefined);
    try {
      await api.forget(box, at.location, at.worktree);
      onForgotten();
      onClose();
    } catch (err) {
      setProblem(faultOf(err).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <AlertDialog
      open={open}
      onOpenChange={(o: boolean) => {
        if (!o) {
          setProblem(undefined);
          onClose();
        }
      }}
    >
      <AlertDialogPopup className="sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>
            Forget {conn.database} for {name}?
          </AlertDialogTitle>
          <AlertDialogDescription>
            <code>{HELPER}</code> on {box} forgets the address you gave it for <code>{conn.database}</code>
            {conn.host && ` (${conn.host}${conn.port ? `:${conn.port}` : ""})`}, and the connection goes from this tab with anything you had staged for it. The database itself isn't touched. To see it here again, connect it again with its address.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {problem && <p className="mx-6 whitespace-pre-wrap rounded-md bg-destructive/8 p-2 text-destructive-foreground text-xs">{problem}</p>}
        <AlertDialogFooter>
          <AlertDialogClose render={<Button variant="ghost" />}>Cancel</AlertDialogClose>
          <Button variant="destructive" loading={busy} onClick={() => void go()}>
            Forget this connection
          </Button>
        </AlertDialogFooter>
      </AlertDialogPopup>
    </AlertDialog>
  );
}
