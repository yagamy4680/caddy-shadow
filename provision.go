package shadow

import (
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/itchyny/gojq"
)

// Provision implements caddy.Provisioner
func (h *Handler) Provision(ctx caddy.Context) (err error) {
	err = h.provisionHandlers(ctx)
	if err != nil {
		return
	}

	h.slogger = ctx.Slogger()

	h.json = make([]*gojq.Query, len(h.JSON))
	for i, qStr := range h.JSON {
		h.json[i], _ = gojq.Parse(string(qStr))
	}

	h.ignoreJSON = make([]*gojq.Query, len(h.IgnoreJSON))
	for i, qStr := range h.IgnoreJSON {
		h.ignoreJSON[i], _ = gojq.Parse(string(qStr))
	}

	h.redactJSON = make([]*gojq.Query, len(h.RedactJSON))
	for i, qStr := range h.RedactJSON {
		h.redactJSON[i], _ = gojq.Parse(string(qStr))
	}

	return nil
}

func (h *Handler) provisionHandlers(ctx caddy.Context) (err error) {
	var mod any
	mod, err = ctx.LoadModuleByID("http.handlers.subroute", h.ShadowJSON)
	if err != nil {
		return fmt.Errorf("error loading shadow module: %w", err)
	}
	h.shadow = mod.(caddyhttp.MiddlewareHandler)
	mod, err = ctx.LoadModuleByID("http.handlers.subroute", h.PrimaryJSON)
	if err != nil {
		return fmt.Errorf("error loading primary module: %w", err)
	}
	h.primary = mod.(caddyhttp.MiddlewareHandler)

	if provisioner, ok := h.shadow.(caddy.Provisioner); ok {
		err = provisioner.Provision(ctx)
		if err != nil {
			return fmt.Errorf("error provisioning shadow: %w", err)
		}
	}
	if provisioner, ok := h.primary.(caddy.Provisioner); ok {
		err = provisioner.Provision(ctx)
		if err != nil {
			return fmt.Errorf("error provisioning shadow: %w", err)
		}
	}

	return nil
}
