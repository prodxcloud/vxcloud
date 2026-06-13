/**
 * Robotic — the Robotic control cloud.
 *
 * Register robots, send commands, push telemetry, request motion plans,
 * and issue fleet-wide commands.
 *
 * Endpoints (all on the per-tenant node):
 *   GET    /api/v2/robotic/info
 *   GET    /api/v2/robotic/robots
 *   POST   /api/v2/robotic/robots
 *   GET    /api/v2/robotic/robots/{id}
 *   DELETE /api/v2/robotic/robots/{id}
 *   POST   /api/v2/robotic/robots/{id}/command
 *   GET    /api/v2/robotic/commands/{id}
 *   POST   /api/v2/robotic/robots/{id}/emergency-stop
 *   POST   /api/v2/robotic/robots/{id}/telemetry
 *   POST   /api/v2/robotic/robots/{id}/plan
 *   POST   /api/v2/robotic/robots/{id}/schedule
 *   POST   /api/v2/robotic/robots/{id}/approval/resolve
 *   GET    /api/v2/robotic/templates
 *   GET    /api/v2/robotic/robots/{id}/state
 *   POST   /api/v2/robotic/robots/{id}/simulate
 *   POST   /api/v2/robotic/robots/{id}/kinematics
 *   POST   /api/v2/robotic/fleet/command
 *   POST   /api/v2/robotic/fleet/mission
 */

import type { Transport } from './transport.js';

/** A decoded JSON object response. */
export type Result = Record<string, unknown>;

export class Robotic {
  constructor(private t: Transport) {}

  /** Service info and capabilities. */
  async info(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/robotic/info')).body ?? {};
  }

  /** List all registered robots. */
  async listRobots(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/robotic/robots')).body ?? {};
  }

  /** Detail record for one robot. */
  async getRobot(robotId: string): Promise<Result> {
    if (!robotId) throw new Error('robotic.getRobot: robotId is required');
    return (await this.t.get<Result>(`/api/v2/robotic/robots/${robotId}`)).body ?? {};
  }

  /** Register a new robot from an arbitrary spec. */
  async registerRobot(spec: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/robotic/robots', spec)).body ?? {};
  }

  /** Deregister a robot. */
  async deleteRobot(robotId: string): Promise<Result> {
    if (!robotId) throw new Error('robotic.deleteRobot: robotId is required');
    return (await this.t.delete<Result>(`/api/v2/robotic/robots/${robotId}`)).body ?? {};
  }

  /** Dispatch a command to a robot. Poll the returned id with commandStatus. */
  async sendCommand(robotId: string, payload: Record<string, unknown>): Promise<Result> {
    if (!robotId) throw new Error('robotic.sendCommand: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/command`, payload)).body ?? {};
  }

  /** Status of a previously-sent command. */
  async commandStatus(commandId: string): Promise<Result> {
    if (!commandId) throw new Error('robotic.commandStatus: commandId is required');
    return (await this.t.get<Result>(`/api/v2/robotic/commands/${commandId}`)).body ?? {};
  }

  /** Issue an immediate emergency stop. */
  async emergencyStop(robotId: string): Promise<Result> {
    if (!robotId) throw new Error('robotic.emergencyStop: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/emergency-stop`, {})).body ?? {};
  }

  /** Push a telemetry frame. */
  async telemetry(robotId: string, payload: Record<string, unknown>): Promise<Result> {
    if (!robotId) throw new Error('robotic.telemetry: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/telemetry`, payload)).body ?? {};
  }

  /** Autonomous LLM mission plan (payload: objective, execute, provider, model). */
  async plan(robotId: string, payload: Record<string, unknown>): Promise<Result> {
    if (!robotId) throw new Error('robotic.plan: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/plan`, payload)).body ?? {};
  }

  /** Schedule a recurring mission via vxchrono (payload: objective, schedule_type, cadence_minutes|cron_expr). */
  async schedule(robotId: string, payload: Record<string, unknown>): Promise<Result> {
    if (!robotId) throw new Error('robotic.schedule: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/schedule`, payload)).body ?? {};
  }

  /** Resolve a pending robot-action approval. */
  async resolveApproval(robotId: string, payload: Record<string, unknown>): Promise<Result> {
    if (!robotId) throw new Error('robotic.resolveApproval: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/approval/resolve`, payload)).body ?? {};
  }

  /** List the built-in robot archetypes (arm, mobile, humanoid, drone, computer)
   *  with their kinematic params + default capabilities — use one as a blueprint
   *  when registering a robot. */
  async templates(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/robotic/templates')).body ?? {};
  }

  /** Live offline-physics state of a robot — pose, joints, battery, balance —
   *  for virtual robots, or device telemetry for real ones. */
  async state(robotId: string): Promise<Result> {
    if (!robotId) throw new Error('robotic.state: robotId is required');
    return (await this.t.get<Result>(`/api/v2/robotic/robots/${robotId}/state`)).body ?? {};
  }

  /** DRY-RUN a motion through the physics engine and return the predicted
   *  trajectory. Changes no state and dispatches no command.
   *  payload: {action, parameters, samples, type}. */
  async simulate(robotId: string, payload: Record<string, unknown>): Promise<Result> {
    if (!robotId) throw new Error('robotic.simulate: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/simulate`, payload)).body ?? {};
  }

  /** Forward (op=fk, needs joints) or inverse (op=ik, needs x,y) kinematics for a
   *  2-link planar arm. payload: {op, joints|x,y, link1_m, link2_m, elbow_up}. */
  async kinematics(robotId: string, payload: Record<string, unknown>): Promise<Result> {
    if (!robotId) throw new Error('robotic.kinematics: robotId is required');
    return (await this.t.postJSON<Result>(`/api/v2/robotic/robots/${robotId}/kinematics`, payload)).body ?? {};
  }

  /** Issue a command to every robot in the fleet. */
  async fleetCommand(payload: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/robotic/fleet/command', payload)).body ?? {};
  }

  /** Multi-robot mission via the workflow engine + per-robot LLM plan
   *  (payload: objective, robot_ids|robot_type|tags). */
  async fleetMission(payload: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/robotic/fleet/mission', payload)).body ?? {};
  }
}
