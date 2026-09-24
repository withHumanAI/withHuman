import { createFileRoute } from "@tanstack/react-router";
import { useAccess } from "../app/authorization";
import { loadChannelsPage } from "../app/contact-channels";
import { OnboardingScreen } from "../app/onboarding";

/* The guided setup needs the whole chain: connecting agents and issuing
 * their credentials, writing and activating the pipeline and the
 * escalation path, and finally deciding the request the test produces.
 * Mirrors domain.OnboardingPermissions. */
const ONBOARDING_PERMISSIONS = [
  "agent.write",
  "agent.credential.issue",
  "pipeline.write",
  "pipeline.activate",
  "escalation_path.write",
  "escalation_path.activate",
  "request.read",
  "request.decide",
];

export const Route = createFileRoute("/_app/welcome")({
  /* The first step is the person's channels and ladders, loaded here so
   * the step never paints empty; the setup session itself is live state
   * the screen follows over its events stream. */
  loader: loadChannelsPage,
  component: Welcome,
});

function Welcome() {
  const { can } = useAccess();
  const page = Route.useLoaderData();
  const canOnboard = ONBOARDING_PERMISSIONS.every((permission) => can(permission));
  return <OnboardingScreen canOnboard={canOnboard} page={page} />;
}
