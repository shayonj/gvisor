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

import "slices"

type xattrCache struct {
	// xattrs caches extended attributes.
	xattrs map[string]string

	// xattrsNegative tracks xattrs known not to exist (linuxerr.ENODATA).
	xattrsNegative map[string]struct{}

	// xattrsList caches the full list of xattr names. If nil, the list is not
	// cached. If non-nil but empty, the cache is authoritatively empty.
	xattrsList []string
}

func (i *inode) getCachedXattr(name string) (val string, negative bool, found bool) {
	if !i.cachedMetadataAuthoritative() {
		return
	}
	i.metadataMu.Lock()
	defer i.metadataMu.Unlock()
	if cachedVal, ok := i.xattrCache.xattrs[name]; ok {
		found = true
		val = cachedVal
		return
	}
	if _, ok := i.xattrCache.xattrsNegative[name]; ok {
		found = true
		negative = true
		return
	}
	return
}

func (i *inode) cacheXattr(name string, value string) {
	if !i.cachedMetadataAuthoritative() {
		return
	}
	i.metadataMu.Lock()
	defer i.metadataMu.Unlock()
	if i.xattrCache.xattrs == nil {
		i.xattrCache.xattrs = make(map[string]string)
	}
	i.xattrCache.xattrs[name] = value
	delete(i.xattrCache.xattrsNegative, name)
	if i.xattrCache.xattrsList != nil {
		if !slices.Contains(i.xattrCache.xattrsList, name) {
			i.xattrCache.xattrsList = append(i.xattrCache.xattrsList, name)
		}
	}
}

func (i *inode) cacheNegativeXattr(name string) {
	if !i.cachedMetadataAuthoritative() {
		return
	}
	i.metadataMu.Lock()
	defer i.metadataMu.Unlock()
	delete(i.xattrCache.xattrs, name)
	if i.xattrCache.xattrsNegative == nil {
		i.xattrCache.xattrsNegative = make(map[string]struct{})
	}
	i.xattrCache.xattrsNegative[name] = struct{}{}
	if i.xattrCache.xattrsList != nil {
		i.xattrCache.xattrsList = slices.DeleteFunc(i.xattrCache.xattrsList, func(n string) bool { return n == name })
	}
}

func (i *inode) getCachedListXattr() ([]string, bool) {
	if !i.cachedMetadataAuthoritative() {
		return nil, false
	}
	i.metadataMu.Lock()
	defer i.metadataMu.Unlock()
	if i.xattrCache.xattrsList == nil {
		return nil, false
	}
	// Return a copy to prevent a concurrent cacheXattr() from causing a race.
	return append([]string(nil), i.xattrCache.xattrsList...), true
}

func (i *inode) cacheListXattr(names []string) {
	if !i.cachedMetadataAuthoritative() {
		return
	}
	i.metadataMu.Lock()
	defer i.metadataMu.Unlock()
	if len(names) == 0 {
		i.xattrCache.xattrsList = []string{}
	} else {
		// Save a copy to prevent a concurrent cacheXattr() from causing a race.
		i.xattrCache.xattrsList = append([]string(nil), names...)
	}
}
