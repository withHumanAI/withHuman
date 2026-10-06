import type { SVGProps } from "react";

/*
 * Hand-rolled 16px stroke icons. 1.5px stroke on a 16 grid, round caps.
 * Sized via the CSS `size-4` (16px) default; override with className.
 */
type IconProps = SVGProps<SVGSVGElement>;

function base(props: IconProps): IconProps {
  return {
    width: 16,
    height: 16,
    viewBox: "0 0 16 16",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.5,
    strokeLinecap: "round",
    strokeLinejoin: "round",
    "aria-hidden": true,
    ...props,
  };
}

export function SunIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="3" />
      <path d="M8 1.5v1.6M8 12.9v1.6M14.5 8h-1.6M3.1 8H1.5M12.6 3.4l-1.2 1.2M4.6 11.4l-1.2 1.2M12.6 12.6l-1.2-1.2M4.6 4.6L3.4 3.4" />
    </svg>
  );
}

export function MoonIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M13.5 9.8A5.7 5.7 0 0 1 6.2 2.5a5.7 5.7 0 1 0 7.3 7.3Z" />
    </svg>
  );
}

export function MonitorIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="1.8" y="2.8" width="12.4" height="8.4" rx="1.5" />
      <path d="M6 13.8h4" />
    </svg>
  );
}

export function CheckIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M3.2 8.4 6.5 11.7 12.8 4.7" />
    </svg>
  );
}

export function DashIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M3.5 8h9" />
    </svg>
  );
}

export function SettingsIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.5 5h4M10.3 5h3.2M2.5 11h6.4M12.7 11h.8" />
      <circle cx="8.4" cy="5" r="1.7" />
      <circle cx="10.8" cy="11" r="1.7" />
    </svg>
  );
}

export function ClockIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="6.2" />
      <path d="M8 4.9V8l2.1 1.6" />
    </svg>
  );
}

export function ChevronUpIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="m4 9.8 4-4 4 4" />
    </svg>
  );
}

export function ChevronDownIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="m4 6.2 4 4 4-4" />
    </svg>
  );
}

export function ArrowDownIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 2.5v11m-4-4 4 4 4-4" />
    </svg>
  );
}

/* An elbow: down, then right into what follows. A pipeline branch's
 * rail drops through it into the blocks the branch runs. */
export function CornerDownRightIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M3.5 2.5v5a2.5 2.5 0 0 0 2.5 2.5h7.5" />
      <path d="m10 6.5 3.5 3.5-3.5 3.5" />
    </svg>
  );
}

export function ChevronLeftIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="m9.8 4-4 4 4 4" />
    </svg>
  );
}

export function ChevronRightIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="m6.2 4 4 4-4 4" />
    </svg>
  );
}

export function InfoIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="6.2" />
      <path d="M8 7.4v3.4" />
      <circle cx="8" cy="5" r="0.9" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function PanelLeftIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="2" y="2.8" width="12" height="10.4" rx="1.8" />
      <path d="M6.2 2.8v10.4" />
    </svg>
  );
}

export function InboxIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.2 9h3.3l1 1.8h3l1-1.8h3.3" />
      <path d="M13.8 9V5.2a1.6 1.6 0 0 0-1.6-1.6H3.8a1.6 1.6 0 0 0-1.6 1.6V9v2.6a1.6 1.6 0 0 0 1.6 1.6h8.4a1.6 1.6 0 0 0 1.6-1.6V9Z" />
    </svg>
  );
}

export function SearchIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="7.2" cy="7.2" r="4.4" />
      <path d="m10.5 10.5 3.3 3.3" />
    </svg>
  );
}

export function ChevronUpDownIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="m5 6 3-3 3 3M5 10l3 3 3-3" />
    </svg>
  );
}

export function XIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M4 4l8 8M12 4l-8 8" />
    </svg>
  );
}

export function EllipsisIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="3.2" cy="8" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="8" cy="8" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="12.8" cy="8" r="0.9" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function CopyIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
      <path d="M10.5 5.5v-2a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" />
    </svg>
  );
}

/* A machine principal: the agent identity and its runtimes. */
export function AgentIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="2.5" y="5.2" width="11" height="8.3" rx="2" />
      <path d="M8 5.2V2.8" />
      <circle cx="8" cy="2.3" r="0.8" fill="currentColor" stroke="none" />
      <circle cx="6" cy="9.3" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="10" cy="9.3" r="0.9" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function PlusIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 3.2v9.6M3.2 8h9.6" />
    </svg>
  );
}

export function ArrowLeftIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M13 8H3M7.5 3.5 3 8l4.5 4.5" />
    </svg>
  );
}

export function ArrowUpRightIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M4.5 11.5 11.5 4.5M5.5 4.5h6v6" />
    </svg>
  );
}

/* Status circles, one per tone, in the Linear mold: the shape says the
 * state so the color never has to be memorized. */
export function CircleIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="6.2" />
    </svg>
  );
}

export function CheckCircleIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="6.2" />
      <path d="M5.3 8.3 7.2 10.2 10.8 6.2" />
    </svg>
  );
}

export function XCircleIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="6.2" />
      <path d="M5.9 5.9l4.2 4.2M10.1 5.9l-4.2 4.2" />
    </svg>
  );
}

export function AlertCircleIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="6.2" />
      <path d="M8 4.9v3.6" />
      <circle cx="8" cy="11" r="0.9" fill="currentColor" stroke="none" />
    </svg>
  );
}

/* Status ring with a filled arc, in the Linear "in progress" mold. */
export function ProgressIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="8" r="5.5" />
      <path
        d="M8 5.2 A2.8 2.8 0 0 1 10.8 8 L8 8 Z"
        fill="currentColor"
        stroke="none"
      />
    </svg>
  );
}

/* Filled rounded square with an exclamation cut in the canvas color. */
export function AlertIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect
        x="2"
        y="2"
        width="12"
        height="12"
        rx="3.5"
        fill="currentColor"
        stroke="none"
      />
      <path d="M8 4.8v3.9" stroke="var(--ds-canvas)" strokeWidth="1.8" />
      <circle cx="8" cy="11.2" r="0.95" fill="var(--ds-canvas)" stroke="none" />
    </svg>
  );
}

export function PersonIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="8" cy="5.2" r="2.7" />
      <path d="M2.8 13.6a5.4 5.4 0 0 1 10.4 0" />
    </svg>
  );
}

export function CalendarIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="2.2" y="3.2" width="11.6" height="10.6" rx="1.8" />
      <path d="M2.2 6.6h11.6M5.4 1.8v2.4M10.6 1.8v2.4" />
    </svg>
  );
}

export function PeopleIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="6" cy="5.4" r="2.4" />
      <path d="M1.8 13.4a4.6 4.6 0 0 1 8.4 0" />
      <path d="M10.6 3.4a2.4 2.4 0 0 1 0 4" />
      <path d="M11.9 9.6a4.6 4.6 0 0 1 2.3 3.8" />
    </svg>
  );
}

/* Everyone at once: a megaphone, for a level that notifies the whole
 * team rather than named members. */
export function BroadcastIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.5 6.5v3a1 1 0 0 0 1 1h1.8l5.2 2.6V2.9L5.3 5.5H3.5a1 1 0 0 0-1 1Z" />
      <path d="M5.3 10.5v2.3a.7.7 0 0 0 .7.7h1" />
      <path d="M12.6 6.2a2.4 2.4 0 0 1 0 3.6" />
    </svg>
  );
}

export function BranchIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <circle cx="4" cy="3.5" r="1.5" />
      <circle cx="12" cy="3.5" r="1.5" />
      <circle cx="8" cy="12.5" r="1.5" />
      <path d="M8 11V9.5C8 7 4 7.5 4 5M8 9.5C8 7 12 7.5 12 5" />
    </svg>
  );
}

export function ShieldIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M8 1.8 13 3.6v4.1c0 3.2-2 5.4-5 6.5-3-1.1-5-3.3-5-6.5V3.6L8 1.8Z" />
    </svg>
  );
}

export function LogIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M3.2 1.8h9.6v12.4H3.2Z" />
      <path d="M5.6 4.9h4.8M5.6 7.5h4.8M5.6 10.1h2.8" />
    </svg>
  );
}

export function TrashIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M2.5 4.3h11M6.2 2.2h3.6M4 4.3l.7 8.3a1.2 1.2 0 0 0 1.2 1.1h4.2a1.2 1.2 0 0 0 1.2-1.1l.7-8.3" />
      <path d="M6.6 7v4M9.4 7v4" />
    </svg>
  );
}

export function GripIcon(props: IconProps) {
  return (
    <svg {...base(props)} stroke="none" fill="currentColor">
      <circle cx="5.75" cy="3.5" r="1.05" />
      <circle cx="10.25" cy="3.5" r="1.05" />
      <circle cx="5.75" cy="8" r="1.05" />
      <circle cx="10.25" cy="8" r="1.05" />
      <circle cx="5.75" cy="12.5" r="1.05" />
      <circle cx="10.25" cy="12.5" r="1.05" />
    </svg>
  );
}

export function LockIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="3.5" y="7" width="9" height="6.5" rx="1.5" />
      <path d="M5.5 7V5.2a2.5 2.5 0 0 1 5 0V7" />
    </svg>
  );
}

export function SignOutIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M6.5 2.5H3.7a1.2 1.2 0 0 0-1.2 1.2v8.6a1.2 1.2 0 0 0 1.2 1.2h2.8" />
      <path d="M10.6 11l3-3-3-3M13.6 8H6.5" />
    </svg>
  );
}

/* A tool an agent calls: a wrench. */
export function ToolIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M10.2 2.3a3.6 3.6 0 0 0-3.9 4.9L2.5 11a1.4 1.4 0 0 0 2 2l3.8-3.8a3.6 3.6 0 0 0 4.9-3.9L11 7.5 8.5 5l2.2-2.2Z" />
    </svg>
  );
}

export function MailIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="1.75" y="3.25" width="12.5" height="9.5" rx="1.5" />
      <path d="M2.2 4.2 8 8.6l5.8-4.4" />
    </svg>
  );
}

export function MessageIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M13.5 3.25h-11a.75.75 0 0 0-.75.75v6.5c0 .41.34.75.75.75h2.25v2.5l3.25-2.5h5.5c.41 0 .75-.34.75-.75V4a.75.75 0 0 0-.75-.75Z" />
    </svg>
  );
}

export function SmartphoneIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <rect x="4.25" y="1.75" width="7.5" height="12.5" rx="1.5" />
      <path d="M7 12h2" />
    </svg>
  );
}

export function LinkIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M6.8 9.2a2.6 2.6 0 0 0 3.7 0l2.3-2.3a2.6 2.6 0 0 0-3.7-3.7L8 4.3" />
      <path d="M9.2 6.8a2.6 2.6 0 0 0-3.7 0L3.2 9.1a2.6 2.6 0 0 0 3.7 3.7L8 11.7" />
    </svg>
  );
}

export function BellIcon(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M3.5 11.6h9l-1.1-1.7V7a3.4 3.4 0 0 0-6.8 0v2.9Z" />
      <path d="M6.6 13.7a1.5 1.5 0 0 0 2.8 0" />
    </svg>
  );
}
