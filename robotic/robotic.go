// Package robotic is the resource module for the Robotic control cloud —
// register robots, send commands, push telemetry, request motion plans,
// and issue fleet-wide commands.
//
// Endpoints (all on the per-tenant node):
//
//	GET    /api/v2/robotic/info
//	GET    /api/v2/robotic/robots
//	POST   /api/v2/robotic/robots
//	GET    /api/v2/robotic/robots/{id}
//	DELETE /api/v2/robotic/robots/{id}
//	POST   /api/v2/robotic/robots/{id}/command
//	GET    /api/v2/robotic/commands/{id}
//	POST   /api/v2/robotic/robots/{id}/emergency-stop
//	POST   /api/v2/robotic/robots/{id}/telemetry
//	POST   /api/v2/robotic/robots/{id}/plan
//	POST   /api/v2/robotic/robots/{id}/approval/resolve
//	GET    /api/v2/robotic/robots/{id}/state
//	POST   /api/v2/robotic/robots/{id}/simulate
//	POST   /api/v2/robotic/robots/{id}/kinematics
//	GET    /api/v2/robotic/templates
//	POST   /api/v2/robotic/fleet/command
package robotic

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Result is a decoded JSON object response.
type Result = map[string]interface{}

// Client is the entry point. Construct via c.Robotic().
type Client struct {
	T       *transport.Transport
	NodeURL string
}

func (c *Client) do(ctx context.Context, op, method, path string, body interface{}) (Result, error) {
	var out Result
	u := transport.JoinURL(c.NodeURL, path)
	if err := c.T.JSON(ctx, op, method, u, body, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

// Info returns Robotic service info and capabilities.
func (c *Client) Info(ctx context.Context) (Result, error) {
	return c.do(ctx, "robotic.Info", "GET", "/api/v2/robotic/info", nil)
}

// ListRobots returns all registered robots for the tenant.
func (c *Client) ListRobots(ctx context.Context) (Result, error) {
	return c.do(ctx, "robotic.ListRobots", "GET", "/api/v2/robotic/robots", nil)
}

// GetRobot returns the detail record for one robot.
func (c *Client) GetRobot(ctx context.Context, robotID string) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.GetRobot: robotID is required")
	}
	return c.do(ctx, "robotic.GetRobot", "GET", "/api/v2/robotic/robots/"+robotID, nil)
}

// RegisterRobot registers a new robot from an arbitrary spec.
func (c *Client) RegisterRobot(ctx context.Context, spec map[string]interface{}) (Result, error) {
	return c.do(ctx, "robotic.RegisterRobot", "POST", "/api/v2/robotic/robots", spec)
}

// DeleteRobot deregisters a robot.
func (c *Client) DeleteRobot(ctx context.Context, robotID string) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.DeleteRobot: robotID is required")
	}
	return c.do(ctx, "robotic.DeleteRobot", "DELETE", "/api/v2/robotic/robots/"+robotID, nil)
}

// SendCommand dispatches a command to a robot. The returned Result
// carries the command id — poll it with CommandStatus.
func (c *Client) SendCommand(ctx context.Context, robotID string, payload map[string]interface{}) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.SendCommand: robotID is required")
	}
	return c.do(ctx, "robotic.SendCommand", "POST",
		"/api/v2/robotic/robots/"+robotID+"/command", payload)
}

// CommandStatus returns the status of a previously-sent command.
func (c *Client) CommandStatus(ctx context.Context, commandID string) (Result, error) {
	if commandID == "" {
		return nil, errors.New("robotic.CommandStatus: commandID is required")
	}
	return c.do(ctx, "robotic.CommandStatus", "GET", "/api/v2/robotic/commands/"+commandID, nil)
}

// EmergencyStop issues an immediate emergency stop to a robot.
func (c *Client) EmergencyStop(ctx context.Context, robotID string) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.EmergencyStop: robotID is required")
	}
	return c.do(ctx, "robotic.EmergencyStop", "POST",
		"/api/v2/robotic/robots/"+robotID+"/emergency-stop", map[string]interface{}{})
}

// Telemetry pushes a telemetry frame for a robot.
func (c *Client) Telemetry(ctx context.Context, robotID string, payload map[string]interface{}) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.Telemetry: robotID is required")
	}
	return c.do(ctx, "robotic.Telemetry", "POST",
		"/api/v2/robotic/robots/"+robotID+"/telemetry", payload)
}

// Plan requests a motion/task plan for a robot.
func (c *Client) Plan(ctx context.Context, robotID string, payload map[string]interface{}) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.Plan: robotID is required")
	}
	return c.do(ctx, "robotic.Plan", "POST",
		"/api/v2/robotic/robots/"+robotID+"/plan", payload)
}

// ResolveApproval resolves a pending robot-action approval.
func (c *Client) ResolveApproval(ctx context.Context, robotID string, payload map[string]interface{}) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.ResolveApproval: robotID is required")
	}
	return c.do(ctx, "robotic.ResolveApproval", "POST",
		"/api/v2/robotic/robots/"+robotID+"/approval/resolve", payload)
}

// Schedule registers a recurring autonomous mission for a robot via vxchrono
// (payload: objective, schedule_type, cadence_minutes|cron_expr, ...).
func (c *Client) Schedule(ctx context.Context, robotID string, payload map[string]interface{}) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.Schedule: robotID is required")
	}
	return c.do(ctx, "robotic.Schedule", "POST",
		"/api/v2/robotic/robots/"+robotID+"/schedule", payload)
}

// Templates lists the built-in robot archetypes (arm, mobile, humanoid, drone,
// computer) with their kinematic params + default capabilities. Use one as a
// blueprint when registering a robot.
func (c *Client) Templates(ctx context.Context) (Result, error) {
	return c.do(ctx, "robotic.Templates", "GET", "/api/v2/robotic/templates", nil)
}

// State returns the live offline-physics state of a robot — pose, joints,
// battery, balance — for virtual robots, or device telemetry for real ones.
func (c *Client) State(ctx context.Context, robotID string) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.State: robotID is required")
	}
	return c.do(ctx, "robotic.State", "GET", "/api/v2/robotic/robots/"+robotID+"/state", nil)
}

// Simulate DRY-RUNS a motion through the physics engine and returns the
// predicted trajectory. It changes no state and dispatches no command (so it
// bypasses no approval policy). payload: {action, parameters, samples, type}.
func (c *Client) Simulate(ctx context.Context, robotID string, payload map[string]interface{}) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.Simulate: robotID is required")
	}
	return c.do(ctx, "robotic.Simulate", "POST",
		"/api/v2/robotic/robots/"+robotID+"/simulate", payload)
}

// Kinematics computes forward (op=fk, needs joints) or inverse (op=ik, needs
// x,y) kinematics for a 2-link planar arm. payload: {op, joints|x,y, link1_m,
// link2_m, elbow_up}.
func (c *Client) Kinematics(ctx context.Context, robotID string, payload map[string]interface{}) (Result, error) {
	if robotID == "" {
		return nil, errors.New("robotic.Kinematics: robotID is required")
	}
	return c.do(ctx, "robotic.Kinematics", "POST",
		"/api/v2/robotic/robots/"+robotID+"/kinematics", payload)
}

// FleetCommand issues a command to every robot in the fleet.
func (c *Client) FleetCommand(ctx context.Context, payload map[string]interface{}) (Result, error) {
	return c.do(ctx, "robotic.FleetCommand", "POST", "/api/v2/robotic/fleet/command", payload)
}

// FleetMission runs a multi-robot mission through the workflow engine plus a
// per-robot LLM plan (payload: objective, robot_ids|robot_type|tags).
func (c *Client) FleetMission(ctx context.Context, payload map[string]interface{}) (Result, error) {
	return c.do(ctx, "robotic.FleetMission", "POST", "/api/v2/robotic/fleet/mission", payload)
}
