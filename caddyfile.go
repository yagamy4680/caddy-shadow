package shadow

import (
	"encoding/json"
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func (h *Handler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()
	for d.NextBlock(0) {
		switch d.Val() {
		case "primary", "shadow":
			handlerName := d.Val()
			if !d.NextBlock(d.Nesting()) {
				return fmt.Errorf("unable to read %s directive", handlerName)
			}

			directive := d.Val()
			d2 := d.NewFromNextSegment()
			d2.Prev()
			hnd, err := unmarshalWrapped(directive, d2)
			if err != nil {
				return fmt.Errorf("error unmarshaling %s: %w", directive, err)
			}

			if handlerName == "primary" {
				h.PrimaryJSON, err = json.Marshal(hnd)
				if err != nil {
					return fmt.Errorf("error marshaling %s: %w", directive, err)
				}
				h.PrimaryModuleID = string(hnd.(caddy.Module).CaddyModule().ID)
			} else {
				h.ShadowJSON, err = json.Marshal(hnd)
				if err != nil {
					return fmt.Errorf("error marshaling %s: %w", directive, err)
				}
				h.ShadowModuleID = string(hnd.(caddy.Module).CaddyModule().ID)
			}
		case "compare_body":
			h.ComparisonConfig.Body = true
		case "compare_status":
			h.ComparisonConfig.Status = true
		case "compare_headers":
			h.ComparisonConfig.Headers = d.RemainingArgs()
		case "compare_json":
			for d.NextArg() {
				qStr := d.Val()
				h.ComparisonConfig.JSON = append(h.ComparisonConfig.JSON, JQQuery(qStr))
			}
		case "ignore_json":
			for d.NextArg() {
				qStr := d.Val()
				h.ComparisonConfig.IgnoreJSON = append(h.ComparisonConfig.IgnoreJSON, JQQuery(qStr))
			}
		case "redact_json":
			for d.NextArg() {
				qStr := d.Val()
				h.ReportingConfig.RedactJSON = append(h.ReportingConfig.RedactJSON, JQQuery(qStr))
			}
		case "no_log":
			h.ReportingConfig.NoLog = true
		case "log_level":
			if len(d.RemainingArgs()) < 1 {
				return fmt.Errorf("log_level requires a log level")
			}
			d.NextArg()
			ll := LogLevel(d.Val())
			h.ReportingConfig.LogLevel = &ll
		}
	}
	return nil
}

func unmarshalWrapped(directive string, d *caddyfile.Dispenser) (hnd caddyhttp.MiddlewareHandler, err error) {
	switch directive {
	case "respond":
		hnd = new(caddyhttp.StaticResponse)
	default:
		var modInfo caddy.ModuleInfo
		modInfo, err = caddy.GetModule("http.handlers." + directive)
		if err != nil {
			return nil, fmt.Errorf("error loading module %s: %w", directive, err)
		}
		var ok bool
		if hnd, ok = modInfo.New().(caddyhttp.MiddlewareHandler); !ok {
			return nil, fmt.Errorf("module %s is not a caddyhttp.MiddlewareHandler", directive)
		}
	}

	if unmarshaler, ok := hnd.(caddyfile.Unmarshaler); ok {
		err = unmarshaler.UnmarshalCaddyfile(d)
	}

	return hnd, err
}
