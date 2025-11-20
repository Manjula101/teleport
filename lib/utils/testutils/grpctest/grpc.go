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

package grpctest

import (
	"context"

	"google.golang.org/grpc"
)

// NewServerStream creates an in-memory grpc.ServerStreamingServer[T] (unidirectional)
// for use in tests, particularly when using synctest.
//
// Users can consume the messages sent by the server by calling Recv.
//
// Private fields are purposefully written in a *not* concurrency-safe manner to
// simulate non-concurrency safety of real over-the-network GRPC stream. It will
// be caught when executing the test with the race detector enbled.
func NewServerStream[T any](ctx context.Context) *ServerStream[T] {
	return &ServerStream[T]{
		ctx:      ctx,
		toClient: make(chan *T, 1),
	}
}

// ServerStream is an in-memory implementation of grpc.ServerStreamingServer[T]
// for use in tests.
type ServerStream[T any] struct {
	grpc.ServerStream
	ctx              context.Context
	toClient         chan *T
	sendRaceDetector bool // simulate non-concurrency safety
}

// Context returns the context for this stream.
func (s *ServerStream[T]) Context() context.Context { return s.ctx }

// Send satisfies grpc.ServerStreamingServer[T].
func (s *ServerStream[T]) Send(t *T) error {
	s.sendRaceDetector = true
	select {
	case s.toClient <- t:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// Recv can be called from tests to receive the most recent *T sent by the server.
func (s *ServerStream[T]) Recv() (*T, error) {
	select {
	case t := <-s.toClient:
		return t, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}
