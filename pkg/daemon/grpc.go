package daemon

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/lancekrogers/agent-coordinator/pkg/daemon/pb"
)

// GRPCClient implements DaemonClient using gRPC transport.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client pb.DaemonServiceClient
	config Config
}

// NewGRPCClient creates a new gRPC daemon client and establishes the connection.
func NewGRPCClient(ctx context.Context, config Config) (*GRPCClient, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("new grpc client: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("new grpc client: %w", err)
	}

	var opts []grpc.DialOption
	if config.TLSEnabled {
		creds, err := credentials.NewClientTLSFromFile(config.TLSCertPath, "")
		if err != nil {
			return nil, fmt.Errorf("new grpc client: load TLS credentials: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(config.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("new grpc client: dial %s: %w", config.Address, err)
	}

	return &GRPCClient{
		conn:   conn,
		client: pb.NewDaemonServiceClient(conn),
		config: config,
	}, nil
}

// Register registers this agent with the daemon.
func (c *GRPCClient) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.config.CallTimeout)
	defer cancel()

	resp, err := c.client.Register(callCtx, &pb.RegisterReq{
		AgentName:       req.AgentName,
		AgentType:       req.AgentType,
		Capabilities:    req.Capabilities,
		HederaAccountId: req.HederaAccountID,
	})
	if err != nil {
		return nil, fmt.Errorf("register agent %q: %w", req.AgentName, err)
	}

	return &RegisterResponse{
		AgentID:      resp.AgentId,
		SessionID:    resp.SessionId,
		RegisteredAt: time.Unix(resp.RegisteredAtUnix, 0),
	}, nil
}

// Execute sends a task execution request to the daemon.
func (c *GRPCClient) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = c.config.CallTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := c.client.Execute(callCtx, &pb.ExecuteReq{
		TaskId:    req.TaskID,
		TaskType:  req.TaskType,
		Payload:   req.Payload,
		TimeoutMs: req.Timeout.Milliseconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("execute task %s type %s: %w", req.TaskID, req.TaskType, err)
	}

	return &ExecuteResponse{
		TaskID:   resp.TaskId,
		Status:   resp.Status,
		Result:   resp.Result,
		Duration: time.Duration(resp.DurationMs) * time.Millisecond,
	}, nil
}

// Heartbeat sends a liveness signal to the daemon.
func (c *GRPCClient) Heartbeat(ctx context.Context, req HeartbeatRequest) error {
	callCtx, cancel := context.WithTimeout(ctx, c.config.CallTimeout)
	defer cancel()

	_, err := c.client.Heartbeat(callCtx, &pb.HeartbeatReq{
		AgentId:       req.AgentID,
		SessionId:     req.SessionID,
		TimestampUnix: req.Timestamp.Unix(),
	})
	if err != nil {
		return fmt.Errorf("heartbeat for agent %s: %w", req.AgentID, err)
	}

	return nil
}

// Close gracefully shuts down the gRPC connection.
func (c *GRPCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Compile-time interface compliance check.
var _ DaemonClient = (*GRPCClient)(nil)
