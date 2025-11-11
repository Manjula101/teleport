/*
 * Teleport
 * Copyright (C) 2025  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gravitational/trace"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gravitational/teleport"
	apidefaults "github.com/gravitational/teleport/api/defaults"
	apievents "github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/api/types/wrappers"
	"github.com/gravitational/teleport/lib/events"
	appcommon "github.com/gravitational/teleport/lib/srv/app/common"
	"github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/teleport/lib/utils/mcputils"
)

type sessionAuditorConfig struct {
	emitter    apievents.Emitter
	logger     *slog.Logger
	hostID     string
	sessionCtx *SessionCtx
	// TODO(greedy52) add recording support.
	preparer events.SessionEventPreparer
}

func (c *sessionAuditorConfig) checkAndSetDefaults() error {
	if c.emitter == nil {
		return trace.BadParameter("missing emitter")
	}
	if c.hostID == "" {
		return trace.BadParameter("missing hostID")
	}
	if c.sessionCtx == nil {
		return trace.BadParameter("missing sessionCtx")
	}
	if c.preparer == nil {
		return trace.BadParameter("missing preparer")
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return nil
}

// sessionAuditor handles audit events for a session.
type sessionAuditor struct {
	sessionAuditorConfig

	// pendingEvents is used to delay sending session start and initialize
	// events until more metadata are captured.
	pendingEvents []apievents.AuditEvent
	mu            sync.Mutex
}

func newSessionAuditor(cfg sessionAuditorConfig) (*sessionAuditor, error) {
	if err := cfg.checkAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}
	return &sessionAuditor{
		sessionAuditorConfig: cfg,
	}, nil
}

type eventOptions struct {
	err    error
	header http.Header
}

type eventOptionFunc func(*eventOptions)

func newEventOptions(options ...eventOptionFunc) (opt eventOptions) {
	for _, fn := range options {
		if fn != nil {
			fn(&opt)
		}
	}
	return
}

func eventWithError(err error) eventOptionFunc {
	return func(o *eventOptions) {
		o.err = err
	}
}

func eventWithHTTPResponseError(resp *http.Response, err error) eventOptionFunc {
	return eventWithError(convertHTTPResponseErrorForAudit(resp, err))
}

func eventWithHeader(r *http.Request) eventOptionFunc {
	return func(o *eventOptions) {
		o.header = headersForAudit(r.Header)
	}
}

func (a *sessionAuditor) shouldEmitEvent(method mcp.MCPMethod) bool {
	// Do not record discovery, ping calls.
	switch method {
	case mcp.MethodPing,
		mcp.MethodResourcesList,
		mcp.MethodResourcesTemplatesList,
		mcp.MethodPromptsList,
		mcp.MethodToolsList:
		return false
	default:
		return true
	}
}

func (a *sessionAuditor) appendStartEvent(ctx context.Context) {
	a.appendEvent(ctx, &apievents.MCPSessionStart{
		Metadata: a.makeEventMetadata(
			events.MCPSessionStartEvent,
			events.MCPSessionStartCode,
		),
		ServerMetadata:     a.makeServerMetadata(),
		SessionMetadata:    a.makeSessionMetadata(),
		UserMetadata:       a.makeUserMetadata(),
		ConnectionMetadata: a.makeConnectionMetadata(),
		AppMetadata:        a.makeAppMetadata(),
	})
}

func (a *sessionAuditor) emitEndEvent(ctx context.Context, options ...eventOptionFunc) {
	opts := newEventOptions(options...)

	event := &apievents.MCPSessionEnd{
		Metadata: a.makeEventMetadata(
			events.MCPSessionEndEvent,
			events.MCPSessionEndCode,
		),
		ServerMetadata:     a.makeServerMetadata(),
		SessionMetadata:    a.makeSessionMetadata(),
		UserMetadata:       a.makeUserMetadata(),
		ConnectionMetadata: a.makeConnectionMetadata(),
		AppMetadata:        a.makeAppMetadata(),
		Status: apievents.Status{
			Success: true,
		},
		Headers: wrappers.Traits(opts.header),
	}

	if opts.err != nil {
		event.Metadata.Code = events.MCPSessionEndFailureCode
		event.Status.Success = false
		event.Status.Error = opts.err.Error()
	}
	a.flushAndEmitEvent(ctx, event)
}

func (a *sessionAuditor) emitNotificationEvent(ctx context.Context, msg *mcputils.JSONRPCNotification, options ...eventOptionFunc) {
	opts := newEventOptions(options...)
	if opts.err == nil && !a.shouldEmitEvent(msg.Method) {
		return
	}
	event := &apievents.MCPSessionNotification{
		Metadata: a.makeEventMetadata(
			events.MCPSessionNotificationEvent,
			events.MCPSessionNotificationCode,
		),
		SessionMetadata: a.makeSessionMetadata(),
		UserMetadata:    a.makeUserMetadata(),
		AppMetadata:     a.makeAppMetadata(),
		Message: apievents.MCPJSONRPCMessage{
			JSONRPC: msg.JSONRPC,
			Method:  string(msg.Method),
			Params:  msg.Params.GetEventParams(),
		},
		Status: apievents.Status{
			Success: true,
		},
		Headers: wrappers.Traits(opts.header),
	}
	if opts.err != nil {
		event.Metadata.Code = events.MCPSessionNotificationFailureCode
		event.Status.Success = false
		event.Status.Error = opts.err.Error()
	}
	a.flushAndEmitEvent(ctx, event)
}

func (a *sessionAuditor) emitOrAppendRequestEvent(ctx context.Context, msg *mcputils.JSONRPCRequest, options ...eventOptionFunc) {
	opts := newEventOptions(options...)
	if opts.err == nil && !a.shouldEmitEvent(msg.Method) {
		return
	}
	event := &apievents.MCPSessionRequest{
		Metadata: a.makeEventMetadata(
			events.MCPSessionRequestEvent,
			events.MCPSessionRequestCode,
		),
		SessionMetadata: a.makeSessionMetadata(),
		UserMetadata:    a.makeUserMetadata(),
		AppMetadata:     a.makeAppMetadata(),
		Status: apievents.Status{
			Success: true,
		},
		Message: apievents.MCPJSONRPCMessage{
			JSONRPC: msg.JSONRPC,
			Method:  string(msg.Method),
			ID:      msg.ID.String(),
			Params:  msg.Params.GetEventParams(),
		},
		Headers: wrappers.Traits(opts.header),
	}

	if opts.err != nil {
		event.Metadata.Code = events.MCPSessionRequestFailureCode
		event.Status.Success = false
		event.Status.Error = opts.err.Error()
	}

	// Wait for server information from initialize result before emitting session
	// start and initialize request events.
	if msg.Method == mcp.MethodInitialize {
		if params, err := msg.GetInitializeParams(); err == nil {
			a.updatePendingSessionStartEvent(func(sessionStartEvent *apievents.MCPSessionStart) {
				sessionStartEvent.ClientInfo = fmt.Sprintf("%s/%s", params.ClientInfo.Name, params.ClientInfo.Version)
				// TODO(greedy52) record protocol version, ingress and egress auth type
			})
		}
		if opts.err == nil {
			a.appendEvent(ctx, event)
			return
		}
	}
	a.flushAndEmitEvent(ctx, event)
}

func (a *sessionAuditor) emitListenSSEStreamEvent(ctx context.Context, options ...eventOptionFunc) {
	opts := newEventOptions(options...)
	event := &apievents.MCPSessionListenSSEStream{
		Metadata: a.makeEventMetadata(
			events.MCPSessionListenSSEStream,
			events.MCPSessionListenSSEStreamCode,
		),
		SessionMetadata: a.makeSessionMetadata(),
		UserMetadata:    a.makeUserMetadata(),
		AppMetadata:     a.makeAppMetadata(),
		Status: apievents.Status{
			Success: true,
		},
		Headers: wrappers.Traits(opts.header),
	}
	if opts.err != nil {
		event.Metadata.Code = events.MCPSessionListenSSEStreamFailureCode
		event.Status.Success = false
		event.Status.Error = opts.err.Error()
	}
	a.flushAndEmitEvent(ctx, event)
}

func (a *sessionAuditor) emitInvalidHTTPRequest(ctx context.Context, r *http.Request) {
	body, _ := utils.GetAndReplaceRequestBody(r)
	event := &apievents.MCPSessionInvalidHTTPRequest{
		Metadata: a.makeEventMetadata(
			events.MCPSessionInvalidHTTPRequest,
			events.MCPSessionInvalidHTTPRequestCode,
		),
		SessionMetadata: a.makeSessionMetadata(),
		UserMetadata:    a.makeUserMetadata(),
		AppMetadata:     a.makeAppMetadata(),
		Path:            r.URL.Path,
		Method:          r.Method,
		Body:            body,
		RawQuery:        r.URL.RawQuery,
		Headers:         wrappers.Traits(r.Header),
	}
	a.flushAndEmitEvent(ctx, event)
}

func (a *sessionAuditor) appendEvent(ctx context.Context, event apievents.AuditEvent) {
	preparedEvent, err := a.preparer.PrepareSessionEvent(event)
	if err != nil {
		a.logger.ErrorContext(ctx, "Failed to prepare event",
			"error", err,
			"event_type", event.GetType(),
			"event_id", event.GetID(),
		)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingEvents = append(a.pendingEvents, preparedEvent.GetAuditEvent())
}

func (a *sessionAuditor) flush(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, event := range a.pendingEvents {
		if err := a.emitter.EmitAuditEvent(ctx, event); err != nil {
			a.logger.ErrorContext(ctx, "Failed to emit audit event",
				"error", err,
				"event_type", event.GetType(),
				"event_id", event.GetID(),
			)
		}
	}
	a.pendingEvents = nil
}

func (a *sessionAuditor) flushAndEmitEvent(ctx context.Context, event apievents.AuditEvent) {
	a.flush(ctx)

	preparedEvent, err := a.preparer.PrepareSessionEvent(event)
	if err != nil {
		a.logger.ErrorContext(ctx, "Failed to prepare event",
			"error", err,
			"event_type", event.GetType(),
			"event_id", event.GetID(),
		)
		return
	}
	if err := a.emitter.EmitAuditEvent(ctx, preparedEvent.GetAuditEvent()); err != nil {
		a.logger.ErrorContext(ctx, "Failed to emit audit event",
			"error", err,
			"event_type", event.GetType(),
			"event_id", event.GetID(),
		)
	}
}

func (a *sessionAuditor) makeEventMetadata(eventType, eventCode string) apievents.Metadata {
	return apievents.Metadata{
		Type:        eventType,
		Code:        eventCode,
		ClusterName: a.sessionCtx.Identity.RouteToApp.ClusterName,
	}
}

func (a *sessionAuditor) makeServerMetadata() apievents.ServerMetadata {
	return apievents.ServerMetadata{
		ServerVersion:   teleport.Version,
		ServerID:        a.hostID,
		ServerNamespace: apidefaults.Namespace,
	}
}

func (a *sessionAuditor) makeConnectionMetadata() apievents.ConnectionMetadata {
	return apievents.ConnectionMetadata{
		RemoteAddr: a.sessionCtx.Identity.LoginIP,
	}
}

func (a *sessionAuditor) makeAppMetadata() apievents.AppMetadata {
	return apievents.AppMetadata{
		AppURI:  a.sessionCtx.App.GetURI(),
		AppName: a.sessionCtx.App.GetName(),
	}
}

func (a *sessionAuditor) makeSessionMetadata() apievents.SessionMetadata {
	return apievents.SessionMetadata{
		SessionID:        a.sessionCtx.sessionID.String(),
		WithMFA:          a.sessionCtx.Identity.MFAVerified,
		PrivateKeyPolicy: string(a.sessionCtx.Identity.PrivateKeyPolicy),
	}
}

func (a *sessionAuditor) makeUserMetadata() apievents.UserMetadata {
	return a.sessionCtx.Identity.GetUserMetadata()
}

func (a *sessionAuditor) updatePendingSessionStartEvent(fn func(*apievents.MCPSessionStart)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, event := range a.pendingEvents {
		if sessionStartEvent, ok := event.(*apievents.MCPSessionStart); ok {
			fn(sessionStartEvent)
		}
	}
}

func (a *sessionAuditor) updatePendingSessionStartEventWithResult(ctx context.Context, resp *mcputils.JSONRPCResponse) {
	// TODO(greedy52) resp.GetInitializeResult() and update session start event

	// We can flush now as we receive the result.
	a.flush(ctx)
}

func (a *sessionAuditor) updatePendingSessionStartEventWithExternalSessionID(sessionID string) {
	a.updatePendingSessionStartEvent(func(event *apievents.MCPSessionStart) {
		event.McpSessionId = sessionID
	})
}

var headersWithSecret = []string{
	"Authorization",
	"X-API-Key",
}

func headersForAudit(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	ret := h.Clone()
	for _, key := range appcommon.ReservedHeaders {
		ret.Del(key)
	}
	for _, key := range headersWithSecret {
		if len(ret.Values(key)) > 0 {
			ret.Set(key, "<REDACTED>")
		}
	}
	return ret
}
