import {
  Apple, ArrowLeftRight, Baby, BadgePercent, Banknote, Bed, Beer, Bike, Bitcoin, BookOpen, Briefcase, Building2, Bus, Cake, Calculator, Camera,
  Car, Cat, CircleDashed, Clapperboard, Coffee, Coins, CreditCard, Crown, Dices, Dog, Droplets, Dumbbell, Film, Fish, Flame, Flower2, Fuel,
  Gamepad2, Gem, Gift, Glasses, Globe, GraduationCap, Hammer, HandCoins, HandHeart, Headphones, Heart, HeartPulse, Hospital, House, Lamp,
  Landmark, Laptop, Leaf, Mail, Mountain, Music, Newspaper, Package, Palette, PawPrint, Pencil, Percent, Phone, PiggyBank, Pill, Pizza,
  Plane, PlugZap, Receipt, Repeat, School, Scissors, Shield, Shirt, Ship, ShoppingBag, ShoppingBasket, ShoppingCart, Smartphone, Sofa,
  Sparkles, Star, Stethoscope, Syringe, Tag, Tent, Ticket, ToyBrick, Train, TreePine, TrendingUp, Trophy, Truck, Tv, Umbrella, Utensils,
  Vault, Wallet, WashingMachine, Watch, Waves, Wifi, Wine, Wrench, Zap, type LucideIcon,
} from "lucide-react";

import { createElement, type ComponentProps } from "react";

import { cn } from "@/lib/utils";

import { category as lookup, type Category } from "@shared/categories";

// The icons a category may use, by the name stored in topper.categories.
// A curated set rather than all of lucide: the picker shows every one.
export const CATEGORY_ICONS: Record<string, LucideIcon> = {
  "shopping-basket": ShoppingBasket, utensils: Utensils, "shopping-bag": ShoppingBag, car: Car, "heart-pulse": HeartPulse, stethoscope: Stethoscope,
  sparkles: Sparkles, clapperboard: Clapperboard, plane: Plane, "graduation-cap": GraduationCap, "paw-print": PawPrint, gift: Gift,
  "circle-dashed": CircleDashed, house: House, "plug-zap": PlugZap, repeat: Repeat, landmark: Landmark, receipt: Receipt, banknote: Banknote,
  "badge-percent": BadgePercent, "arrow-left-right": ArrowLeftRight, tag: Tag,
  coffee: Coffee, pizza: Pizza, cake: Cake, apple: Apple, wine: Wine, beer: Beer, "shopping-cart": ShoppingCart, package: Package, shirt: Shirt,
  glasses: Glasses, watch: Watch, gem: Gem, bus: Bus, train: Train, bike: Bike, fuel: Fuel, truck: Truck, ship: Ship, wrench: Wrench,
  hammer: Hammer, sofa: Sofa, lamp: Lamp, bed: Bed, "washing-machine": WashingMachine, droplets: Droplets, flame: Flame, zap: Zap, wifi: Wifi,
  phone: Phone, smartphone: Smartphone, laptop: Laptop, tv: Tv, headphones: Headphones, music: Music, film: Film, gamepad: Gamepad2, dices: Dices,
  ticket: Ticket, camera: Camera, palette: Palette, trophy: Trophy, dumbbell: Dumbbell, pill: Pill, syringe: Syringe, hospital: Hospital,
  scissors: Scissors, "flower-2": Flower2, leaf: Leaf, "tree-pine": TreePine, mountain: Mountain, tent: Tent, waves: Waves, umbrella: Umbrella,
  globe: Globe, "book-open": BookOpen, pencil: Pencil, school: School, calculator: Calculator, briefcase: Briefcase, "building-2": Building2,
  mail: Mail, newspaper: Newspaper, baby: Baby, "toy-brick": ToyBrick, dog: Dog, cat: Cat, fish: Fish, heart: Heart, "hand-heart": HandHeart,
  star: Star, crown: Crown, wallet: Wallet, "piggy-bank": PiggyBank, "credit-card": CreditCard, coins: Coins, "hand-coins": HandCoins, vault: Vault,
  bitcoin: Bitcoin, "trending-up": TrendingUp, percent: Percent, shield: Shield,
};

export const ICON_NAMES = Object.keys(CATEGORY_ICONS);

export function iconFor(name: string | null | undefined): LucideIcon {
  return CATEGORY_ICONS[name ?? ""] ?? Tag;
}

// Glyph renders the icon by name. The lucide components are static, so
// looking one up per render is fine; createElement keeps that explicit.
export function Glyph({ name, ...props }: { name: string | null | undefined } & ComponentProps<LucideIcon>) {
  return createElement(iconFor(name), props);
}

// CategoryIcon is the category's glyph in a tinted circle, the way a row
// shows what a transaction was for.
export function CategoryIcon({
  category,
  id,
  size = 32,
  muted = false,
  className,
}: {
  category?: Pick<Category, "icon" | "color">;
  id?: string | null;
  size?: number;
  // Grey, for a transaction that still needs a category.
  muted?: boolean;
  className?: string;
}) {
  const c = category ?? lookup(id);
  const color = muted ? "var(--color-stone)" : c.color;
  return (
    <span
      aria-hidden
      className={cn("inline-flex shrink-0 items-center justify-center rounded-full", className)}
      style={{ width: size, height: size, background: `color-mix(in srgb, ${color} 14%, var(--color-sheet))`, color }}
    >
      <Glyph name={c.icon} size={Math.round(size * 0.5)} strokeWidth={1.75} />
    </span>
  );
}

// CategoryTag is the inline name with a small swatch: for lists, legends
// and pickers.
export function CategoryTag({ id, category, className }: { id?: string | null; category?: Category; className?: string }) {
  const c = category ?? lookup(id);
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1.5", className)}>
      <Glyph name={c.icon} size={14} strokeWidth={1.75} style={{ color: c.color }} className="shrink-0" />
      <span className="truncate">{c.label}</span>
    </span>
  );
}
