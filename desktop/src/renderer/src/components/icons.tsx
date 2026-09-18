// The design's line icons, drawn on a 24-unit grid with a 1.6 stroke.

function Icon({ size = 18, children, className }: { size?: number; children: React.ReactNode; className?: string }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      aria-hidden
      className={className}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {children}
    </svg>
  );
}

type P = { size?: number; className?: string };

export const OverviewIcon = (p: P) => (
  <Icon {...p}>
    <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
    <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
    <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
    <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
  </Icon>
);

export const SpendingIcon = (p: P) => (
  <Icon {...p}>
    <path d="M11 3.6A8.5 8.5 0 1 0 20.4 13H11z" />
    <path d="M14 3.4a8.5 8.5 0 0 1 6.6 6.6H14z" />
  </Icon>
);

export const CashFlowIcon = (p: P) => (
  <Icon {...p}>
    <path d="M3.5 17l5-5 4 3 7.5-8" />
    <path d="M15 7h5v5" />
  </Icon>
);

export const RecurringIcon = (p: P) => (
  <Icon {...p}>
    <path d="M4 12a8 8 0 0 1 13.7-5.6L20 8.5" />
    <path d="M20 4v4.5h-4.5" />
    <path d="M20 12a8 8 0 0 1-13.7 5.6L4 15.5" />
    <path d="M4 20v-4.5h4.5" />
  </Icon>
);

export const AccountsIcon = (p: P) => (
  <Icon {...p}>
    <path d="M3.5 9.5L12 4l8.5 5.5" />
    <path d="M5.5 10v7M10 10v7M14 10v7M18.5 10v7" />
    <path d="M3.5 20h17" />
  </Icon>
);

export const TransactionsIcon = (p: P) => (
  <Icon {...p}>
    <path d="M4 7h16M4 12h16M4 17h10" />
  </Icon>
);

export const SettingsIcon = (p: P) => (
  <Icon {...p}>
    <path d="M4 7h10M18 7h2M4 17h4M12 17h8" />
    <circle cx="16" cy="7" r="2" />
    <circle cx="10" cy="17" r="2" />
  </Icon>
);

export const ProfileIcon = (p: P) => (
  <Icon {...p}>
    <circle cx="12" cy="8.5" r="3.5" />
    <path d="M5 19.5a7 7 0 0 1 14 0" />
  </Icon>
);

export const SearchIcon = (p: P) => (
  <Icon {...p}>
    <circle cx="11" cy="11" r="6.5" />
    <path d="M16 16l4 4" />
  </Icon>
);

export const SyncIcon = (p: P) => (
  <Icon {...p}>
    <path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" />
    <path d="M19.5 4.5v4h-4" />
  </Icon>
);

export const PlusIcon = (p: P) => (
  <Icon {...p}>
    <path d="M12 5v14M5 12h14" />
  </Icon>
);

export const ChevronDownIcon = (p: P) => (
  <Icon {...p}>
    <path d="M6 9l6 6 6-6" />
  </Icon>
);

export const MoreIcon = (p: P) => (
  <Icon {...p}>
    <circle cx="5.5" cy="12" r="1" />
    <circle cx="12" cy="12" r="1" />
    <circle cx="18.5" cy="12" r="1" />
  </Icon>
);

export const TagIcon = (p: P) => (
  <Icon {...p}>
    <path d="M3.5 12.5V4.5a1 1 0 0 1 1-1h8l8 8-8 8z" />
    <circle cx="8" cy="8" r="1.3" />
  </Icon>
);

export const TrashIcon = (p: P) => (
  <Icon {...p}>
    <path d="M4 7h16M9.5 7V4.5h5V7M6.5 7l1 13h9l1-13" />
  </Icon>
);

export const PencilIcon = (p: P) => (
  <Icon {...p}>
    <path d="M4 20h4l11-11-4-4L4 16z" />
    <path d="M13 7l4 4" />
  </Icon>
);

export const EyeOffIcon = (p: P) => (
  <Icon {...p}>
    <path d="M3 3l18 18" />
    <path d="M10.6 6.3A9.7 9.7 0 0 1 12 6c5 0 8.5 4 9.5 6-.4.8-1.2 2-2.4 3.1M6.4 6.9C4.4 8.2 3 10.2 2.5 12c1 2 4.5 6 9.5 6 1.5 0 2.8-.4 4-1" />
    <path d="M9.9 9.9a3 3 0 0 0 4.2 4.2" />
  </Icon>
);

export const LockIcon = (p: P) => (
  <Icon {...p}>
    <rect x="5" y="10.5" width="14" height="9.5" rx="2" />
    <path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5" />
  </Icon>
);

// Mark is the app's logo: a ring half filled, a year half gone.
export function Mark({ size = 22 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden>
      <circle cx="12" cy="12" r="9.5" fill="none" stroke="var(--color-clay)" strokeWidth={1.8} />
      <path d="M12 2.5a9.5 9.5 0 0 1 0 19z" fill="var(--color-clay)" />
    </svg>
  );
}
