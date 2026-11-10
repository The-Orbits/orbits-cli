// Package api is the CLI's side of the Orbits Go SDK (orbits-go): it makes
// the client every command uses, and turns the API's problem documents into
// messages that say what to do. Every API call goes through orbits-go.
package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	orbits "github.com/The-Orbits/orbits-go"
)

// DefaultBaseURL is the production API, with /v1.
const DefaultBaseURL = orbits.DefaultBaseURL

// Options for New.
type Options struct {
	BaseURL string
	Version string // the CLI's, for the User-Agent
	// MaxRetries, when not nil, replaces the SDK's default.
	MaxRetries *int
	HTTP       *http.Client
}

// New is an orbits-go client for token, sending User-Agent
// "orbits-cli/<version> orbits-go/<sdk>", which Settings › API tokens shows
// as the token's "last used by".
func New(token string, o Options) (*orbits.Client, error) {
	opts := []orbits.Option{orbits.WithUserAgent("orbits-cli/" + strings.TrimPrefix(o.Version, "v"))}
	if o.BaseURL != "" {
		opts = append(opts, orbits.WithBaseURL(o.BaseURL))
	}
	if o.MaxRetries != nil {
		opts = append(opts, orbits.WithMaxRetries(*o.MaxRetries))
	}
	if o.HTTP != nil {
		opts = append(opts, orbits.WithHTTPClient(o.HTTP))
	}
	c, err := orbits.New(token, opts...)
	if err != nil {
		return nil, errors.New(strings.TrimPrefix(err.Error(), "orbits: "))
	}
	return c, nil
}

// Error is an API error explained for a person. It keeps the SDK's
// *orbits.Error in its chain, so errors.As and the orbits.Is… helpers work.
type Error struct {
	msg string
	err error
}

func (e *Error) Error() string { return e.msg }
func (e *Error) Unwrap() error { return e.err }

// Context names what a message may mention: the team in effect and how to
// sign in again.
type Context struct {
	Team    string // slug or id
	Login   string // "orbits login"
	WebURL  string // https://orbits.africa
	BaseURL string // the API, for connection errors
}

// Explain turns an error from orbits-go into one that says what happened and
// what to do, keeping the API's own words (its detail) and its request id.
// Anything that is not from the SDK comes back as it was.
func Explain(err error, cx Context) error {
	if err == nil {
		return nil
	}
	var done *Error
	if errors.As(err, &done) {
		return err // explained already
	}
	if cx.Login == "" {
		cx.Login = "orbits login"
	}
	e, ok := orbits.AsError(err)
	if !ok {
		return explainOther(err, cx)
	}
	detail := strings.TrimSpace(e.Detail)
	if detail == "" {
		detail = e.Title
	}
	var lines []string
	switch {
	case e.Status == http.StatusUnauthorized:
		lines = []string{
			"the API refused the token: it is wrong, expired or revoked",
			"Sign in again: " + cx.Login,
		}
	case orbits.IsTokenRefused(err) && strings.HasPrefix(detail, "This API token is read-only"):
		lines = []string{
			"this token is read-only, so it can't make changes (or read variable values)",
			"Sign in again with full access: " + cx.Login + ", and choose Full access in the browser",
		}
	case orbits.IsTokenRefused(err):
		lines = []string{
			strings.TrimSuffix(detail, ".") + ": API tokens can't do this, by design",
			"Do it in the web app instead: " + webURL(cx),
		}
	case e.Status == http.StatusForbidden:
		lines = []string{detail, "Your role in the team may not allow this: ask an owner or admin."}
	case e.Status == http.StatusNotFound:
		lines = []string{detail}
		if cx.Team != "" {
			lines = append(lines, fmt.Sprintf("It does not exist, or it is not in team %s, the team this token acts in.", cx.Team))
		}
	case e.Status == http.StatusPaymentRequired:
		lines = []string{detail}
		if e.RequiredPlan != "" {
			lines = append(lines, fmt.Sprintf("It needs the %s plan: upgrade the team in the web app, %s", e.RequiredPlan, webURL(cx)))
		}
	case e.Status == http.StatusUnprocessableEntity:
		lines = []string{detail}
		for _, fe := range e.Errors {
			if fe.Field != "" {
				lines = append(lines, fmt.Sprintf("  %s: %s", fe.Field, fe.Message))
			} else {
				lines = append(lines, "  "+fe.Message)
			}
		}
	case e.Status == http.StatusTooManyRequests:
		lines = []string{detail}
		if e.RetryAfter > 0 {
			lines = append(lines, fmt.Sprintf("Try again in %s.", e.RetryAfter.Round(time.Second)))
		}
	default:
		lines = []string{detail}
	}
	var ref []string
	if e.Code != "" {
		ref = append(ref, e.Code)
	}
	ref = append(ref, fmt.Sprintf("status %d", e.Status))
	if e.RequestID != "" {
		ref = append(ref, "request "+e.RequestID)
	}
	lines = append(lines, "("+strings.Join(ref, ", ")+")")
	return &Error{msg: strings.Join(lines, "\n"), err: err}
}

func explainOther(err error, cx Context) error {
	var urlErr *url.Error
	var netErr net.Error
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.As(err, &dnsErr), errors.As(err, &opErr), errors.As(err, &urlErr) && errors.As(err, &netErr):
		where := cx.BaseURL
		if where == "" {
			where = "the Orbits API"
		}
		return &Error{msg: fmt.Sprintf("could not reach %s: check your connection and try again\n(%s)", where, cause(err)), err: err}
	}
	if msg := err.Error(); strings.HasPrefix(msg, "orbits: ") {
		return &Error{msg: strings.TrimPrefix(msg, "orbits: "), err: err}
	}
	return err
}

// cause is the innermost message of a network error, without the URL and
// method the outer layers add.
func cause(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return opErr.Err.Error()
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Error()
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}

func webURL(cx Context) string {
	base := cx.WebURL
	if base == "" {
		base = "https://orbits.africa"
	}
	if cx.Team != "" {
		return strings.TrimRight(base, "/") + "/" + url.PathEscape(cx.Team)
	}
	return base
}
