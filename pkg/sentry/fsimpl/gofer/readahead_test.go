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

package gofer

import (
	"testing"

	"gvisor.dev/gvisor/pkg/sentry/memmap"
)

const (
	kib = 1 << 10
	mib = 1 << 20
)

func mr(start, end uint64) memmap.MappableRange {
	return memmap.MappableRange{Start: start, End: end}
}

func fillStream(i *inode, start, req uint64, n int) []memmap.MappableRange {
	var windows []memmap.MappableRange
	off := start
	for range n {
		w := i.fillRange(mr(off, off+req), mr(off, off+readaheadMax))
		windows = append(windows, w)
		off = w.End
	}
	return windows
}

func TestFillRangeSequentialStreamRampsUp(t *testing.T) {
	var i inode
	want := []memmap.MappableRange{
		mr(0, 64*kib),
		mr(64*kib, 320*kib),
		mr(320*kib, 832*kib),
		mr(832*kib, 1856*kib),
		mr(1856*kib, 1856*kib+2*mib),
		mr(1856*kib+2*mib, 1856*kib+4*mib),
	}
	got := fillStream(&i, 0, 4*kib, len(want))
	for n := range want {
		if got[n] != want[n] {
			t.Errorf("miss %d: got window %v, want %v", n, got[n], want[n])
		}
	}
}

func TestFillRangeOtherMissKeepsFixedWindow(t *testing.T) {
	var i inode
	fillStream(&i, 0, 4*kib, 3)
	for _, tc := range []struct {
		name               string
		required, optional memmap.MappableRange
		want               memmap.MappableRange
	}{
		{"far away", mr(10*mib, 10*mib+4*kib), mr(10*mib, 12*mib), mr(10*mib, 10*mib+64*kib)},
		{"small hole", mr(5*mib+16*kib, 5*mib+20*kib), mr(5*mib+8*kib, 5*mib+40*kib), mr(5*mib+8*kib, 5*mib+40*kib)},
		{"interrupted stream", mr(832*kib, 836*kib), mr(832*kib, 2*mib), mr(832*kib, 896*kib)},
	} {
		if got := i.fillRange(tc.required, tc.optional); got != tc.want {
			t.Errorf("%s: got window %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFillRangeLargeRequestIsReadWhole(t *testing.T) {
	var i inode
	for _, tc := range []struct {
		name               string
		required, optional memmap.MappableRange
		want               memmap.MappableRange
	}{
		{"first request", mr(0, 1*mib), mr(0, 4*mib), mr(0, 1*mib)},
		{"sequential request", mr(1*mib, 2*mib), mr(1*mib, 4*mib), mr(1*mib, 3*mib)},
		{"oversized request", mr(3*mib, 7*mib), mr(3*mib, 8*mib), mr(3*mib, 7*mib)},
	} {
		if got := i.fillRange(tc.required, tc.optional); got != tc.want {
			t.Errorf("%s: got window %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFillRangeIsBoundedByOptional(t *testing.T) {
	var i inode
	for _, tc := range []struct {
		name               string
		required, optional memmap.MappableRange
		want               memmap.MappableRange
	}{
		{"first miss", mr(0, 4*kib), mr(0, 16*kib), mr(0, 16*kib)},
		{"sequential miss", mr(16*kib, 20*kib), mr(16*kib, 24*kib), mr(16*kib, 24*kib)},
		{"after clipping", mr(24*kib, 28*kib), mr(24*kib, 2*mib), mr(24*kib, 56*kib)},
	} {
		if got := i.fillRange(tc.required, tc.optional); got != tc.want {
			t.Errorf("%s: got window %v, want %v", tc.name, got, tc.want)
		}
	}
}
