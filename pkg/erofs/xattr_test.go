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

package erofs

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/errors/linuxerr"
	"gvisor.dev/gvisor/pkg/marshal"
)

// Layout of the image built by xattrImage. Its on-disk blocks are 4 KiB,
// independently of the host page size.
const (
	inlineNid      = 0
	chunkNid       = 32
	inlineIbodyOff = 4096 + InodeCompactSize
	chunkIbodyOff  = 5120 + InodeExtendedSize
	sharedXattrOff = 3 * 4096
	sharedUserID   = 0
	chunkDataBlock = 5
)

var inlineData = bytes.Repeat([]byte("inline"), 20)

type testXattr struct {
	index uint8
	name  string
	value []byte
}

// putXattrEntry writes an entry at data[off:] and returns its padded size.
func putXattrEntry(data []byte, off int, x testXattr) int {
	data[off] = uint8(len(x.name))
	data[off+1] = x.index
	binary.LittleEndian.PutUint16(data[off+2:], uint16(len(x.value)))
	copy(data[off+xattrEntrySize:], x.name)
	copy(data[off+xattrEntrySize+len(x.name):], x.value)
	return (xattrEntrySize + len(x.name) + len(x.value) + 3) &^ 3
}

// putXattrIbody writes an xattr body at data[off:] and returns its count.
func putXattrIbody(data []byte, off int, shared []uint32, inline []testXattr) uint16 {
	data[off+4] = uint8(len(shared))
	pos := off + xattrIbodyHeaderSize
	for _, id := range shared {
		binary.LittleEndian.PutUint32(data[pos:], id)
		pos += 4
	}
	for _, x := range inline {
		pos += putXattrEntry(data, pos, x)
	}
	return uint16((pos-off-xattrIbodyHeaderSize)/4 + 1)
}

// xattrImage returns a compact inline inode and an extended chunked inode
// with xattrs.
func xattrImage(t *testing.T, mutate func([]byte)) *Image {
	t.Helper()
	data := make([]byte, 6*4096)
	sb := SuperBlock{Magic: SuperBlockMagicV1, BlockSizeBits: 12, Blocks: 6, MetaBlockAddr: 1, XattrBlockAddr: 3, FeatureIncompat: FeatureIncompatChunkedFile}
	copy(data[SuperBlockOffset:], marshal.Marshal(&sb))

	putXattrEntry(data, sharedXattrOff+sharedUserID*4, testXattr{xattrIndexUser, "shared", []byte("s")})

	count := putXattrIbody(data, inlineIbodyOff, []uint32{sharedUserID}, []testXattr{
		{xattrIndexUser, "inline", []byte("inline-value")},
		{0x80 | xattrIndexUser, "long", []byte("v")},
	})
	ino := InodeCompact{Format: InodeDataLayoutFlatInline << InodeDataLayoutBit, XattrCount: count, Mode: linux.S_IFREG | 0755, Nlink: 1, Size: uint32(len(inlineData))}
	copy(data[4096:], marshal.Marshal(&ino))
	copy(data[inlineIbodyOff+int(xattrIbodySize(count)):], inlineData)

	count = putXattrIbody(data, chunkIbodyOff, []uint32{sharedUserID}, []testXattr{
		{xattrIndexUser, "chunk", []byte("c")},
	})
	ext := InodeExtended{Format: InodeLayoutExtended<<InodeLayoutBit | InodeDataLayoutChunkBased<<InodeDataLayoutBit, XattrCount: count, Mode: linux.S_IFREG | 0644, Nlink: 1, Size: 4096, RawBlockAddr: 0x20}
	copy(data[5120:], marshal.Marshal(&ext))
	// This xattr body requires padding before the 8-byte chunk indexes.
	chunkIndexOff := (chunkIbodyOff + int(xattrIbodySize(count)) + 7) &^ 7
	binary.LittleEndian.PutUint32(data[chunkIndexOff+4:], chunkDataBlock)

	if mutate != nil {
		mutate(data)
	}
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	image, err := OpenImage(f)
	if err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	t.Cleanup(image.Close)
	return image
}

// TestUnknownXattrIndex checks that unsupported disk entries are skipped.
func TestUnknownXattrIndex(t *testing.T) {
	image := xattrImage(t, nil)
	inode, err := image.Inode(inlineNid)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"user.inline", "user.shared"}
	if names, err := inode.ListXattrs(); err != nil || !slices.Equal(names, want) {
		t.Errorf("ListXattrs() = %q, %v; want %q", names, err, want)
	}
	if _, err := inode.Xattr("user.long"); !linuxerr.Equals(linuxerr.ENODATA, err) {
		t.Errorf("Xattr(user.long) error = %v, want ENODATA", err)
	}
}

// TestXattrDataOffsets checks data locations after an inode's xattr storage.
func TestXattrDataOffsets(t *testing.T) {
	image := xattrImage(t, nil)
	inline, err := image.Inode(inlineNid)
	if err != nil {
		t.Fatal(err)
	}
	extent, err := inline.MapBlocks(0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := image.BytesAt(extent.ImageOff, uint64(len(inlineData)))
	if err != nil || !bytes.Equal(got, inlineData) {
		t.Errorf("inline data = %q, %v; want %q", got, err, inlineData)
	}
	chunk, err := image.Inode(chunkNid)
	if err != nil {
		t.Fatal(err)
	}
	if extent, err := chunk.MapBlocks(0); err != nil || extent.ImageOff != chunkDataBlock*4096 {
		t.Errorf("MapBlocks(0) = %v, %v; want image offset %d", extent, err, chunkDataBlock*4096)
	}
}

// TestInvalidXattrs checks truncated and out-of-range xattr metadata.
func TestInvalidXattrs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		nid    uint64
		mutate func([]byte)
	}{
		{"shared ids past ibody", inlineNid, func(data []byte) { data[inlineIbodyOff+4] = 255 }},
		{"inline entry past ibody", inlineNid, func(data []byte) {
			binary.LittleEndian.PutUint16(data[4096+2:], 2)
			data[inlineIbodyOff+4] = 0
			putXattrEntry(data, inlineIbodyOff+xattrIbodyHeaderSize, testXattr{xattrIndexUser, "x", nil})
		}},
		{"shared value past image", inlineNid, func(data []byte) {
			binary.LittleEndian.PutUint16(data[sharedXattrOff+sharedUserID*4+2:], 0xffff)
		}},
		{"shared id past image", chunkNid, func(data []byte) {
			binary.LittleEndian.PutUint32(data[chunkIbodyOff+xattrIbodyHeaderSize:], ^uint32(0))
		}},
		{"ibody past image", inlineNid, func(data []byte) { binary.LittleEndian.PutUint16(data[4096+2:], 0xffff) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := xattrImage(t, tc.mutate)
			inode, err := image.Inode(tc.nid)
			if err != nil {
				if !linuxerr.Equals(linuxerr.EUCLEAN, err) {
					t.Fatalf("Inode() error = %v, want EUCLEAN", err)
				}
				return
			}
			if _, err := inode.ListXattrs(); !linuxerr.Equals(linuxerr.EUCLEAN, err) {
				t.Errorf("ListXattrs() error = %v, want EUCLEAN", err)
			}
			if _, err := inode.Xattr("user.missing"); !linuxerr.Equals(linuxerr.EUCLEAN, err) {
				t.Errorf("Xattr() error = %v, want EUCLEAN", err)
			}
		})
	}
}

// TestReservedXattrBody checks the undefined header-only xattr representation.
func TestReservedXattrBody(t *testing.T) {
	image := xattrImage(t, func(data []byte) {
		binary.LittleEndian.PutUint16(data[4096+2:], 1)
	})
	inode, err := image.Inode(inlineNid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inode.ListXattrs(); !linuxerr.Equals(linuxerr.EOPNOTSUPP, err) {
		t.Errorf("ListXattrs() error = %v, want EOPNOTSUPP", err)
	}
}

// TestPOSIXACLIndexes checks both on-disk ACL indexes.
func TestPOSIXACLIndexes(t *testing.T) {
	for _, index := range []uint8{xattrIndexPosixACLAccess, xattrIndexPosixACLDefault} {
		image := xattrImage(t, func(data []byte) {
			count := putXattrIbody(data, inlineIbodyOff, nil, []testXattr{
				{index, "", []byte{2, 0, 0, 0}},
			})
			binary.LittleEndian.PutUint16(data[4096+2:], count)
		})
		inode, err := image.Inode(inlineNid)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := inode.HasPOSIXACL(); err != nil || !got {
			t.Errorf("index %d: HasPOSIXACL() = %v, %v; want true", index, got, err)
		}
	}
}
