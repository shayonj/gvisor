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

	"gvisor.dev/gvisor/pkg/unet"
)

func TestRemoteBackingIdentity(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "same backing", true: "replaced backing"}[replace], func(t *testing.T) {
			client, server, err := unet.SocketPair(true)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			remote := &remoteImage{socket: client, files: make(map[uint16]*deviceFile)}
			defer remote.close()
			path := filepath.Join(t.TempDir(), "data")
			data := make([]byte, 8192)
			data[0], data[4096] = 42, 43
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			first, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second := first
			if replace {
				path = filepath.Join(t.TempDir(), "other")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				second, err = os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer second.Close()
			}
			result := make(chan error, 1)
			go func() {
				for _, file := range []*os.File{first, second} {
					var request [24]byte
					if _, err := server.Read(request[:]); err != nil {
						result <- err
						return
					}
					writer := server.Writer(true)
					writer.PackFDs(int(file.Fd()))
					if _, err := writer.WriteVec([][]byte{rangeResponse(request[:])}); err != nil {
						result <- err
						return
					}
				}
				result <- nil
			}()
			if err := remote.resolve(1, 0, 4096, 8192); err != nil {
				t.Fatal(err)
			}
			image := &Image{remote: remote}
			if _, err := image.DeviceBytes(1, 4096, 1); err == nil {
				t.Fatal("unresolved bytes became readable")
			}
			err = remote.resolve(1, 4096, 4096, 8192)
			if replace && err == nil {
				t.Fatal("accepted replacement backing")
			}
			if !replace && err != nil {
				t.Fatal(err)
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if !replace {
				data, err := image.DeviceBytes(1, 4096, 1)
				if err != nil || data[0] != 43 {
					t.Fatalf("resolved bytes %v, error %v", data, err)
				}
			}
		})
	}
}

func TestRemoteRejectsWritableBacking(t *testing.T) {
	client, server, err := unet.SocketPair(true)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	remote := &remoteImage{socket: client, files: make(map[uint16]*deviceFile)}
	defer remote.close()
	file, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(4096); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		var request [24]byte
		if _, err := server.Read(request[:]); err != nil {
			result <- err
			return
		}
		writer := server.Writer(true)
		writer.PackFDs(int(file.Fd()))
		_, err := writer.WriteVec([][]byte{rangeResponse(request[:])})
		result <- err
	}()
	if err := remote.resolve(1, 0, 4096, 4096); err == nil {
		t.Fatal("accepted writable descriptor")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func rangeResponse(request []byte) []byte {
	response := make([]byte, 24)
	start := binary.LittleEndian.Uint64(request[8:16])
	binary.LittleEndian.PutUint64(response[8:16], start)
	binary.LittleEndian.PutUint64(response[16:], start+binary.LittleEndian.Uint64(request[16:24]))
	return response
}

func TestRemoteVerifiedExtent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end uint64
		valid      bool
		ready      bool
	}{
		{"whole chunk", 0, 16384, true, true},
		{"partial first page", 1, 16384, true, false},
		{"partial last page", 4096, 8193, true, false},
		{"starts after request", 4097, 16384, false, false},
		{"ends before request", 0, 8191, false, false},
		{"exceeds device", 0, 16385, false, false},
		{"reversed extent", 8192, 4096, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server, err := unet.SocketPair(true)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			remote := &remoteImage{socket: client, files: make(map[uint16]*deviceFile)}
			defer remote.close()
			path := filepath.Join(t.TempDir(), "data")
			if err := os.WriteFile(path, make([]byte, 16384), 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			result := make(chan error, 1)
			go func() {
				var request [24]byte
				if _, err := server.Read(request[:]); err != nil {
					result <- err
					return
				}
				response := make([]byte, 24)
				binary.LittleEndian.PutUint64(response[8:16], tc.start)
				binary.LittleEndian.PutUint64(response[16:], tc.end)
				writer := server.Writer(true)
				writer.PackFDs(int(file.Fd()))
				_, err := writer.WriteVec([][]byte{response})
				result <- err
			}()
			err = remote.resolve(1, 4096, 4096, 16384)
			if (err == nil) != tc.valid {
				t.Fatalf("resolve error %v", err)
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			server.Close()
			if !tc.valid {
				return
			}
			image := &Image{remote: remote}
			if _, err := image.DeviceBytes(1, 4096, 4096); err != nil {
				t.Fatal(err)
			}
			if tc.ready {
				if err := remote.resolve(1, 0, 16384, 16384); err != nil {
					t.Fatalf("verified chunk required another request: %v", err)
				}
			} else {
				if _, err := image.DeviceBytes(1, 0, 16384); err == nil {
					t.Fatal("unverified partial page became readable")
				}
			}
		})
	}
}
