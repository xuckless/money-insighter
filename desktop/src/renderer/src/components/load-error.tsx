import { CircleAlert } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

export function LoadError({ what, message }: { what: string; message: string }) {
  return (
    <Alert variant="destructive">
      <CircleAlert />
      <AlertTitle>Could not load {what}</AlertTitle>
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
}
