// Copyright 2026 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hostinet

import (
	"context"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/fdnotifier"
	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/waiter"
)

// Close-on-save for hostinet sockets is unconditional and applies even under
// --leave-running and --net-disconnect-ok=false: host fds cannot be serialized
// so the only choice is to close them. Applications see EBADF on the next I/O.
// epoll_wait returns immediately because Readiness short-circuits on fd<0.

// beforeSave is invoked by stateify. The host fd cannot be serialized because
// the post-restore process will have a different fd table, so we release it
// here and leave the Socket in a closed state. Application I/O on the restored
// Socket fails with EBADF, mirroring the close-on-save pattern used for
// SCMConnectedEndpoint in pkg/sentry/socket/unix/transport.
func (s *Socket) beforeSave() {
	if s.fd < 0 {
		return
	}
	// Mark the receive side closed so any task currently blocked in RecvMsg
	// observes s.recvClosed and bails out instead of looping on the dead fd.
	s.recvClosed.Store(true)
	fdnotifier.RemoveFD(int32(s.fd))
	// Best-effort shutdown to deliver EOF to peers; ignore errors because the
	// connection may already be torn down.
	_ = unix.Shutdown(s.fd, unix.SHUT_RDWR)
	if err := unix.Close(s.fd); err != nil {
		log.Warningf("hostinet.Socket: failed to close host fd %d: %v", s.fd, err)
	}
	s.fd = -1
	// Wake any waiters so blocked tasks unblock and observe EBADF on their
	// next syscall attempt.
	s.queue.Notify(waiter.EventIn | waiter.EventOut | waiter.EventHUp | waiter.EventErr)
}

// afterLoad is invoked by stateify after the Socket has been deserialized.
// The host fd was released by beforeSave, so any I/O against this Socket on
// the new host will fail with EBADF.
func (s *Socket) afterLoad(context.Context) {
	s.fd = -1
}
