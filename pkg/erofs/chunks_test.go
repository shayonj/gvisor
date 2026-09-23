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
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/marshal"
)

func chunkImage(t *testing.T, mutate func([]byte)) *Image {
	t.Helper()
	data := make([]byte, 16*4096)
	sb := SuperBlock{Magic: SuperBlockMagicV1, BlockSizeBits: 12, RootNid: 128, Blocks: 16, FeatureIncompat: FeatureIncompatChunkedFile | FeatureIncompatDeviceTable, ExtraDevices: 2, DevTableSlotOff: 16}
	copy(data[SuperBlockOffset:], marshal.Marshal(&sb))
	ino := InodeCompact{Format: InodeDataLayoutChunkBased << InodeDataLayoutBit, Mode: linux.S_IFREG | 0444, Nlink: 1, Size: 8193, RawBlockAddr: 0x20}
	copy(data[4096:], marshal.Marshal(&ino))
	for index, base := range []uint32{8, 12} {
		slot := 2048 + index*128
		binary.LittleEndian.PutUint32(data[slot+64:], 4)
		binary.LittleEndian.PutUint32(data[slot+68:], base)
	}
	for index, device := range []uint16{1, 2, 1} {
		slot := 4096 + InodeCompactSize + index*8
		binary.LittleEndian.PutUint16(data[slot+2:], device)
		binary.LittleEndian.PutUint32(data[slot+4:], uint32(index))
	}
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
		f.Close()
		t.Fatal(err)
	}
	t.Cleanup(image.Close)
	return image
}

func TestChunkDeviceRanges(t *testing.T) {
	image := chunkImage(t, nil)
	inode, err := image.Inode(image.RootNid())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ offset, backing, length uint64 }{
		{0, 8 * 4096, 4096},
		{4095, 9*4096 - 1, 1},
		{4096, 13 * 4096, 4096},
		{8192, 10 * 4096, 4096},
	} {
		rng, err := inode.DataRangeAt(tc.offset)
		if err != nil || rng.Off != tc.backing || rng.Size != tc.length {
			t.Fatalf("offset %d: range %+v, error %v; want backing %d, length %d", tc.offset, rng, err, tc.backing, tc.length)
		}
	}
	if _, err := inode.DataRangeAt(12288); err == nil {
		t.Fatal("accepted offset past padded EOF")
	}
}

func TestInvalidChunkDeviceRanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"missing device", func(data []byte) { binary.LittleEndian.PutUint16(data[4096+InodeCompactSize+2:], 3) }},
		{"device overflow", func(data []byte) { binary.LittleEndian.PutUint32(data[4096+InodeCompactSize+4:], 4) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := chunkImage(t, tc.mutate)
			inode, err := image.Inode(image.RootNid())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inode.DataRangeAt(0); err == nil {
				t.Fatal("accepted invalid data range")
			}
		})
	}
}
