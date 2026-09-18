import { Skeleton } from "@/components/ui/skeleton";

// Loading holds a page's shape while its data arrives: the header, a strip
// of figures, and two panels.
export function Loading() {
  return (
    <div className="flex flex-col gap-5" aria-busy="true" aria-label="Loading">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-6 w-48 rounded-[3px] bg-line" />
        <Skeleton className="h-3.5 w-80 rounded-[3px] bg-line/70" />
      </div>
      <div className="grid grid-cols-12 gap-4">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="col-span-6 h-[108px] rounded-[4px] bg-line/70 xl:col-span-3" />
        ))}
        <Skeleton className="col-span-12 h-72 rounded-[4px] bg-line/70 xl:col-span-8" />
        <Skeleton className="col-span-12 h-72 rounded-[4px] bg-line/70 xl:col-span-4" />
      </div>
    </div>
  );
}
