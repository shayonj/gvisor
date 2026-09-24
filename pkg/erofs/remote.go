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
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/errors/linuxerr"
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/sync"
	"gvisor.dev/gvisor/pkg/unet"
)

const maxDeviceBytes = 16 << 30

type remoteImage struct {
	mu     sync.Mutex
	socket *unet.Socket
	files  map[uint16]*deviceFile
}

type deviceFile struct {
	file  *os.File
	bytes []byte
	ready []uint64
}

func openRemoteImage(source *os.File) (*Image, error) {
	fd, err := unix.Dup(int(source.Fd()))
	if err != nil {
		return nil, err
	}
	socket, err := unet.NewSocket(fd)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	r := &remoteImage{socket: socket, files: make(map[uint16]*deviceFile)}
	metadata, _, _, err := r.request(0, 0, 0)
	if err != nil {
		socket.Close()
		return nil, err
	}
	stat, err := metadata.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 64<<20 {
		metadata.Close()
		socket.Close()
		return nil, linuxerr.EFBIG
	}
	image, err := OpenImage(metadata)
	if err != nil {
		metadata.Close()
		socket.Close()
		return nil, err
	}
	if image.sb.ExtraDevices > 255 {
		image.Close()
		socket.Close()
		return nil, linuxerr.EFBIG
	}
	image.remote = r
	source.Close()
	return image, nil
}

func (r *remoteImage) request(device uint16, offset, length uint64) (*os.File, uint64, uint64, error) {
	timedOut := make(chan struct{})
	deadline := time.AfterFunc(30*time.Second, func() {
		r.socket.Shutdown()
		close(timedOut)
	})
	defer func() {
		if !deadline.Stop() {
			<-timedOut
		}
	}()
	var request [24]byte
	binary.LittleEndian.PutUint64(request[:8], uint64(device))
	binary.LittleEndian.PutUint64(request[8:16], offset)
	binary.LittleEndian.PutUint64(request[16:], length)
	if n, err := r.socket.Write(request[:]); err != nil || n != len(request) {
		return nil, 0, 0, fmt.Errorf("erofs source write: %d, %v", n, err)
	}
	reader := r.socket.Reader(true)
	reader.EnableFDs(1)
	var response [24]byte
	n, err := reader.ReadVec([][]byte{response[:]})
	defer reader.CloseFDs()
	if err != nil || n != len(response) || binary.LittleEndian.Uint64(response[:8]) != 0 {
		return nil, 0, 0, fmt.Errorf("erofs source response: %d, %v", n, err)
	}
	fds, err := reader.ExtractFDs()
	if err != nil || len(fds) != 1 {
		return nil, 0, 0, linuxerr.EIO
	}
	flags, err := unix.FcntlInt(uintptr(fds[0]), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		return nil, 0, 0, linuxerr.EACCES
	}
	reader.UnpackFDs()
	return os.NewFile(uintptr(fds[0]), "erofs device"), binary.LittleEndian.Uint64(response[8:16]), binary.LittleEndian.Uint64(response[16:]), nil
}

func (r *remoteImage) close() {
	r.socket.Close()
	for _, file := range r.files {
		unix.Munmap(file.bytes)
		file.file.Close()
	}
}

// ResolveRange makes a backing range readable before it is exposed to a mapping.
func (i *Image) ResolveRange(offset, length uint64) (uint16, uint64, error) {
	if i.remote == nil {
		return 0, offset, nil
	}
	if length == 0 || offset+length < offset {
		return 0, 0, linuxerr.EINVAL
	}
	if offset+length <= uint64(len(i.bytes)) {
		return 0, offset, nil
	}
	for device := uint16(1); device <= i.sb.ExtraDevices; device++ {
		info, err := i.BytesAt(uint64(i.sb.DevTableSlotOff)*128+uint64(device-1)*128, 128)
		if err != nil {
			return 0, 0, err
		}
		base := i.sb.BlockAddrToOffset(binary.LittleEndian.Uint32(info[68:72]))
		size := i.sb.BlockAddrToOffset(binary.LittleEndian.Uint32(info[64:68]))
		if offset < base || offset-base >= size {
			continue
		}
		relative := offset - base
		if length > size-relative || size > maxDeviceBytes {
			return 0, 0, linuxerr.EIO
		}
		if err := i.remote.resolve(device, relative, length, size); err != nil {
			return 0, 0, err
		}
		return device, relative, nil
	}
	return 0, 0, linuxerr.EIO
}

func (r *remoteImage) resolve(device uint16, offset, length, size uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	first := offset / hostarch.PageSize
	last := (offset + length - 1) / hostarch.PageSize
	file := r.files[device]
	if file != nil {
		ready := true
		for page := first; page <= last; page++ {
			if file.ready[page/64]&(uint64(1)<<(page%64)) == 0 {
				ready = false
				break
			}
		}
		if ready {
			return nil
		}
	}
	fd, start, end, err := r.request(device, first*hostarch.PageSize, (last-first+1)*hostarch.PageSize)
	if err != nil {
		return err
	}
	if start > first*hostarch.PageSize || end < (last+1)*hostarch.PageSize || end > size {
		fd.Close()
		return linuxerr.EIO
	}
	if file == nil {
		stat, err := fd.Stat()
		if err != nil || !stat.Mode().IsRegular() || uint64(stat.Size()) < size || stat.Size() > maxDeviceBytes {
			fd.Close()
			return linuxerr.EIO
		}
		data, err := unix.Mmap(int(fd.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			fd.Close()
			return err
		}
		file = &deviceFile{file: fd, bytes: data, ready: make([]uint64, (size/hostarch.PageSize+63)/64)}
		r.files[device] = file
	} else {
		previous, previousErr := file.file.Stat()
		current, currentErr := fd.Stat()
		fd.Close()
		if previousErr != nil || currentErr != nil || !os.SameFile(previous, current) {
			return linuxerr.EIO
		}
	}
	for page := (start + hostarch.PageSize - 1) / hostarch.PageSize; page < end/hostarch.PageSize; page++ {
		file.ready[page/64] |= uint64(1) << (page % 64)
	}
	return nil
}

// DeviceFD returns an already resolved device’s read-only backing descriptor.
func (i *Image) DeviceFD(device uint16) int {
	if device == 0 {
		return i.FD()
	}
	i.remote.mu.Lock()
	defer i.remote.mu.Unlock()
	return int(i.remote.files[device].file.Fd())
}

// DeviceBytes returns bytes from an already resolved device range.
func (i *Image) DeviceBytes(device uint16, offset, length uint64) ([]byte, error) {
	if device == 0 {
		return i.BytesAt(offset, length)
	}
	i.remote.mu.Lock()
	defer i.remote.mu.Unlock()
	file := i.remote.files[device]
	if file == nil || length == 0 || offset+length < offset || offset+length > uint64(len(file.bytes)) {
		return nil, linuxerr.EIO
	}
	for page := offset / hostarch.PageSize; page <= (offset+length-1)/hostarch.PageSize; page++ {
		if file.ready[page/64]&(uint64(1)<<(page%64)) == 0 {
			return nil, linuxerr.EIO
		}
	}
	return file.bytes[offset : offset+length], nil
}

// HasRemoteSource reports whether the image uses an external range provider.
func (i *Image) HasRemoteSource() bool {
	return i.remote != nil
}
