import { Check, Copy } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useSettings } from "@/hooks/use-app-state";

// KekBackupDialog asks once, after the first successful start, for the
// user to save the key that encrypts bank access tokens.
export function KekBackupDialog() {
  const [settings, refresh] = useSettings();
  const [key, setKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const open = settings !== null && !settings.kekBackedUp;

  useEffect(() => {
    if (open && key === null) window.api.app.revealKek().then(setKey);
  }, [open, key]);

  const copy = async () => {
    if (!key) return;
    await navigator.clipboard.writeText(key);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const done = async () => {
    await window.api.app.markKekBackedUp();
    toast.success("Key backup confirmed");
    refresh();
  };

  return (
    <Dialog open={open}>
      <DialogContent showCloseButton={false} onEscapeKeyDown={(e) => e.preventDefault()} onInteractOutside={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>Back up your encryption key</DialogTitle>
          <DialogDescription>
            This key encrypts the bank access tokens stored on this computer. If it is lost, every
            linked bank becomes unreadable and you will have to reconnect each one. Save it somewhere
            safe, such as a password manager.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <code className="flex-1 truncate rounded-md border bg-muted px-3 py-2 font-mono text-xs">
            {key ?? "…"}
          </code>
          <Button variant="outline" size="icon" onClick={copy} disabled={!key} aria-label="Copy key">
            {copied ? <Check /> : <Copy />}
          </Button>
        </div>
        <DialogFooter>
          <Button onClick={done} disabled={!key}>
            I saved it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
