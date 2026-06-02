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
export { VxChrono } from './vxchrono.js';
export { Workflow } from './workflow.js';
