import { createContext, useContext, useEffect, type CSSProperties } from "react";
import { PanelLeftIcon } from "@/ds/icons";
import { IconButton } from "./icon-button";
import { Tooltip } from "./tooltip";

/*
 * The collapsed sidebar's way back. The shell owns the sidebar and
 * provides its state here. By default the shell floats the control at
 * the pane's corner, where its own "Hide sidebar" button was. A layout
 * whose title would sit against that corner (Page hugging the pane's
 * edge, or a screen with its own header there) claims the control and
 * renders it on the title line instead, so the icon reads as the first
 * thing on that line rather than something hovering over it.
 */
export interface SidebarHandle {
  collapsed: boolean;
  /* Show the sidebar, releasing any hold; a no-op when already shown. */
  reopen: () => void;
  /* For a screen that wants the pane to itself (the guided setup):
   * held closed while the screen says so, on top of the person's own
   * preference, which it never touches. */
  hold: (held: boolean) => void;
  /* Whether the caller is placing the control itself right now. */
  claim: (owned: boolean) => void;
  /* Something in the hidden sidebar wants the person (the to-do bell
   * has items), so the control carries the same mark. */
  attention: boolean;
}

export const SidebarContext = createContext<SidebarHandle | null>(null);

export function useSidebar(): SidebarHandle | null {
  return useContext(SidebarContext);
}

/* The control's box, for placing a title beside it. */
export const SIDEBAR_REOPEN_SIZE = 26;

/* Renders nothing while the sidebar is open or absent. With claim, the
 * caller takes the control over from the shell's corner for as long as
 * it is mounted (Page claims on its own terms instead). */
export function SidebarReopenControl({
  className,
  style,
  claim = false,
}: {
  className?: string;
  style?: CSSProperties;
  claim?: boolean;
}) {
  const sidebar = useSidebar();
  const collapsed = sidebar?.collapsed === true;
  const take = sidebar?.claim;
  useEffect(() => {
    if (!claim || !take) return;
    take(collapsed);
    return () => take(false);
  }, [claim, take, collapsed]);
  if (!collapsed) return null;
  return (
    <span className={className} style={style}>
      <Tooltip content="Show sidebar" keys="[">
        <IconButton
          aria-label={sidebar.attention ? "Show sidebar, you have things to do" : "Show sidebar"}
          variant="neutral"
          size="sm"
          attention={sidebar.attention}
          onClick={sidebar.reopen}
        >
          <PanelLeftIcon width={15} height={15} />
        </IconButton>
      </Tooltip>
    </span>
  );
}
