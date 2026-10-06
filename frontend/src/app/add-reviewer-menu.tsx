import { useState, type ReactElement } from "react";
import { Menu, MenuContent, MenuTrigger, type MenuAction } from "@/ds/components/menu";
import { useToast } from "@/ds/components/toast";
import { api } from "../api";
import type { ApprovalReview } from "../types";

/*
 * The handoff: a targeted reviewer pulls a colleague into a request's
 * routing. Widen, never move: the snapshot gains a name and loses none.
 * A filtering menu on the trigger, in the Linear mold: open, type a
 * name, pick. The list is the active members who hold request.decide
 * through some role, minus the people already on the request, fetched
 * the first time the menu opens.
 */
interface Candidate {
  id: string;
  name: string;
}

type People =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "failed" }
  | { state: "ready"; candidates: Candidate[] };

export function AddReviewerMenu({
  requestId,
  excludedMembershipIds,
  trigger,
  onOpenChange,
  onAdded,
}: {
  requestId: string;
  /* The viewer and everyone already added: pointless to offer. */
  excludedMembershipIds: string[];
  /* The button the menu hangs from. */
  trigger: ReactElement;
  onOpenChange?: (open: boolean) => void;
  /* The review the server returns once the reviewer is on the request. */
  onAdded: (review: ApprovalReview) => void;
}) {
  const toast = useToast();
  const [people, setPeople] = useState<People>({ state: "idle" });
  const [addingId, setAddingId] = useState<string | null>(null);

  function load() {
    setPeople({ state: "loading" });
    const excluded = new Set(excludedMembershipIds);
    void api
      .reviewerCandidates(requestId)
      .then((result) => {
        setPeople({
          state: "ready",
          candidates: result.candidates
            .filter((candidate) => !excluded.has(candidate.membership_id))
            .map((candidate) => ({
              id: candidate.membership_id,
              name: candidate.display_name || candidate.email,
            })),
        });
      })
      .catch(() => setPeople({ state: "failed" }));
  }

  async function add(candidate: Candidate) {
    setAddingId(candidate.id);
    try {
      const review = await api.addReviewer(requestId, candidate.id);
      onAdded(review);
      /* They are off the list now; the next open fetches afresh. */
      setPeople({ state: "idle" });
      toast.add({
        type: "approve",
        title: "Reviewer added",
        description: `${candidate.name} was notified.`,
      });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not add the reviewer",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setAddingId(null);
    }
  }

  /* A state that is not a list of people is one disabled row saying so,
   * so the menu never opens onto nothing. */
  const actions: MenuAction[] =
    people.state === "ready"
      ? people.candidates.length > 0
        ? people.candidates.map((candidate) => ({
            id: candidate.id,
            label: candidate.name,
            disabled: addingId !== null,
            action: () => void add(candidate),
          }))
        : [{ id: "none", label: "Nobody else can review requests", disabled: true, action: () => undefined }]
      : people.state === "failed"
        ? [{ id: "failed", label: "Could not load the people who can review", disabled: true, action: () => undefined }]
        : [{ id: "loading", label: "Loading people", disabled: true, action: () => undefined }];

  return (
    <Menu
      actions={actions}
      onOpenChange={(open) => {
        if (open && people.state === "idle") load();
        onOpenChange?.(open);
      }}
    >
      <MenuTrigger render={trigger} />
      <MenuContent align="end" filterLabel="Filter people" emptyLabel="No matching people." />
    </Menu>
  );
}
