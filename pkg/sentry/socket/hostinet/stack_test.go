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
	"testing"
)

// TestReplaceConfigCopiesHostState exercises the path used during restore:
// a fresh post-boot stack is Configured, the deserialized stack absorbs that
// configuration via ReplaceConfig, and the deserialized stack ends up with
// live /proc/net/{dev,snmp} handles and the post-boot allowedSocketTypes.
func TestReplaceConfigCopiesHostState(t *testing.T) {
	fresh := NewStack()
	if err := fresh.Configure(true /* allowRawSockets */); err != nil {
		t.Fatalf("fresh.Configure: %v", err)
	}
	if fresh.netDevFile == nil {
		t.Fatalf("fresh.netDevFile is nil after Configure")
	}
	if len(fresh.allowedSocketTypes) == 0 {
		t.Fatalf("fresh.allowedSocketTypes is empty after Configure(true)")
	}

	restored := &Stack{}
	if restored.netDevFile != nil || restored.netSNMPFile != nil {
		t.Fatalf("deserialized stack has unexpected proc handles")
	}
	if restored.configured {
		t.Fatalf("deserialized stack already marked configured")
	}

	restored.ReplaceConfig(fresh)

	if restored.netDevFile != fresh.netDevFile {
		t.Errorf("netDevFile not copied: got %v want %v", restored.netDevFile, fresh.netDevFile)
	}
	if restored.netSNMPFile != fresh.netSNMPFile {
		t.Errorf("netSNMPFile not copied: got %v want %v", restored.netSNMPFile, fresh.netSNMPFile)
	}
	if !restored.configured {
		t.Errorf("configured not propagated")
	}
	if len(restored.allowedSocketTypes) != len(fresh.allowedSocketTypes) {
		t.Errorf("allowedSocketTypes len mismatch: got %d want %d",
			len(restored.allowedSocketTypes), len(fresh.allowedSocketTypes))
	}
	if restored.tcpRecvBufSize != fresh.tcpRecvBufSize {
		t.Errorf("tcpRecvBufSize mismatch: got %+v want %+v",
			restored.tcpRecvBufSize, fresh.tcpRecvBufSize)
	}

	if _, err := restored.netDevFile.Stat(); err != nil {
		t.Errorf("netDevFile.Stat after ReplaceConfig: %v", err)
	}
}

// TestConfigureIdempotent ensures Configure can be called twice on the same
// stack without re-opening /proc handles or re-parsing host sysctls. The
// restore path relies on this so createNetworkStackForRestore can Configure
// even when the upstream caller (Loader.run) will Configure again.
func TestConfigureIdempotent(t *testing.T) {
	s := NewStack()
	if err := s.Configure(false /* allowRawSockets */); err != nil {
		t.Fatalf("first Configure: %v", err)
	}
	firstDev := s.netDevFile
	firstSNMP := s.netSNMPFile
	firstAllowedLen := len(s.allowedSocketTypes)

	if err := s.Configure(true /* would have appended raw if not idempotent */); err != nil {
		t.Fatalf("second Configure: %v", err)
	}
	if s.netDevFile != firstDev {
		t.Errorf("second Configure reopened netDevFile")
	}
	if s.netSNMPFile != firstSNMP {
		t.Errorf("second Configure reopened netSNMPFile")
	}
	if len(s.allowedSocketTypes) != firstAllowedLen {
		t.Errorf("second Configure mutated allowedSocketTypes: got %d want %d",
			len(s.allowedSocketTypes), firstAllowedLen)
	}
}
