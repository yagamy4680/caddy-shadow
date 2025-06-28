package shadow

import (
	"encoding/json"
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Handler{})
	httpcaddyfile.RegisterHandlerDirective("shadow", ParseCaddyfile)
}

func ParseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	hnd := new(Handler)
	h.Next()
	for h.NextBlock(0) {
		handlerName := h.Val()
		switch handlerName {
		case "primary", "shadow":
			innerHnd, err := httpcaddyfile.ParseSegmentAsSubroute(h.WithDispenser(h.NewFromNextSegment()))
			if err != nil {
				return nil, fmt.Errorf("error unmarshaling %s: %w", handlerName, err)
			}

			if handlerName == "primary" {
				hnd.PrimaryJSON, err = json.Marshal(innerHnd)
				if err != nil {
					return nil, fmt.Errorf("error marshaling %s: %w", handlerName, err)
				}
			} else {
				hnd.ShadowJSON, err = json.Marshal(innerHnd)
				if err != nil {
					return nil, fmt.Errorf("error marshaling %s: %w", handlerName, err)
				}
			}
		case "compare_body":
			hnd.ComparisonConfig.Body = true
		case "compare_status":
			hnd.ComparisonConfig.Status = true
		case "compare_headers":
			hnd.ComparisonConfig.Headers = h.RemainingArgs()
		case "compare_json":
			for h.NextArg() {
				qStr := h.Val()
				hnd.ComparisonConfig.JSON = append(hnd.ComparisonConfig.JSON, JQQuery(qStr))
			}
		case "ignore_json":
			for h.NextArg() {
				qStr := h.Val()
				hnd.ComparisonConfig.IgnoreJSON = append(hnd.ComparisonConfig.IgnoreJSON, JQQuery(qStr))
			}
		case "redact_json":
			for h.NextArg() {
				qStr := h.Val()
				hnd.ReportingConfig.RedactJSON = append(hnd.ReportingConfig.RedactJSON, JQQuery(qStr))
			}
		case "no_log":
			hnd.ReportingConfig.NoLog = true
		case "log_level":
			if len(h.RemainingArgs()) < 1 {
				return nil, fmt.Errorf("log_level requires a log level")
			}
			h.NextArg()
			ll := LogLevel(h.Val())
			hnd.ReportingConfig.LogLevel = &ll
		}
	}
	return hnd, nil
}
