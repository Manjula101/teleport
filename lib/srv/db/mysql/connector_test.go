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

package mysql

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/gravitational/trace"
	"github.com/stretchr/testify/require"
)

func Test_isDialError(t *testing.T) {
	tests := []struct {
		desc string
		err  error
		want bool
	}{
		{
			desc: "non dial error",
			err:  errors.New("llama stampede!"),
		},
		{
			desc: "dial error",
			err:  newDialError(errors.New("failed to dial x.x.x.x")),
			want: true,
		},
		{
			desc: "nil error",
		},
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			require.Equal(t, test.want, isDialError(trace.Wrap(test.err)))
		})
	}
}

func Test_recorderConn(t *testing.T) {
	mustWrite := func(w io.Writer, data string) {
		t.Helper()
		n, err := io.WriteString(w, data)
		require.NoError(t, err)
		require.Equal(t, len(data), n)
	}

	mustRead := func(r io.Reader, want string) {
		t.Helper()
		buf := make([]byte, len(want))
		n, err := io.ReadFull(r, buf)
		require.NoError(t, err)
		require.Equal(t, want, string(buf[:n]))
	}

	conn := &fakeConn{}
	mustWrite(conn, "hello")

	recorder := newRecorderConn(conn)
	mustRead(recorder, "hello")

	mustWrite(conn, "world")
	mustRead(recorder, "world")

	recorder.rewind()
	mustRead(recorder, "helloworld")
	recorder.rewind()
	mustRead(recorder, "helloworld")
	recorder.rewind()
	mustRead(recorder, "helloworld")

	mustWrite(conn, "!")
	recorder.rewind()
	mustRead(recorder, "helloworld!")
	rawConn := recorder.rewind()
	mustRead(rawConn, "helloworld!")
	mustRead(rawConn, "")
	mustRead(recorder, "")
}

type fakeConn struct {
	net.Conn
	buf bytes.Buffer
}

func (c *fakeConn) Read(b []byte) (n int, err error) {
	return c.buf.Read(b)
}

func (c *fakeConn) Write(b []byte) (n int, err error) {
	return c.buf.Write(b)
}
