package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
)

// Client is the public typed wrkq/wrkc client. New binds helper calls to ctx;
// callers that need a per-call context can use Call directly.
type Client struct {
	ctx          context.Context
	transport    Transport
	principalRef string
	scopeRef     string

	Task      TaskService
	Comment   CommentService
	Promise   PromiseService
	Container ContainerService
	Room      RoomService
}

type newOptions struct {
	locator      string
	token        string
	principalRef string
	scopeRef     string
	transport    Transport
}

// Option customizes New.
type Option func(*newOptions) error

// WithLocator is the library equivalent of wrkq --db. It accepts a local path
// in wrkq_local builds or an rpc://host[:port] locator in every build.
func WithLocator(locator string) Option {
	return func(opts *newOptions) error {
		opts.locator = strings.TrimSpace(locator)
		return nil
	}
}

// WithToken supplies the bearer token for a remote locator. When omitted, New
// uses the same WRKQD_TOKEN/WRKQD_TOKEN_FILE precedence as the CLIs.
func WithToken(token string) Option {
	return func(opts *newOptions) error {
		opts.token = token
		return nil
	}
}

// WithPrincipalRef is the programmatic equivalent of --as. It accepts either
// agent:<id> or a full agent ScopeRef and normalizes it to agent:<id>.
func WithPrincipalRef(ref string) Option {
	return func(opts *newOptions) error {
		principal, err := attribution.NormalizeCanonical(ref)
		if err != nil {
			return err
		}
		opts.principalRef = principal
		return nil
	}
}

// WithScopeRef supplies the caller's exact HRC session handle for collaboration
// calls. When omitted, New uses HRC_SESSION_REF, matching wrkc.
func WithScopeRef(ref string) Option {
	return func(opts *newOptions) error {
		opts.scopeRef = strings.TrimSpace(ref)
		return nil
	}
}

// WithTransport injects an already initialized transport. Client.Close owns and
// closes the supplied transport.
func WithTransport(transport Transport) Option {
	return func(opts *newOptions) error {
		if transport == nil {
			return errors.New("client transport is required")
		}
		opts.transport = transport
		return nil
	}
}

// New resolves endpoint, token, and caller identity with the same configuration
// precedence as wrkq/wrkc, opens the fail-closed protocol transport, and returns
// the typed client.
func New(ctx context.Context, options ...Option) (*Client, error) {
	if ctx == nil {
		return nil, errors.New("client context is required")
	}
	opts := newOptions{scopeRef: strings.TrimSpace(os.Getenv("HRC_SESSION_REF"))}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&opts); err != nil {
			return nil, err
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if opts.locator != "" {
		if err := config.ApplyDBLocator(cfg, opts.locator, false); err != nil {
			return nil, err
		}
	}
	if opts.principalRef == "" {
		opts.principalRef, err = configuredPrincipalRef(cfg)
		if err != nil {
			return nil, err
		}
	}

	transport := opts.transport
	if transport == nil {
		if cfg.RemoteEndpoint != "" {
			token := opts.token
			if token == "" {
				token = TokenFromEnv()
			}
			transport, err = NewRemote(cfg.RemoteEndpoint, token, WrkqProfile)
		} else {
			transport, err = newConfiguredLocalTransport(cfg, opts.principalRef)
		}
		if err != nil {
			return nil, err
		}
	}

	client := &Client{
		ctx:          ctx,
		transport:    transport,
		principalRef: opts.principalRef,
		scopeRef:     opts.scopeRef,
	}
	client.Task = TaskService{client: client}
	client.Comment = CommentService{client: client}
	client.Promise = PromiseService{client: client}
	client.Container = ContainerService{client: client}
	client.Room = RoomService{client: client}
	return client, nil
}

func configuredPrincipalRef(cfg *config.Config) (string, error) {
	var resolved *scope.ResolvedScope
	if current, _, err := scope.Resolve(""); err == nil {
		resolved = &current
	}
	attr, err := attribution.Resolve(attribution.ResolveOptions{Config: cfg, ResolvedScope: resolved})
	if err != nil {
		if attribution.IsNoPrincipalConfigured(err) {
			return "", nil
		}
		return "", err
	}
	return attr.PrincipalRef, nil
}

// Call invokes any registered JSON-RPC method and decodes its result into out.
// A nil out discards a successful result.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if c == nil || c.transport == nil {
		return errors.New("client is not initialized")
	}
	if ctx == nil {
		return errors.New("call context is required")
	}
	raw, err := c.transport.Call(ctx, method, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

func (c *Client) call(method string, params any, out any) error {
	return c.Call(c.ctx, method, params, out)
}

// Close releases the underlying transport. It is safe to call more than once.
func (c *Client) Close() error {
	if c == nil || c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

func (c *Client) mutationPrincipal() (string, error) {
	if strings.TrimSpace(c.principalRef) == "" {
		return "", errors.New("mutation requires a caller principal; use client.WithPrincipalRef(agent:<id>) or configure WRKQ_PRINCIPAL_REF/runtime scope")
	}
	return c.principalRef, nil
}
