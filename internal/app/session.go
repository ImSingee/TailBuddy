package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ImSingee/TailBuddy/internal/config"
	"github.com/ImSingee/TailBuddy/internal/engine"
)

// The interface lets lifecycle tests use an offline fake, never a real tailnet.
type nodeEngine interface {
	Start(context.Context, config.Config) error
	Stop(context.Context) error
	Status() engine.Snapshot
	ApplyConfig(context.Context, config.Config) error
	BeginLogin(context.Context) (string, error)
}

func (a *App) canDisconnect() bool {
	return a.snapshot.Active || a.cfg.ConnectionWanted || a.disconnectSavePending
}

func (a *App) resumeConnection() {
	if a.smoke || a.fatal != "" || a.engine == nil || a.busy || a.quitting || a.snapshot.Active {
		return
	}
	if a.cfg.CanResume() {
		a.startConnection(a.cfg)
	}
}

func (a *App) startConnection(cfg config.Config) {
	cfg.ConnectionWanted = true
	a.cfg = cfg
	a.run(func() error { return connectNode(a.ctx, a.stateDir, cfg, a.engine) }, "")
}

func (a *App) dismissConsent() {
	a.consentOpen = false
	a.accessConsent, a.credentialConsent = false, false
}

func (a *App) confirmConsent() {
	if !a.consentOpen || !a.accessConsent || !a.credentialConsent || a.busy || a.quitting || a.fatal != "" {
		return
	}
	// Nothing is persisted by checking a box alone. Only this explicit action
	// grants the current scope and continues the user's connection request.
	a.cfg.ConsentVersion = config.CurrentConsentVersion
	a.cfg.ConsentAccepted, a.cfg.PersistCredentials = true, true
	a.dismissConsent()
	a.connect()
}

func connectNode(ctx context.Context, dir string, cfg config.Config, node nodeEngine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !cfg.CanResume() {
		return engine.ErrConsentRequired
	}
	// Persist consent and intent before any helper can expose services. The
	// engine also saves this exact configuration before launching its process.
	if err := config.Save(dir, cfg); err != nil {
		return err
	}
	return node.Start(ctx, cfg)
}

func disconnectNode(ctx context.Context, dir string, cfg config.Config, node nodeEngine, saveMessage string) error {
	cfg.ConnectionWanted = false
	saveErr := config.Save(dir, cfg)
	// A full/read-only disk must never prevent the requested exposure shutdown.
	var stopErr error
	if node != nil {
		stopErr = node.Stop(ctx)
	}
	if saveErr != nil {
		return errors.Join(fmt.Errorf("%s: %w", saveMessage, saveErr), stopErr)
	}
	return stopErr
}
