import { FlaskConical } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { addSandboxItem } from "@/lib/actions";

export function SandboxButton({ onAdded }: { onAdded: () => void }) {
  const [pending, setPending] = useState(false);
  return (
    <Button
      variant="outline"
      disabled={pending}
      onClick={async () => {
        setPending(true);
        try {
          const res = await addSandboxItem();
          if (!res.ok) {
            toast.error("Could not add Sandbox item", { description: res.error });
            return;
          }
          toast.success(`${res.data.item.institution_name ?? "Sandbox item"} added`, {
            description: "Initial sync queued.",
          });
          onAdded();
        } finally {
          setPending(false);
        }
      }}
    >
      <FlaskConical />
      {pending ? "Adding…" : "Add Sandbox item"}
    </Button>
  );
}
