import { Skeleton } from "@/components/ui/skeleton";

// Loading holds a page's shape while its data arrives: the eyebrow and
// headline, a row of figures, and two panels.
export function Loading() {
  return (
    <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading">
      <div className="flex flex-col gap-3">
        <Skeleton className="h-3 w-48 bg-line" />
        <Skeleton className="h-10 w-[min(640px,80%)] bg-line" />
      </div>
      <div className="grid grid-cols-2 gap-4 xl:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-28 rounded-[14px] bg-line/70" />
        ))}
      </div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Skeleton className="h-72 rounded-[14px] bg-line/70" />
        <Skeleton className="h-72 rounded-[14px] bg-line/70" />
      </div>
    </div>
  );
}
