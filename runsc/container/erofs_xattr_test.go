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

package container

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"

	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/sentry/fsimpl/erofs"
	"gvisor.dev/gvisor/pkg/test/testutil"
)

// setOptionalXattr sets a privileged attribute if the host permits it.
func setOptionalXattr(t *testing.T, path, name string, value []byte) bool {
	t.Helper()
	if err := unix.Setxattr(path, name, value, 0); err != nil {
		if err != unix.EPERM && err != unix.EOPNOTSUPP {
			t.Fatalf("Setxattr(%q, %q): %v", path, name, err)
		}
		t.Logf("Host does not permit fixture attribute %q: %v", name, err)
		return false
	}
	return true
}

// createXattrSource creates the immutable fixture for the syscall tests.
func createXattrSource(t *testing.T, source string) {
	t.Helper()
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file", "shared", "execute-only", "large", "directory"} {
		path := filepath.Join(source, name)
		if name == "directory" {
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
		} else {
			contents := []byte("erofs xattr contents\n")
			if name == "large" {
				contents = bytes.Repeat([]byte{'q'}, 8193)
			}
			if err := os.WriteFile(path, contents, 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := unix.Setxattr(path, "user.test", []byte("test-value"), 0); err != nil {
			if err == unix.EOPNOTSUPP {
				t.Skip("fixture filesystem does not support user xattrs")
			}
			t.Fatal(err)
		}
		if err := unix.Setxattr(path, "user.unique", []byte(name), 0); err != nil {
			t.Fatal(err)
		}
		if name == "file" || name == "shared" {
			for key, value := range map[string][]byte{
				"user.binary": {0, 1, 255, 0},
				"user.empty":  {},
				"user.shared": []byte("shared-value"),
			} {
				if err := unix.Setxattr(path, key, value, 0); err != nil {
					t.Fatal(err)
				}
			}
		}
		if name == "execute-only" {
			if err := os.Chmod(path, 0111); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}

	trusted := filepath.Join(source, "trusted")
	if err := os.WriteFile(trusted, nil, 0444); err != nil {
		t.Fatal(err)
	}
	if !setOptionalXattr(t, trusted, "trusted.test", []byte("trusted")) {
		if err := os.Remove(trusted); err != nil {
			t.Fatal(err)
		}
	}

	aclPath := filepath.Join(source, "acl")
	if err := os.WriteFile(aclPath, nil, 0444); err != nil {
		t.Fatal(err)
	}
	acl := make([]byte, 44)
	binary.LittleEndian.PutUint32(acl, linux.POSIX_ACL_XATTR_VERSION)
	for j, entry := range []struct {
		tag  uint16
		perm uint16
		id   uint32
	}{
		{linux.ACL_USER_OBJ, linux.ACL_READ, linux.ACL_UNDEFINED_ID},
		{linux.ACL_USER, 0, 0},
		{linux.ACL_GROUP_OBJ, linux.ACL_READ, linux.ACL_UNDEFINED_ID},
		{linux.ACL_MASK, linux.ACL_READ, linux.ACL_UNDEFINED_ID},
		{linux.ACL_OTHER, linux.ACL_READ, linux.ACL_UNDEFINED_ID},
	} {
		off := 4 + j*8
		binary.LittleEndian.PutUint16(acl[off:], entry.tag)
		binary.LittleEndian.PutUint16(acl[off+2:], entry.perm)
		binary.LittleEndian.PutUint32(acl[off+4:], entry.id)
	}
	if setOptionalXattr(t, aclPath, linux.XATTR_NAME_POSIX_ACL_ACCESS, acl) {
		if err := os.WriteFile(filepath.Join(source, "acl-present"), nil, 0444); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(aclPath); err != nil {
		t.Fatal(err)
	}

	helper, err := testutil.FindFile("test/syscalls/linux/exec_check_creds")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	capability := filepath.Join(source, "capability")
	if err := os.WriteFile(capability, contents, 0755); err != nil {
		t.Fatal(err)
	}
	caps := make([]byte, linux.XATTR_CAPS_SZ_2)
	binary.LittleEndian.PutUint32(caps, linux.VFS_CAP_REVISION_2|linux.VFS_CAP_FLAGS_EFFECTIVE)
	binary.LittleEndian.PutUint32(caps[4:], 1<<linux.CAP_NET_BIND_SERVICE)
	if setOptionalXattr(t, capability, linux.XATTR_SECURITY_CAPABILITY, caps) {
		if err := os.Chmod(capability, 0111); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(capability); err != nil {
		t.Fatal(err)
	}
}

// TestXattrsEROFS runs syscall tests against inline and shared attributes.
func TestXattrsEROFS(t *testing.T) {
	skipIfNotAvailable(t, "mkfs.erofs")
	testDir := t.TempDir()
	source := filepath.Join(testDir, "source")
	createXattrSource(t, source)
	binary, err := testutil.FindFile("test/syscalls/linux/xattr_test")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	// No attribute can occur more often than there are fixture inodes.
	inlineTolerance := strconv.Itoa(len(entries) + 1)
	// Executables require block-aligned data because EROFS Translate does
	// not support tail-packed inline data. Xattrs can still be inline.
	for _, format := range []struct {
		name    string
		options []string
	}{
		{"inline", []string{"-T", "0", "-x", inlineTolerance, "-E", "force-inode-compact,noinline_data"}},
		{"shared", []string{"-T", "0", "-x", "0", "-E", "noinline_data"}},
		{"extended", []string{"-T", "0", "-x", inlineTolerance, "-E", "force-inode-extended,noinline_data"}},
		{"chunked", []string{"-T", "0", "-x", "1", "--chunksize=" + strconv.Itoa(hostarch.PageSize)}},
	} {
		t.Run(format.name, func(t *testing.T) {
			image := filepath.Join(testDir, format.name+".erofs")
			if err := createImageEROFS(image, source, format.options...); err != nil {
				t.Fatal(err)
			}
			for platform, conf := range configs(t, true) {
				t.Run(platform, func(t *testing.T) {
					spec := testutil.NewSpecWithArgs(binary, "--gtest_filter=ErofsXattrTest.*", "--xattr_fixture_dir=/mnt")
					spec.Process.Env = append(spec.Process.Env, "TEST_ON_GVISOR="+conf.Platform)
					spec.Mounts = append(spec.Mounts, specs.Mount{Source: image, Destination: "/mnt", Type: erofs.Name, Options: []string{"ro"}})
					_, bundle, cleanup, err := testutil.SetupContainer(spec, conf)
					if err != nil {
						t.Fatal(err)
					}
					defer cleanup()
					c, err := New(conf, Args{ID: testutil.RandomContainerID(), Spec: spec, BundleDir: bundle})
					if err != nil {
						t.Fatal(err)
					}
					defer c.Destroy()
					if err := c.Start(conf); err != nil {
						t.Fatal(err)
					}
					status, err := c.Wait()
					if err != nil || !status.Exited() || status.ExitStatus() != 0 {
						t.Fatalf("xattr syscall tests: status %v, error %v", status, err)
					}
				})
			}
		})
	}
}
