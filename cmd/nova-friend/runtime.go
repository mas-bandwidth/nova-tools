package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// realWorld uses the shared seat-first connection and explicit adapter only
// (STANDARD section 4); no sprint state supplies a registration.
func realWorld(ctx context.Context, sel *seatcred.Selection) world {
	return world{ctx: ctx, hasSeat: sel.Selected() != "", redisDefault: redisDefault(sel, os.Getenv), run: func(ctx context.Context, name string, r request) *tool.Out {
		cfg := friend.CommandConfig{}
		var adapter friend.Adapter
		if name != "status" {
			if err := readAdapter(r.Adapter, &cfg); err != nil {
				return tool.Refuse(err.Error())
			}
			var err error
			adapter, err = friend.NewAdapter(cfg, r.Timeout)
			if err != nil {
				return tool.Refuse(err.Error())
			}
		}
		opctx, cancel := context.WithTimeout(ctx, r.Timeout)
		conn, err := openTeam(opctx, r.Redis, sel, os.Getenv)
		cancel()
		if err != nil {
			return tool.Refuse(err.Error())
		}
		defer func() {
			if err := conn.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "nova-friend NOTE closing store: %s\n", oneline.Err(err))
			}
		}()
		bus, err := friendbus.New(friendbus.Config{Prefix: r.Prefix, Redis: conn.Client()})
		if err != nil {
			return tool.Refuse(err.Error())
		}
		if name == "status" {
			opctx, cancel := context.WithTimeout(ctx, r.Timeout)
			defer cancel()
			session, found, err := bus.GetSession(opctx, r.Friend)
			if err != nil {
				return tool.Refuse(err.Error())
			}
			out := tool.Done().Fact("friend", r.Friend).Fact("registered", found).Fact("awake", "unknown")
			if found {
				out.Item("session", "id", session.ID, "adapter", session.Adapter, "revision", session.Revision)
			}
			return out.Note("registration is adapter evidence, not current liveness or completed work")
		}
		rt := friend.Runtime{Bus: bus, Name: r.Friend, Consumer: r.Consumer, Adapter: adapter, Lease: r.Lease, Block: r.Block, Retry: r.Retry, Timeout: r.Timeout, OnError: func(err error) {
			fmt.Fprintf(os.Stderr, "nova-friend NOTE delivery remains pending: %s\n", oneline.Err(err))
		}}
		if name == "register" || name == "startup" {
			registration, cancel := context.WithTimeout(ctx, r.Timeout)
			defer cancel()
			session, err := rt.Register(registration)
			if err != nil {
				return tool.Refuse(err.Error())
			}
			return tool.Done().Fact("friend", r.Friend).Fact("session", session.ID).Fact("adapter", session.Adapter).Note("registered from the harness handshake; current awake state remains unknown")
		}
		if err := rt.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return tool.Refuse(err.Error())
		}
		return tool.Done().Fact("friend", r.Friend).Fact("consumer", r.Consumer).Note("listener stopped; acknowledgements require durable harness receipts")
	}}
}

// readAdapter bounds configuration input and refuses trailing or unknown data.
// Libraries considered: encoding/json and io.LimitReader supply this boundary.
func readAdapter(path string, cfg *friend.CommandConfig) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("--adapter wants a readable JSON configuration: %w", err)
	}
	// ignored: closing a read-only regular configuration file cannot change the bytes read
	defer f.Close()
	const limit = 64 * 1024
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if len(b) > limit {
		return fmt.Errorf("--adapter wants at most %d bytes", limit)
	}
	return strictAdapterJSON(b, cfg)
}

// redisDefault preserves the configured seat before the general environment.
func redisDefault(sel *seatcred.Selection, getenv func(string) string) string {
	if addr := sel.Addr(); addr != "" {
		return addr
	}
	return getenv("NOVA_REDIS_ADDR")
}

// openTeam resolves secrets only on a real operation, never in help or plans.
func openTeam(ctx context.Context, addr string, sel *seatcred.Selection, getenv func(string) string) (*redisconn.Conn, error) {
	if sel.Selected() != "" {
		path, err := seatcred.ProfilePath("nova-friend", getenv)
		if err != nil {
			return nil, err
		}
		profile, err := seatcred.LoadProfile(path, sel.Selected(), getenv("HOME"))
		if err == nil {
			lookup := getenv
			sel.SelectProfile(profile, func(string) (seatcred.Cred, error) { return seatcred.ResolveProfile(profile, lookup) })
			if addr == "" {
				addr = profile.Addr
			}
		} else if !errors.Is(err, seatcred.ErrNoProfileRow) || addr == "" {
			return nil, err
		}
	}
	options := redisconn.Options{Addr: addr, Env: redisconn.GeneralEnv}
	cred, selected, err := sel.Active()
	if err != nil {
		return nil, err
	}
	if selected {
		var password string
		if err := cred.Password.Use(func(value string) error { password = value; return nil }); err != nil {
			return nil, err
		}
		options.User, options.PasswordEnv, options.Env = cred.User, cred.Key, redisconn.Env{}
		original := getenv
		getenv = func(k string) string {
			if k == cred.Key {
				return password
			}
			return original(k)
		}
	}
	return redisconn.Open(ctx, options, getenv)
}

// strictAdapterJSON holds configuration to one typed value.
func strictAdapterJSON(b []byte, cfg *friend.CommandConfig) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("--adapter wants one typed JSON configuration: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("--adapter wants exactly one JSON value")
	}
	return nil
}
