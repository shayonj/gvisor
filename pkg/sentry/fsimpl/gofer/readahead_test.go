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

type fillRangeStep struct {
	required, optional memmap.MappableRange
	want               memmap.MappableRange
}

func TestFillRange(t *testing.T) {
	tests := []struct {
		name  string
		steps []fillRangeStep
	}{
		{"sequential stream ramps up", []fillRangeStep{
			{mr(0, 4*kib), mr(0, 2*mib), mr(0, 64*kib)},
			{mr(64*kib, 68*kib), mr(64*kib, 64*kib+2*mib), mr(64*kib, 320*kib)},
			{mr(320*kib, 324*kib), mr(320*kib, 320*kib+2*mib), mr(320*kib, 832*kib)},
			{mr(832*kib, 836*kib), mr(832*kib, 832*kib+2*mib), mr(832*kib, 1856*kib)},
			{mr(1856*kib, 1860*kib), mr(1856*kib, 1856*kib+2*mib), mr(1856*kib, 1856*kib+2*mib)},
			{mr(1856*kib+2*mib, 1860*kib+2*mib), mr(1856*kib+2*mib, 1856*kib+4*mib), mr(1856*kib+2*mib, 1856*kib+4*mib)},
		}},
		{"other miss keeps fixed window", []fillRangeStep{
			{mr(768*kib, 772*kib), mr(768*kib, 2*mib), mr(768*kib, 832*kib)},
			{mr(10*mib, 10*mib+4*kib), mr(10*mib, 12*mib), mr(10*mib, 10*mib+64*kib)},
			{mr(5*mib+16*kib, 5*mib+20*kib), mr(5*mib+8*kib, 5*mib+40*kib), mr(5*mib+8*kib, 5*mib+40*kib)},
			{mr(832*kib, 836*kib), mr(832*kib, 2*mib), mr(832*kib, 896*kib)},
		}},
		{"large request is filled whole", []fillRangeStep{
			{mr(0, 1*mib), mr(0, 4*mib), mr(0, 1*mib)},
			{mr(1*mib, 2*mib), mr(1*mib, 4*mib), mr(1*mib, 3*mib)},
			{mr(3*mib, 7*mib), mr(3*mib, 8*mib), mr(3*mib, 7*mib)},
		}},
		{"optional range bounds fill", []fillRangeStep{
			{mr(0, 4*kib), mr(0, 16*kib), mr(0, 16*kib)},
			{mr(16*kib, 20*kib), mr(16*kib, 24*kib), mr(16*kib, 24*kib)},
			{mr(24*kib, 28*kib), mr(24*kib, 2*mib), mr(24*kib, 56*kib)},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var i inode
			i.dataMu.Lock()
			defer i.dataMu.Unlock()
			for step, fill := range test.steps {
				if got := i.fillRangeLocked(fill.required, fill.optional); got != fill.want {
					t.Errorf("step %d: got window %v, want %v", step, got, fill.want)
				}
			}
		})
	}
}
