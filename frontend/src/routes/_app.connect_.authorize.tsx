import { createFileRoute } from "@tanstack/react-router";
import { api, RequestError } from "../api";
import {
  AuthorizeEnrollmentScreen,
  type EnrollmentPage,
} from "../app/authorize-enrollment";

export interface EnrollmentSearch {
  user_code?: string;
  q?: string;
  offset?: number;
}

export const Route = createFileRoute("/_app/connect_/authorize")({
  validateSearch: (search: Record<string, unknown>): EnrollmentSearch => ({
    user_code:
      typeof search.user_code === "string"
        ? search.user_code.trim().toUpperCase()
        : undefined,
    q: typeof search.q === "string" ? search.q : undefined,
    offset: Math.max(0, Math.floor(Number(search.offset) || 0)),
  }),
  loaderDeps: ({ search }) => search,
  loader: async ({ deps }): Promise<EnrollmentPage> => {
    if (!deps.user_code) return { preview: null, agents: [], error: "" };
    try {
      const preview = await api.previewAgentEnrollment(deps.user_code);
      const choices =
        preview.status === "pending" && !preview.requested_agent_slug
          ? await api.enrollmentAgents(deps.q ?? "", deps.offset ?? 0)
          : { agents: [] };
      return { preview, ...choices, error: "" };
    } catch (reason) {
      const error =
        reason instanceof RequestError && reason.status === 404
          ? "The enrollment code or agent was not found. Run install again to get a new code."
          : reason instanceof Error
            ? reason.message
            : "Unable to load the enrollment. Try again.";
      return { preview: null, agents: [], error };
    }
  },
  component: AuthorizeEnrollment,
});

function AuthorizeEnrollment() {
  const search = Route.useSearch();
  const page = Route.useLoaderData();
  const navigate = Route.useNavigate();
  return (
    <AuthorizeEnrollmentScreen
      key={search.user_code ?? ""}
      page={page}
      search={search}
      onSearch={(next) => navigate({ search: next })}
    />
  );
}
