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
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/sentry/memmap"
)

const readaheadMax = hostarch.HugePageSize

// readahead is the window of the most recent page cache fill of an inode.
//
// +stateify savable
type readahead struct {
	start uint64
	end   uint64
}

// fillRange returns the range to fill for a page cache miss at required.
//
// Preconditions: i.dataMu must be locked.
func (i *inode) fillRange(required, optional memmap.MappableRange) memmap.MappableRange {
	mr := maxFillRange(required, optional)
	if required.Start == i.ra.end && i.ra.end != 0 {
		size := max(nextReadaheadSize(i.ra.end-i.ra.start, readaheadMax), required.Length())
		mr = memmap.MappableRange{Start: required.Start, End: min(required.Start+size, optional.End)}
	}
	i.ra = readahead{mr.Start, mr.End}
	return mr
}

// nextReadaheadSize is Linux's get_next_ra_size.
func nextReadaheadSize(cur, max uint64) uint64 {
	if cur < max/16 {
		return 4 * cur
	}
	if cur <= max/2 {
		return 2 * cur
	}
	return max
}
