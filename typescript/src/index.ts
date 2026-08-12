/**
 * @vxcloud/sdk — TypeScript client for the vxcloud / VxCloud platform.
 *
 *     import { VxCloud } from '@vxcloud/sdk';
 *
 *     const c = await VxCloud.loadFromVxcli();
 *     const sess = await c.deploy.container({
 *       image: 'grafana/grafana:latest', name: 'grafana',
 *       host: '13.216.243.13', sshUser: 'ubuntu',
 *       keyPairName: 'AWSPRODKEY1.PEM',
 *       ports: ['3000:3000'],
 *     });
 *     console.log(sess.sessionId);
 *
 * See https://vxcloud.io/docs/sdks for the full reference and
 * https://github.com/prodxcloud/vxcloud/blob/main/services/sdk/BIG_PLAN.md
 * for what's coming next.
 */

export { VxCloud, VERSION, type VxCloudOptions, AuthFacade } from './client.js';
// Brand aliases — additive, all resolve to the same VxCloud class. Lets users
// mirror the Python SDK's `vxsdk.vxcloud` / `vxsdk.Client` shapes from TS.
//   import { vxcloud } from '@vxcloud/sdk'; const c = await vxcloud.loadFromVxcli();
//   import { Vxsdk } from '@vxcloud/sdk';   const c = await Vxsdk.loadFromVxcli();
export { VxCloud as vxcloud, VxCloud as Vxsdk, VxCloud as Client } from './client.js';
export {
  VxError, VxAuthError, VxValidationError, VxNotFoundError,
  VxRateLimitError, VxServerError, VxNetworkError,
  fromHTTP, isRetryable, type VxErrorPayload,
} from './errors.js';
export type {
  SSHTarget, DeploySession, ContainerSummary, ContainerStatus,
  ActionResponse, Pipeline, Build, NodeInfo, MarketplaceItem,
  StackKind, HostAction,
} from './types.js';

// Re-export the resource classes so consumers can construct them
// independently in tests, or extend them.
//
// Transport comes with them: every resource constructor takes one, so without
// it exported the classes below could be imported but never named in a typed
// signature — and there was no escape hatch for an endpoint the SDK does not
// wrap yet. It is also the seam to stub in tests (`new Leads(fakeTransport)`).
export {
  Transport,
  type TransportOptions, type CallOptions, type JSONResponse, type MultipartFile,
} from './transport.js';
export { Services, ServicesVM } from './services.js';
export { Sessions } from './sessions.js';
export { Deploy } from './deploy.js';
export { Install } from './install.js';
export { CICD, Pipelines, Builds, GitProviders } from './cicd.js';
export { Marketplace, MarketplaceList } from './marketplace.js';
export { Nodes } from './nodes.js';
export { Cloud, VM, S3, IAM, Database, Kubernetes, Network, Serverless, type VMProvisionInput } from './cloud.js';
export { MetalDB } from './metaldb.js';
export {
  AgentControl, LongRunningJob,
  FineTuning, Training, Knowledge, Datasets,
  AgentControlAgents, AgentControlGitHub,
} from './agentcontrol.js';
export { Networks, type ScriptCatalogEntry, type RunRemoteInput } from './networks.js';
export {
  Agents, type AgentKind, type AgentRunInput, type AgentRunOutput,
} from './agents.js';
export {
  Chat, type ChatProvider, type ChatMessage, type ChatSendInput, type ChatSendOutput,
} from './chat.js';
export {
  Observability, Backups, Migrations, SyncSub,
  type Backup, type CreateBackupInput, type RestoreBackupInput,
  type MigrationPlan, type MigrationPlanInput,
} from './observability.js';
export {
  Billing,
  type MulticloudReport, type OptimizationRecommendation, type OptimizationReport,
} from './billing.js';
export {
  SalesShift,
  type SendEmailInput, type SendEmailResult, type TrackedEmail, type WorkerHealth,
} from './salesshift.js';
// SalesShift platform surfaces — what the workspace pays for (billing) and
// what it publishes (social), plus the signal pool, tasks and campaigns.
export {
  SalesShiftBilling, SalesShiftSocial, SalesShiftOpportunities,
  SalesShiftTasks, SalesShiftCampaigns,
  type Plan, type PlanQuotas, type Subscription, type Invoice,
  type SocialChannel, type SocialDelivery, type SocialPost, type DistributeJob,
  type Opportunity, type Task, type CampaignRecipient,
} from './salesshift-platform.js';
// SalesShift CRM — contacts (mailable), workflows and sequences.
export {
  SalesShiftContacts, SalesShiftWorkflows, SalesShiftSequences,
  type Contact, type Pagination, type Activity, type ContactList, type SendResult,
  // Renamed on the way out: `Workflow` is already taken by the VxCloud
  // workflow engine (./workflow.js), which is a different product entirely.
  type Workflow as SalesShiftWorkflow,
  type WorkflowGraph, type WorkflowRun, type RunStep, type GraphIssue,
  type Sequence, type SequenceStep, type SequenceTotals, type SkipDetail, type Enrollment,
} from './salesshift-crm.js';
// Leads — the prospect pool. Separate from SalesShift on purpose: a lead is not
// mailable until it is converted into a Contact.
export {
  Leads,
  // helpers
  describeConvert, estimateRevealCost, revealedEmail,
  // errors worth catching by name
  VxLeadQuotaExceededError, VxLeadErasedError,
  // limits the server enforces
  LEADS_MAX_BATCH, LEADS_MAX_PAGE_SIZE, LEADS_MAX_LIST_LIMIT, LEADS_TOTAL_CAP,
  // filters + search
  type LeadFilters, type LeadSearchInput, type LeadResultType,
  type LeadSort, type LeadSortField, type LeadPersonSortField, type LeadCompanySortField,
  type LeadSeniority, type LeadDepartment, type LeadEmailStatus, type LeadEmployeeRange,
  type LeadSearchPage, type LeadSearchResult,
  type LeadPersonSearchPage, type LeadCompanySearchPage,
  type SearchAllLeadsOptions,
  // pool rows
  type PoolPerson, type PoolPersonCompany, type PoolPersonDetail,
  type PoolCompanyResult, type PoolCompanyBrief, type PoolCompanyDetail, type PoolCompanyPerson,
  type LeadFacets, type LeadFacetBucket,
  // saved leads
  type SavedLead, type SavedLeadCompany, type SavedLeadDetail,
  type LeadPoolSnapshot, type UpdateLeadInput,
  // quota, reveal, convert, erasure
  type RevealQuota, type RevealResult, type RevealCostEstimate,
  type SaveLeadsResult, type ConvertLeadResult,
  type ConvertFromPoolReport, type BulkConvertReport,
  type ErasureInput, type ErasureResult,
  // enrichment
  type EnrichInput, type EnrichResult,
  // saved searches
  type SavedSearch, type SavedSearchRef, type SaveSearchInput,
} from './leads.js';
export {
  Workspace,
  type WorkspaceResult, type APIToken, type AWSCredentialsInput,
  type AzureCredentialsInput, type GCPCredentialsInput,
  type AICredentialsInput, type AIProvider,
} from './workspace.js';
export {
  VxComputer,
  type VxComputerRunInput, type VxComputerApprovalInput,
} from './vxcomputer.js';
export { Robotic } from './robotic.js';
export {
  Connector,
  type ConnectorVMInput, type ConnectorDeployVMInput, type ConnectorBucketInput,
  type ConnectorStaticSiteInput, type ConnectorCloudRunInput,
  type ConnectorSubdomainInput, type ConnectorLBInput,
} from './connector.js';
export {
  WebScraper, type ScrapeInput, type SearchInput, type DeepResearchInput,
} from './webscraper.js';
export {
  AgentCLI, resolveAgent, type Agent,
  type AgentInstallOpts, type AgentConfigureOpts,
} from './agentcli.js';
export { VxChrono } from './vxchrono.js';
export { Workflow } from './workflow.js';
