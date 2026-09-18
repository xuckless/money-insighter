import { useState } from "react";
import { toast } from "sonner";

import { CategoryIcon, Glyph, ICON_NAMES } from "@/components/category-icon";
import { Segmented } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useCategories } from "@/hooks/use-categories";
import { useDataVersion } from "@/hooks/use-data-version";
import { deleteCategory, saveCategory } from "@/lib/queries";
import { cn } from "@/lib/utils";

import { categoryPalette, kindLabel, kindOrder, slugId, type Category, type CategoryKind } from "@shared/categories";

// CategoryDialog creates a category or edits one. A built-in category can
// be renamed, recoloured and given another icon, but keeps its kind: the
// Plaid mapping and the screens lean on it.
export function CategoryDialog({ open, onOpenChange, editing }: { open: boolean; onOpenChange: (o: boolean) => void; editing: Category | null }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">{open && <CategoryForm editing={editing} onDone={() => onOpenChange(false)} />}</DialogContent>
    </Dialog>
  );
}

function CategoryForm({ editing, onDone }: { editing: Category | null; onDone: () => void }) {
  const { list, reload } = useCategories();
  const { bump } = useDataVersion();
  const [label, setLabel] = useState(editing?.label ?? "");
  const [color, setColor] = useState<string>(editing?.color ?? categoryPalette[list.length % categoryPalette.length]);
  const [icon, setIcon] = useState(editing?.icon ?? "tag");
  const [kind, setKind] = useState<CategoryKind>(editing?.kind ?? "spending");
  const [saving, setSaving] = useState(false);
  const valid = label.trim().length > 0 && label.trim().length <= 40;

  const save = async () => {
    setSaving(true);
    try {
      const id = editing?.id ?? slugId(label, (s) => list.some((c) => c.id === s));
      const sortOrder = editing?.sortOrder ?? 500 + list.filter((c) => !c.builtin).length;
      await saveCategory({ id, label: label.trim(), color, icon, kind, builtin: editing?.builtin ?? false, sortOrder });
      toast.success(editing ? "Category saved" : `${label.trim()} added`);
      reload();
      bump();
      onDone();
    } catch (err) {
      toast.error("Could not save the category", { description: (err as Error).message });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>{editing ? "Edit category" : "New category"}</DialogTitle>
        <DialogDescription>
          {editing?.builtin
            ? "Built-in categories keep their kind; the name, colour and icon are yours."
            : "Pick a kind: spending is paced day by day, bills land on a schedule."}
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-4">
        <div className="flex items-center gap-3">
          <CategoryIcon category={{ icon, color }} size={44} />
          <div className="grid flex-1 gap-1.5">
            <Label htmlFor="cat-name">Name</Label>
            <Input id="cat-name" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Hobbies" maxLength={40} autoFocus />
          </div>
        </div>
        <div className="grid gap-1.5">
          <Label>Kind</Label>
          {editing?.builtin ? (
            <div className="text-[13.5px] text-ink-2">{kindLabel[kind]}</div>
          ) : (
            <Select value={kind} onValueChange={(v) => setKind(v as CategoryKind)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {kindOrder.map((k) => (
                  <SelectItem key={k} value={k}>
                    {kindLabel[k]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
        <div className="grid gap-1.5">
          <Label>Colour</Label>
          <div className="flex flex-wrap gap-1.5">
            {categoryPalette.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={c}
                aria-pressed={c === color}
                onClick={() => setColor(c)}
                className={cn("size-6 cursor-pointer rounded-full border-2", c === color ? "border-ink" : "border-transparent hover:border-line-strong")}
                style={{ background: c }}
              />
            ))}
          </div>
        </div>
        <div className="grid gap-1.5">
          <Label>Icon</Label>
          <div className="grid max-h-40 grid-cols-10 gap-1 overflow-y-auto rounded-[4px] border border-line bg-field p-1.5">
            {ICON_NAMES.map((name) => {
              return (
                <button
                  key={name}
                  type="button"
                  aria-label={name}
                  aria-pressed={name === icon}
                  onClick={() => setIcon(name)}
                  className={cn(
                    "flex size-7 cursor-pointer items-center justify-center rounded-[3px] border-0",
                    name === icon ? "bg-ink text-sheet" : "bg-transparent text-ink-2 hover:bg-row-active hover:text-ink",
                  )}
                >
                  <Glyph name={name} size={15} strokeWidth={1.75} />
                </button>
              );
            })}
          </div>
          <span className="text-xs text-ink-3">
            Chosen: <Glyph name={icon} size={12} className="inline align-[-2px]" /> {icon}
          </span>
        </div>
      </div>
      <DialogFooter>
        <Button variant="outline" onClick={onDone} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={() => void save()} disabled={!valid || saving}>
          {saving ? "Saving…" : editing ? "Save" : "Add category"}
        </Button>
      </DialogFooter>
    </>
  );
}

// DeleteCategoryDialog asks where the category's transactions, rules and
// recurring entries should go, then removes it. Its budget goes with it.
export function DeleteCategoryDialog({
  target,
  onOpenChange,
  usage,
}: {
  target: Category | null;
  onOpenChange: (o: boolean) => void;
  // How many overrides, rules and recurring entries point at it.
  usage: { overrides: number; rules: number; entries: number; budget: boolean } | null;
}) {
  const { list, reload } = useCategories();
  const { bump } = useDataVersion();
  const [into, setInto] = useState<string>("");
  const [working, setWorking] = useState(false);
  const options = list.filter((c) => c.id !== target?.id && c.kind === target?.kind);
  const fallback = options.find((c) => c.id === "other") ?? options[0];
  const chosen = into || fallback?.id || "";
  const inUse = usage ? usage.overrides + usage.rules + usage.entries > 0 : false;

  const remove = async () => {
    if (!target || !chosen) return;
    setWorking(true);
    try {
      await deleteCategory(target.id, chosen);
      toast.success(`${target.label} removed`);
      reload();
      bump();
      onOpenChange(false);
    } catch (err) {
      toast.error("Could not remove the category", { description: (err as Error).message });
    } finally {
      setWorking(false);
    }
  };

  return (
    <Dialog open={target !== null} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {target && (
          <>
            <DialogHeader>
              <DialogTitle>Remove {target.label}?</DialogTitle>
              <DialogDescription>
                {inUse
                  ? `Everything filed under it moves to the category you pick: ${[
                      usage!.overrides ? `${usage!.overrides} transaction${usage!.overrides === 1 ? "" : "s"} you sorted` : "",
                      usage!.rules ? `${usage!.rules} merchant rule${usage!.rules === 1 ? "" : "s"}` : "",
                      usage!.entries ? `${usage!.entries} recurring entr${usage!.entries === 1 ? "y" : "ies"}` : "",
                    ]
                      .filter(Boolean)
                      .join(", ")}.`
                  : "Nothing you sorted points at it."}
                {usage?.budget ? " Its budget is dropped." : ""} Transactions Plaid placed here on its own fall back to their Plaid category.
              </DialogDescription>
            </DialogHeader>
            {options.length > 0 && (
              <div className="grid gap-1.5">
                <Label>Move everything to</Label>
                <Select value={chosen} onValueChange={setInto}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {options.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)} disabled={working}>
                Cancel
              </Button>
              <Button variant="destructive" onClick={() => void remove()} disabled={working || !chosen}>
                {working ? "Removing…" : "Remove category"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// KindSwitch is the small Spending / Bills toggle used on the Categories
// page's filters.
export function KindSwitch({ value, onChange }: { value: CategoryKind | "all"; onChange: (k: CategoryKind | "all") => void }) {
  return (
    <Segmented
      label="Kind"
      size="sm"
      value={value}
      options={[{ value: "all", label: "All" }, ...kindOrder.map((k) => ({ value: k, label: kindLabel[k] }))]}
      onChange={onChange}
    />
  );
}
