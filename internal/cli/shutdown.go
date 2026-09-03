// Package cli — unified graceful shutdown.
//
// This file is the single source of truth for tearing down everything
// bootstrapSession assembles. Previously shutdown logic was duplicated
// (and diverged) across three places: the runSession signal-handler
// goroutine, the runSession deferred cleanup, server.go's
// shutdownServers, and run.go's cleanupBootstrap. That duplication
// caused gaps (ANPServer and ARDRegistry leaked in some paths) and a
// double-shutdown hazard (runSession could stop the same servers from
// both the goroutine and the defer).
//
// shutdownBootstrap is idempotent (guarded by sync.Once) and covers
// every field of BootstrapState, so all four call sites now delegate to
// it and can no longer drift.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/util"
)

// shutdownBootstrap tears down all resources held by state (protocol
// servers, gateway, knowledge manager) and then closes the agent loop.
// It is safe to call from multiple paths (e.g. a signal handler and a
// deferred cleanup): a sync.Once ensures the work runs exactly once.
//
// Shutdown order:
//  1. Gateway first — close inbound message channels so no new agent
//     runs are triggered while the rest tears down.
//  2. Protocol servers (A2A, AG-UI, ACP, ACP-MCP bridge, ARD, ANP).
//  3. Knowledge manager.
//  4. CoreLoop last — its closeFn stops memory workers, runner,
//     session, telemetry, and finally the DB pool.
//
// Errors from individual stops are logged but do not abort the sequence
// (best-effort shutdown). The loop.Close() result is returned for
// callers that wish to report it.
func shutdownBootstrap(
	ctx context.Context,
	state *BootstrapState,
	loop *agent.CoreLoop,
) error {
	if state == nil {
		return nil
	}

	// Hard watchdog: some .Close() / .Stop() calls in runShutdown
	// ignore the context deadline (e.g. knowledge manager's gse
	// dictionary, MCP subprocess drain, telemetry flush).  If any of
	// them blocks, the process would hang forever.  Start a goroutine
	// that force-exits after a hard deadline; it is a no-op when
	// shutdown completes normally because os.Exit kills it.
	go func() {
		wdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		<-wdCtx.Done()
		util.Logger.Warn("shutdown watchdog: forcing exit")
		os.Exit(0)
	}()

	// shutdownState is embedded in BootstrapState; its do() method is
	// promoted, so state.do runs the teardown exactly once.
	state.do(ctx, loop, state)
	return state.err
}

// shutdownState carries the sync.Once that makes shutdownBootstrap
// idempotent. It is embedded in BootstrapState so every call site
// shares the same guard.
type shutdownState struct {
	once sync.Once
	err  error
}

func (s *shutdownState) do(
	ctx context.Context, loop *agent.CoreLoop, state *BootstrapState,
) error {
	s.once.Do(func() {
		s.err = runShutdown(ctx, loop, state)
	})
	return s.err
}

// runShutdown performs the actual teardown. It is called at most once
// per BootstrapState (guarded by shutdownState.once).
func runShutdown(
	ctx context.Context, loop *agent.CoreLoop, state *BootstrapState,
) error {
	var errs []error

	// 1. Gateway — stop inbound channels first.
	if state.GatewayServer != nil {
		if err := state.GatewayServer.Stop(ctx); err != nil {
			util.Logger.Warn("gateway stop error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  Gateway server stopped")
	}

	// 2. Protocol servers.
	if state.A2AServer != nil {
		if err := state.A2AServer.Stop(ctx); err != nil {
			util.Logger.Warn("A2A server stop error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  A2A server stopped")
	}
	if state.AGUIServer != nil {
		_ = state.AGUIServer.Stop(ctx)
		fmt.Println("  AG-UI server stopped")
	}
	if state.ACPServer != nil {
		_ = state.ACPServer.Stop(ctx)
		fmt.Println("  ACP server stopped")
	}
	if state.ACPMCPBridge != nil {
		if err := state.ACPMCPBridge.Stop(); err != nil {
			util.Logger.Warn("ACP MCP bridge stop error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  ACP MCP bridge stopped")
	}
	if state.MCPServer != nil {
		if err := state.MCPServer.Shutdown(ctx); err != nil {
			util.Logger.Warn("MCP server stop error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  MCP server stopped")
	}
	if state.ARDRegistry != nil {
		if err := state.ARDRegistry.Shutdown(ctx); err != nil {
			util.Logger.Warn("ARD registry stop error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  ARD registry stopped")
	}
	if state.ANPServer != nil {
		if err := state.ANPServer.Shutdown(ctx); err != nil {
			util.Logger.Warn("ANP server stop error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  ANP server stopped")
	}
	if state.CredentialRotator != nil {
		state.CredentialRotator.Stop()
		fmt.Println("  Credential rotator stopped")
	}

	// Extension manager — closes external MCP stdio subprocesses.
	// Must run BEFORE CoreLoop close so tool calls in flight don't
	// hit a closed transport.
	if state.ExtMgr != nil {
		if err := state.ExtMgr.Close(); err != nil {
			util.Logger.Warn("extension manager close error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  Extension manager stopped")
	}

	// 3. Knowledge manager.
	if state.KnowledgeMgr != nil {
		if err := state.KnowledgeMgr.Close(); err != nil {
			util.Logger.Warn("knowledge manager close error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
		fmt.Println("  Knowledge manager stopped")
	}

	// 4. CoreLoop last — runs the full internal cleanup chain
	//    (memory workers → runner → session → telemetry → DB pool).
	if loop != nil {
		if err := loop.Close(); err != nil {
			util.Logger.Warn("agent loop close error",
				slog.String("error", err.Error()))
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown encountered %d error(s): %w", len(errs), errors.Join(errs...))
	}
	return nil
}
