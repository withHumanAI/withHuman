import type { ComponentProps, ComponentType, ReactNode } from "react";
import type { LinkProps } from "@tanstack/react-router";
import type { BlockMenuHandlers, BlockPosition } from "@/ds/components/block-card";
import type { PipelineExit } from "@/ds/components/policy-block";
import type { ReviewerTarget } from "@/ds/components/reviewer-target";
import type { AgentDetail, ApprovalRequest, ApprovalReview, ContactMethod } from "../types";
import type { AgentSearch } from "./agent-search";
import { useAccess } from "./authorization";
import { NavItem } from "./nav-item";
import type { OrganizationPolicyDraft } from "./organization";
import type { TestStep } from "./pipeline-replay";
import { type RuntimeConfig, useRuntimeConfig } from "./use-runtime-config";

/* Extension points an edition's frontend fills before the router
 * renders. Core's screens never link to a route only an edition ships:
 * what the hosted edition adds (its sidebar entries, palette commands,
 * permission labels, the links into its audit log, the tabs on an
 * agent's page, the tool sources its catalogue knows, the block kinds
 * its pipelines can run, the contact kinds it delivers to, the rows on
 * the organization policies page, the enterprise SSO door on the
 * sign-in screens) arrives through this registry, so the open bundle carries no dead hosted
 * surface and the hosted overlay registers exactly once, in its
 * main.tsx, before createRouter. */

export interface NavEntry {
  id: string;
  /* The route path. The edition's route tree declares it; core's does
   * not, which is why it is a string here and cast at one render site. */
  to: string;
  label: string;
  icon: ReactNode;
  /* Where the entry sits in the workspace sidebar: with the records after
   * the review queue, under Agents, or under Policy. */
  group: "records" | "agents" | "policy";
  /* The permission the person must hold somewhere for the entry to show. */
  permission?: string;
  /* Whether this deployment has the feature, read from the edition's own
   * runtime-config entries; absent means always. */
  enabled?: (config: RuntimeConfig) => boolean;
  /* The command palette label, when the entry also joins the palette. */
  command?: string;
}

/* A settings page the edition adds to the sidebar's settings mode. */
export interface SettingsEntry {
  id: string;
  to: string;
  label: string;
  group: "you" | "organization";
  permission?: string;
  enabled?: (config: RuntimeConfig) => boolean;
  command?: string;
}

export interface AuditLinkTarget {
  actor?: string;
  subject?: { type: string; id: string };
  q?: string;
}

export interface AuditLink {
  to: string;
  search: Record<string, string>;
  label: string;
  permission: string;
}

/* An audit link resolved against the reader: null when the edition has
 * no audit log, allowed false when it has one the person may not read. */
export type ResolvedAuditLink = (AuditLink & { allowed: boolean }) | null;

/* The request page's slots for what an edition can tell from its audit
 * log: the automated assessments shown before the decision, the fact
 * row saying why a human is needed, and the history in place of core's
 * own, which knows only the routing, the reviewers, and the decision. */
export interface RequestDetailSlots {
  assessments: ComponentType<{ request: ApprovalRequest }> | null;
  cause: ComponentType<{ request: ApprovalRequest }> | null;
  history: ComponentType<{ request: ApprovalRequest; review: ApprovalReview | null; now: number }> | null;
}

/* The fields every pipeline block draft has, whatever its kind. A kind's
 * own draft type adds its fields beside them. */
export interface BlockDraftBase {
  id: string;
  name: string;
  enabled: boolean;
  /* The sentence the agent receives when this block decides. */
  reason: string;
  /* Who a human exit asks. null or absent inherits the pipeline default. */
  target?: ReviewerTarget | null;
}

/* A draft of a kind an edition registered: kind is what core's screens
 * switch on, type names the kind in the registry, and the rest of the
 * draft is the kind's own. */
export interface EditionBlockDraft extends BlockDraftBase {
  kind: "edition";
  type: string;
}

/* The wire shape of a block whose kind an edition adds: core's envelope
 * around a config only the kind reads. reason and escalation_path sit
 * where every kind keeps them, so core can show them for a kind this
 * bundle does not know. */
export interface EditionBlockDefinition {
  id: string;
  name: string;
  type: string;
  enabled: true;
  timeout: string;
  max_attempts: 1 | 2 | 3;
  config: Record<string, unknown>;
}

/* What the rail hands a kind's editing card: the shared block anatomy
 * (position, expansion, the grip, the block menu) and the reviewer
 * directory for its human exits. */
export interface BlockEditorProps<Draft> extends BlockMenuHandlers {
  block: Draft;
  position: BlockPosition;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onChange: (next: Draft) => void;
  handleProps?: ComponentProps<"div">;
  dragging?: boolean;
  /* Teams and people a human exit may name. Without it the card offers
   * no reviewer choice and every human exit uses the default. */
  directory?: ReviewerTarget[];
  /* The resolved reviewer destination, included in the field help. */
  describeRoute?: (target: ReviewerTarget | null) => string;
}

/* What the rail hands a kind's read-only face. */
export interface BlockSummaryProps<Draft> {
  block: Draft;
  position: BlockPosition;
  expanded?: boolean;
  /* Present when the card opens to its details. */
  onExpandedChange?: (expanded: boolean) => void;
  describeRoute?: (target: ReviewerTarget | null) => string;
}

/* One section of the styleguide: its anchor, its nav label, its demos. */
export interface StyleguideSection {
  id: string;
  label: string;
  Component: ComponentType;
}

/* A block kind an edition adds to the approval pipeline. Core's screens
 * draw the kind through this contract alone: the add menu, the rail's
 * cards and exits, the JSON document, validation, the test replay, the
 * reference cards and the styleguide. Drafts are the kind's own type
 * over EditionBlockDraft; definitions are its own over
 * EditionBlockDefinition. */
export interface BlockKind<
  Draft extends EditionBlockDraft = EditionBlockDraft,
  Definition extends EditionBlockDefinition = EditionBlockDefinition,
> {
  /* The block type on the wire and in the registry. */
  type: string;
  /* The add menu's choice: its label, the sentence behind its info mark,
   * and its icon. */
  label: string;
  description: string;
  icon: ReactNode;
  /* The kind as a noun, the way core says "Webhook", for the reference
   * cards' Kind fact and the rail drawn inside a pipeline's hover card. */
  kindLabel: string;
  /* Whether this deployment can run the kind, read from the edition's
   * own runtime-config entries, and the sentence the disabled add-menu
   * choice carries when it cannot. */
  available: (config: RuntimeConfig) => boolean;
  unavailable: string;
  /* A new block of the kind, with the key the editor minted for it. */
  draft: (id: string) => Draft;
  /* Between the wire definition and the editor's draft. target is the
   * reviewer core resolved from config.escalation_path; escalationPath
   * is the key core read from the draft's target, absent for the
   * pipeline default. */
  fromDefinition: (definition: Definition, target: ReviewerTarget | null) => Draft;
  toDefinition: (draft: Draft, escalationPath: string | undefined) => Definition;
  /* The first problem that would make the server reject the draft,
   * phrased for a toast and naming the block by label; null when it can
   * ship. The name and reason every block needs are checked by core. */
  validate: (draft: Draft, label: string) => string | null;
  /* The first problem with a JSON-mode block of the kind, named by its
   * label ("Block 3.2"); null when its config has the kind's shape. */
  checkDocument: (block: { config?: Record<string, unknown> }, label: string) => string | null;
  /* The one-line summary under the block's name, in machine mono. */
  summarize: (draft: Draft) => string;
  /* The exits drawn under the card, and the label of the rail running
   * on for what does not exit. */
  exits: (draft: Draft) => { exits: PipelineExit[]; continueLabel: string };
  /* The exit key a replayed test step that answered took, read from the
   * step's metadata, which is the block's own. Core already handles a
   * step that continued or failed. */
  takenExit: (step: TestStep) => string | undefined;
  /* The editing card and the read-only face, sharing every other kind's
   * anatomy (BlockCard, BlockSummaryCard). */
  editor: ComponentType<BlockEditorProps<Draft>>;
  summaryCard: ComponentType<BlockSummaryProps<Draft>>;
  /* What a replayed step of the kind shows under its card, the way a
   * webhook shows its delivery; null when the kind leaves no evidence. */
  evidence: ComponentType<{ step: TestStep; block: Draft }> | null;
  /* Readable labels for the codes only this kind reports: why a
   * revision naming it cannot be activated (PipelineReferences.blocks),
   * and the block error codes its evaluations can fail with. */
  problemLabels: Record<string, string>;
  errorLabels: Record<string, string>;
  /* The styleguide section documenting the kind's components, so the
   * bundle that carries them documents them; null for none. */
  styleguide: StyleguideSection | null;
}

/* A kind as the registry holds it, its draft and definition types
 * erased: core's screens work with the base shapes. */
export type RegisteredBlockKind = BlockKind;

/* Types a kind fully, then erases it for the registry. The cast is the
 * one place a kind's own draft meets core's base draft. */
export function defineBlockKind<Draft extends EditionBlockDraft, Definition extends EditionBlockDefinition>(
  kind: BlockKind<Draft, Definition>,
): RegisteredBlockKind {
  return kind as unknown as RegisteredBlockKind;
}

/* What an edition's agent tab renders with: the agent, what the tab's
 * own loader delivered, and the page's search, whose q is the tab's to
 * filter by and to change through the agent route. */
export interface AgentTabProps<Data = unknown> {
  agent: AgentDetail;
  data: Data;
  search: AgentSearch;
}

/* A tab an edition adds to the single-agent page, beside core's own.
 * The agent route loads the tab's data before the page paints, the way
 * it loads core's tabs, so the tab never fetches on mount. */
export interface AgentTab<Data = unknown> {
  /* The tab's value in the page's tab search parameter. */
  id: string;
  label: string;
  /* The permission the person must hold somewhere for the tab to show. */
  permission: string;
  load: (agentSlug: string) => Promise<Data>;
  component: ComponentType<AgentTabProps<Data>>;
}

/* A tab as the registry holds it, its data type erased. */
export type RegisteredAgentTab = AgentTab;

/* Types a tab fully, then erases it for the registry: the one place a
 * tab's own data meets the page's unknown. */
export function defineAgentTab<Data>(tab: AgentTab<Data>): RegisteredAgentTab {
  return tab as unknown as RegisteredAgentTab;
}

/* A tool source an edition adds to the catalogue beside the runtime's
 * own (builtin, mcp): the name the wire carries, the label the
 * catalogue shows for it, and whether the source is external, meaning
 * its tools are discovered centrally and granted to an agent rather
 * than reported by one of its instances. */
export interface ToolSourceEntry {
  source: string;
  label: string;
  external: boolean;
}

/* What an edition can tell about a tool mention's server: the server's
 * display name in place of the alias the mention carries. */
export interface ToolReferenceFacts {
  serverName: string;
}

export interface ToolMention {
  tool: string;
  server?: string;
}

/* What a contact kind's add flow renders with: the person's channels,
 * so the flow can say the kind is already there; where to go once the
 * server holds the new method (the settings page returns to the list,
 * whose new row carries whatever proves it; the guided setup folds the
 * flow away and reloads); and how to leave without adding. */
export interface ContactAddProps {
  methods: ContactMethod[];
  onAdded: (created: ContactMethod) => void | Promise<void>;
  onDone: () => void;
}

/* What the panel under an unconfirmed method's row renders with: the
 * method, whether its row is mid-action, a runner for the calls the
 * panel makes on the method's behalf (the row shows busy meanwhile and
 * reports a refusal under the failure title; the answer, or null when
 * it failed), and a reload of the list, for a method the server
 * confirms on its own. */
export interface ContactConfirmProps {
  method: ContactMethod;
  busy: boolean;
  act: <T>(action: () => Promise<T>, failure: string) => Promise<T | null>;
  onRefresh: () => Promise<void>;
}

/* A method's state badge; without an icon the tone's status glyph. */
export interface ContactMethodBadge {
  tone: "approve" | "warn" | "neutral";
  label: string;
  icon?: ReactNode;
}

/* How the channel list draws a method of a kind: the row's title and
 * the line under it (a typed-in value in mono with the kind's name
 * under it; a registered device by name with its identifier under),
 * its state badge, the accessible name of its remove action (null when
 * the list cannot remove one) and what a failed removal is called, and
 * the panel an unconfirmed method's row unfolds to prove it (null for a
 * kind the server vouches for). */
export interface ContactMethodRow {
  title: (method: ContactMethod) => ReactNode;
  subtitle: (method: ContactMethod) => ReactNode;
  badge: (method: ContactMethod) => ContactMethodBadge;
  removeLabel: ((method: ContactMethod) => string) | null;
  removeFailure: string;
  confirm: ComponentType<ContactConfirmProps> | null;
}

/* A contact kind an edition delivers to, beside core's email, Slack
 * and Discord. Core's screens draw the kind through this contract
 * alone: the add-channel menu and the guided setup's picker, the add
 * flow, the channel list, and the ladders' method choices. */
export interface ContactKind {
  /* The kind on the wire. */
  kind: string;
  /* The kind's name, as the menu, the picker and the rows show it. */
  label: string;
  icon: ReactNode;
  /* The menu's sentence about the kind. */
  description: string;
  /* Whether this deployment delivers to the kind, read from the
   * edition's own runtime-config entries, and the sentence the disabled
   * menu row carries when it does not. */
  available: (config: RuntimeConfig) => boolean;
  unavailable: string;
  /* The menu's button: what adding the kind is ("Add number",
   * "Install app"). */
  action: string;
  /* The whole add flow: its heading (the settings page's title and
   * description, the guided setup's panel header), the section title
   * over the flow on the settings page, and the flow itself: the input,
   * any agreement it needs, submit, or how the channel arrives by
   * itself. Core supplies the calls and the callbacks; the row that
   * proves the new method afterwards is the kind's row.confirm. */
  add: {
    title: string;
    description: string;
    section?: string;
    component: ComponentType<ContactAddProps>;
  };
  row: ContactMethodRow;
  /* The method's name among a ladder's choices, the way core says
   * "Email, you@example.com". */
  ladderLabel: (method: ContactMethod) => string;
}

/* Types a kind at its definition site, so its components are checked
 * against the props core hands them where they are written. */
export function defineContactKind(kind: ContactKind): ContactKind {
  return kind;
}

/* What a row an edition adds to the organization policies page renders
 * with: the policy as the page's form holds it, the change to make to
 * it (batched behind the page's save bar with core's own edits), and
 * whether the person may change organization settings. */
export interface OrganizationPolicyRowProps {
  draft: OrganizationPolicyDraft;
  update: (change: Partial<OrganizationPolicyDraft>) => void;
  canEdit: boolean;
}

/* The enterprise single sign-on door an edition serves beside the
 * password: where the browser goes to sign in to an organization
 * through its identity provider, and what the callback's sso_error
 * codes mean when it sends the browser back (sso_failed is the
 * fallback for a code the table does not name). Core's login
 * vocabulary (sso_organizations, sso_required) is the same in both
 * editions, but the routes behind it are the hosted edition's, so
 * without a door the access and invitation screens offer no SSO
 * button and read no sso_error. */
export interface SSODoor {
  beginURL: (organizationSlug: string) => string;
  errors: Record<string, string>;
}

/* The sign-in methods an edition serves beyond core's own password:
 * enterprise SSO, or nothing. (Social providers are core's own
 * registry, reported by runtime-config.) */
export interface LoginMethodDoors {
  sso: SSODoor | null;
}

export interface Extensions {
  nav: NavEntry[];
  settingsNav: SettingsEntry[];
  /* Readable names for edition-owned permission keys. */
  permissionLabels: Record<string, string>;
  /* A link into the edition's audit log for an actor, a subject or a
   * search, or null in an edition without one. */
  auditLink: ((target: AuditLinkTarget) => AuditLink) | null;
  /* The edition's self-serve organization door, offered by the sign-in
   * screen when the deployment has it; the open edition's only door is
   * the first-owner ceremony. */
  signup: { href: string; enabled: (config: RuntimeConfig) => boolean } | null;
  /* Rendered above every workspace page, for notices such as the hosted
   * edition's over-quota warning. Each decides for itself whether to show. */
  shellBanners: ComponentType[];
  /* Rendered under a refused invitation when the organization is at its
   * seat quota, for the edition's way of adding seats. */
  invitationQuotaHint: ComponentType | null;
  requestDetail: RequestDetailSlots;
  /* The block kinds the edition's pipelines can run beside core's own
   * (always, conditions, webhook, branch). */
  blockKinds: RegisteredBlockKind[];
  /* The tabs the edition adds to the single-agent page, after core's. */
  agentTabs: RegisteredAgentTab[];
  /* The tool sources the edition's catalogue reports beside the
   * runtime's own. A source no bundle registered shows by its raw name. */
  toolSources: ToolSourceEntry[];
  /* What the edition knows about a tool mention's server, for the tool
   * hover card. Runs as a hook inside the card, so it may read shared
   * reads and the reader's access; the open edition knows nothing. */
  toolReference: (mention: ToolMention) => ToolReferenceFacts | null;
  /* The contact kinds the edition delivers to beside core's own (email,
   * slack, discord), in the order its menus list them. A method of a
   * kind no bundle registered still lists, by its raw value. */
  contactKinds: ContactKind[];
  /* The rows the edition adds to the organization policies page's
   * Programmatic access section, after core's own: the switch for a
   * knob core serves (organization_api_keys_allowed) whose feature the
   * edition ships. */
  organizationPolicyRows: ComponentType<OrganizationPolicyRowProps>[];
  /* The sign-in doors the edition serves beside the password: the
   * enterprise SSO door, whose routes the open edition does not mount. */
  loginMethods: LoginMethodDoors;
}

export const extensions: Extensions = {
  nav: [],
  settingsNav: [],
  permissionLabels: {},
  auditLink: null,
  signup: null,
  shellBanners: [],
  invitationQuotaHint: null,
  requestDetail: { assessments: null, cause: null, history: null },
  blockKinds: [],
  agentTabs: [],
  toolSources: [],
  toolReference: () => null,
  contactKinds: [],
  organizationPolicyRows: [],
  loginMethods: { sso: null },
};

export function registerExtensions(partial: Partial<Extensions>): void {
  if (partial.nav) extensions.nav.push(...partial.nav);
  if (partial.settingsNav) extensions.settingsNav.push(...partial.settingsNav);
  if (partial.permissionLabels) Object.assign(extensions.permissionLabels, partial.permissionLabels);
  if (partial.auditLink) extensions.auditLink = partial.auditLink;
  if (partial.signup) extensions.signup = partial.signup;
  if (partial.shellBanners) extensions.shellBanners.push(...partial.shellBanners);
  if (partial.invitationQuotaHint) extensions.invitationQuotaHint = partial.invitationQuotaHint;
  if (partial.requestDetail) Object.assign(extensions.requestDetail, partial.requestDetail);
  if (partial.blockKinds) extensions.blockKinds.push(...partial.blockKinds);
  if (partial.agentTabs) extensions.agentTabs.push(...partial.agentTabs);
  if (partial.toolSources) extensions.toolSources.push(...partial.toolSources);
  if (partial.toolReference) extensions.toolReference = partial.toolReference;
  if (partial.contactKinds) extensions.contactKinds.push(...partial.contactKinds);
  if (partial.organizationPolicyRows) extensions.organizationPolicyRows.push(...partial.organizationPolicyRows);
  if (partial.loginMethods) Object.assign(extensions.loginMethods, partial.loginMethods);
}

/* The enterprise SSO door, or null in an edition that serves none.
 * Registered before the router exists, so the sign-in screens read it
 * at render time without subscribing. */
export function ssoDoor(): SSODoor | null {
  return extensions.loginMethods.sso;
}

/* The registered kind for a contact method kind, or undefined for
 * core's own kinds and for a kind this bundle does not know. */
export function contactKind(kind: string): ContactKind | undefined {
  return extensions.contactKinds.find((entry) => entry.kind === kind);
}

/* The registered agent tab for a tab search value, or undefined for
 * core's own tabs and for a value no bundle registered. */
export function agentTab(id: string | undefined): RegisteredAgentTab | undefined {
  return id === undefined ? undefined : extensions.agentTabs.find((tab) => tab.id === id);
}

/* The edition's agent tabs the person may see. */
export function useAgentTabs(): RegisteredAgentTab[] {
  const { holdsAnywhere } = useAccess();
  return extensions.agentTabs.filter((tab) => holdsAnywhere(tab.permission));
}

/* The registered kind for a block type, or undefined for core's own
 * kinds and for a type this bundle does not know. */
export function blockKind(type: string): RegisteredBlockKind | undefined {
  return extensions.blockKinds.find((kind) => kind.type === type);
}

/* The kind an edition draft was made by. Such a draft exists only in
 * the bundle that registered its kind, so a miss is a programming error. */
export function requireBlockKind(type: string): RegisteredBlockKind {
  const kind = blockKind(type);
  if (!kind) throw new Error(`No block kind "${type}" is registered.`);
  return kind;
}

/* Whether an entry applies to this person on this deployment. */
function useEntryFilter(): (entry: { permission?: string; enabled?: (config: RuntimeConfig) => boolean }) => boolean {
  const { holdsAnywhere } = useAccess();
  const { config } = useRuntimeConfig();
  return (entry) =>
    (!entry.permission || holdsAnywhere(entry.permission)) &&
    (!entry.enabled || (config !== null && entry.enabled(config)));
}

/* The router's link props for an edition route. The cast is the one
 * place core admits a path its own route tree does not declare. */
export function extensionLink(link: { to: string; search?: Record<string, string> }): Pick<LinkProps, "to" | "search"> {
  return { to: link.to, search: link.search } as unknown as Pick<LinkProps, "to" | "search">;
}

/* The link props for an audit link the person may follow, else nothing. */
export function auditLinkProps(link: ResolvedAuditLink): Pick<LinkProps, "to" | "search"> | undefined {
  return link && link.allowed ? extensionLink(link) : undefined;
}

/* The edition's sidebar entries in a group that the person may see. */
export function useExtensionNav(group: NavEntry["group"]): NavEntry[] {
  const applies = useEntryFilter();
  return extensions.nav.filter((entry) => entry.group === group && applies(entry));
}

/* The edition's settings pages in a group that the person may see. */
export function useExtensionSettingsNav(group: SettingsEntry["group"]): SettingsEntry[] {
  const applies = useEntryFilter();
  return extensions.settingsNav.filter((entry) => entry.group === group && applies(entry));
}

/* Every edition entry with a palette label that the person may reach. */
export function useExtensionCommands(): Array<NavEntry | SettingsEntry> {
  const applies = useEntryFilter();
  return [...extensions.nav, ...extensions.settingsNav].filter((entry) => entry.command && applies(entry));
}

/* The self-serve door, when the edition has one and this deployment
 * switched it on. */
export function useSignupDoor(config: RuntimeConfig | null): { href: string } | null {
  if (!extensions.signup || config === null || !extensions.signup.enabled(config)) return null;
  return { href: extensions.signup.href };
}

export function ExtensionSettingsNav({ entries }: { entries: SettingsEntry[] }) {
  return (
    <>
      {entries.map((entry) => (
        <NavItem key={entry.id} to={extensionLink(entry).to}>
          {entry.label}
        </NavItem>
      ))}
    </>
  );
}

export function ExtensionBanners() {
  return (
    <>
      {extensions.shellBanners.map((Banner, index) => (
        <Banner key={index} />
      ))}
    </>
  );
}

/* The edition's rows in the organization policies page's Programmatic
 * access section, each over the same draft as core's own rows. */
export function ExtensionOrganizationPolicyRows(props: OrganizationPolicyRowProps) {
  return (
    <>
      {extensions.organizationPolicyRows.map((Row, index) => (
        <Row key={index} {...props} />
      ))}
    </>
  );
}

export function ExtensionNav({ entries }: { entries: NavEntry[] }) {
  return (
    <>
      {entries.map((entry) => (
        <NavItem key={entry.id} to={extensionLink(entry).to} icon={entry.icon}>
          {entry.label}
        </NavItem>
      ))}
    </>
  );
}

/* Links into the audit log for the screens that offer one beside a
 * record: the row action stays visible but disabled when the person
 * may not read the log, and disappears in an edition without one. */
export function useAuditLinks(): (target: AuditLinkTarget) => ResolvedAuditLink {
  const { holdsAnywhere } = useAccess();
  return (target) => {
    const link = extensions.auditLink?.(target) ?? null;
    return link ? { ...link, allowed: holdsAnywhere(link.permission) } : null;
  };
}
